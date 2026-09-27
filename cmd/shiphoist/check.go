package main

import (
	"context"
	"fmt"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/discovery"
)

// runCheck resolves the update candidates for a single image reference without
// touching any file. It returns an error rather than exiting.
func runCheck(d deps, opts options, imageRef string) error {
	activeFetcher, err := d.NewFetcher(opts.ForceRefresh)
	if err != nil {
		return fmt.Errorf("failed to initialize fetcher: %w", err)
	}

	imageName, oldTag, oldDigest := discovery.ParseImageReference(imageRef)

	fmt.Fprintf(d.Out, "🔍 Checking %s...\n\n", imageRef)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updated, err := activeFetcher.FetchUpdate(ctx, core.ImageUpdate{
		ImageName: imageName,
		OldTag:    oldTag,
		OldDigest: oldDigest,
	})
	if err != nil {
		return fmt.Errorf("failed to check %s: %w", imageRef, err)
	}

	printCheckResult(d.Out, updated)

	return nil
}
