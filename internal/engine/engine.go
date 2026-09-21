package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/potibm/shiphoist/internal/core"
)

// Pipeline orchestrates the entire update lifecycle.
// It ties together discovery, remote fetching, and local patching.
type Pipeline struct {
	Discoverer core.Discoverer
	Fetcher    core.RegistryFetcher
	Patcher    core.Patcher
	Prompter   core.Prompter
}

// ProcessFile takes the raw file content, runs it through the update pipeline,
// and returns the modified content along with a list of successfully applied updates.
func (p *Pipeline) ProcessFile(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
	updates, err := p.Discoverer.Discover(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("discovery phase failed for %s: %w", filePath, err)
	}

	totalUpdates := len(updates)
	if totalUpdates == 0 {
		return nil, nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var fetchedUpdates []core.ImageUpdate // Umbenannt von appliedUpdates zur Klarheit

	// Thread-sicherer Zähler für unseren Fortschritt
	var completed int32

	// 1. Fetch updates in parallel
	for _, u := range updates {
		wg.Add(1)
		go p.fetchSingle(ctx, u, totalUpdates, &completed, &wg, &mu, &fetchedUpdates)
	}

	wg.Wait()

	if len(fetchedUpdates) == 0 {
		return nil, nil
	}

	// 2. Prompt (Interaktive Auswahl) - JETZT mit den fertigen fetchedUpdates!
	selectedUpdates := fetchedUpdates
	if p.Prompter != nil {
		selectedUpdates, err = p.Prompter.SelectUpdates(fetchedUpdates)
		if err != nil {
			return nil, err // z.B. wenn der User mit Ctrl+C abbricht
		}
	}

	if len(selectedUpdates) == 0 {
		return nil, nil
	}

	// 3. Patch - Nur mit dem, was der User in der TUI ausgewählt hat!
	err = p.Patcher.Patch(ctx, filePath, selectedUpdates)
	if err != nil {
		return nil, fmt.Errorf("failed to patch file %s: %w", filePath, err)
	}

	return selectedUpdates, nil
}

// fetchSingle kümmert sich isoliert um ein einziges Image.
// Das senkt die cyclomatische Komplexität der Hauptmethode und kapselt Fehlerbehandlung und Timeouts.
func (p *Pipeline) fetchSingle(
	ctx context.Context,
	update core.ImageUpdate,
	total int,
	completed *int32,
	wg *sync.WaitGroup,
	mu *sync.Mutex,
	appliedUpdates *[]core.ImageUpdate,
) {
	defer wg.Done()

	// Eigener Timeout pro Image
	imgCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	updated, err := p.Fetcher.FetchUpdate(imgCtx, update)

	// Fortschritt thread-sicher um 1 erhöhen
	currentProgress := atomic.AddInt32(completed, 1)

	if err != nil {
		fmt.Printf("\r⚠️  [%d/%d] Skipping %s: registry error (%v)\n", currentProgress, total, update.ImageName, err)
		return
	}

	// Visuelles Feedback für den Nutzer
	fmt.Printf("\r✅ [%d/%d] Checked %s\n", currentProgress, total, update.ImageName)

	if updated.Selected {
		mu.Lock()
		*appliedUpdates = append(*appliedUpdates, updated)
		mu.Unlock()
	}
}
