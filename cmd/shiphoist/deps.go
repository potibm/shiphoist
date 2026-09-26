package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/registry"
	"github.com/potibm/shiphoist/internal/tui"
)

const cacheTTLMinutes = 15

// deps is everything the commands need from the outside world.
//
// Commands take it as an argument rather than reaching for os.Stdout and the
// network themselves, so a test can hand them buffers and stubs. Every field has
// a real implementation in newDeps, and tests override only what they care
// about.
type deps struct {
	// Out and ErrOut receive user-facing output. Out carries results, ErrOut
	// carries warnings, so a future machine-readable mode can split them.
	Out    io.Writer
	ErrOut io.Writer

	// NewFetcher builds the registry fetcher for the given force-refresh flag.
	NewFetcher func(forceRefresh bool) (core.RegistryFetcher, error)

	// NewPrompter builds the interactive selector. Returning nil applies every
	// fetched update without asking, which is the non-interactive path.
	NewPrompter func() core.Prompter
}

// newDeps returns the real environment.
func newDeps() deps {
	return deps{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		NewFetcher: func(forceRefresh bool) (core.RegistryFetcher, error) {
			return buildFetcher(os.Stderr, forceRefresh)
		},
		NewPrompter: func() core.Prompter { return tui.NewHuhPrompter() },
	}
}

// buildFetcher decorates the remote registry client with the on-disk cache.
//
// A cache that cannot be initialised is a warning rather than a failure: the run
// still works, it just costs a registry round-trip per image.
func buildFetcher(w io.Writer, forceRefresh bool) (core.RegistryFetcher, error) {
	cacheTTL := cacheTTLMinutes * time.Minute

	var client registry.RegistryClient = registry.NewRemoteClient()

	cachedClient, err := registry.NewCachedClient(client, cacheTTL, forceRefresh)
	if err != nil {
		fmt.Fprintf(w, "⚠️  Failed to init cache, running without: %v\n", err)
	} else {
		client = cachedClient
	}

	return registry.NewDefaultFetcher(client), nil
}
