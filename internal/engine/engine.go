package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/potibm/shiphoist/internal/core"
)

const imageProcessingTimeout = 60

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

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	var fetchedUpdates []core.ImageUpdate

	var completed int32

	for _, u := range updates {
		wg.Add(1)
		go p.fetchSingle(ctx, u, totalUpdates, &completed, &wg, &mu, &fetchedUpdates)
	}

	wg.Wait()

	if len(fetchedUpdates) == 0 {
		return nil, nil
	}

	selectedUpdates := fetchedUpdates
	if p.Prompter != nil {
		selectedUpdates, err = p.Prompter.SelectUpdates(fetchedUpdates)
		if err != nil {
			return nil, err
		}
	}

	if len(selectedUpdates) == 0 {
		return nil, nil
	}

	err = p.Patcher.Patch(ctx, filePath, selectedUpdates)
	if err != nil {
		return nil, fmt.Errorf("failed to patch file %s: %w", filePath, err)
	}

	return selectedUpdates, nil
}

// fetchSingle handles a single image update in isolation.
// This reduces the cyclomatic complexity of the main method and encapsulates error handling and timeouts.
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

	imgCtx, cancel := context.WithTimeout(ctx, imageProcessingTimeout*time.Second)
	defer cancel()

	updated, err := p.Fetcher.FetchUpdate(imgCtx, update)

	currentProgress := atomic.AddInt32(completed, 1)

	if err != nil {
		fmt.Printf("\r⚠️  [%d/%d] Skipping %s: registry error (%v)\n", currentProgress, total, update.ImageName, err)

		return
	}

	fmt.Printf("\r✅ [%d/%d] Checked %s\n", currentProgress, total, update.ImageName)

	if updated.Selected {
		mu.Lock()

		*appliedUpdates = append(*appliedUpdates, updated)
		mu.Unlock()
	}
}
