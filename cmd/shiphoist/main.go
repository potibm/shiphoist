package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/discovery"
	"github.com/potibm/shiphoist/internal/engine"
	"github.com/potibm/shiphoist/internal/patcher"
	"github.com/potibm/shiphoist/internal/registry"
	"github.com/potibm/shiphoist/internal/tui"
	"github.com/potibm/shiphoist/internal/ui"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

const cacheTTLMinutes = 15

func buildFetcher(forceRefresh bool) (core.RegistryFetcher, error) {
	cacheTTL := cacheTTLMinutes * time.Minute

	var client registry.RegistryClient = registry.NewRemoteClient()

	cachedClient, err := registry.NewCachedClient(client, cacheTTL, forceRefresh)
	if err != nil {
		fmt.Printf("⚠️  Failed to init cache, running without: %v\n", err)
	} else {
		client = cachedClient
	}

	return registry.NewDefaultFetcher(client), nil
}

func main() {
	var (
		forceRefresh bool
		verbose      bool
	)

	rootCmd := &cobra.Command{
		Use:   "shiphoist [path-to-docker-compose.yml]",
		Short: "Interactively update and SHA256-pin Docker images in Compose files",
		Args:  cobra.ExactArgs(1),
		Run:   runRootCommand(&forceRefresh, &verbose),
	}

	rootCmd.PersistentFlags().BoolVarP(&forceRefresh, "force", "f", false, "Force refresh by bypassing the local cache")
	rootCmd.PersistentFlags().
		BoolVarP(&verbose, "verbose", "v", false, "Report one line per image instead of a single progress bar")
	rootCmd.Version = fmt.Sprintf("%s (Commit: %s, Date: %s)", Version, Commit, Date)

	checkCmd := &cobra.Command{
		Use:   "check [image-reference]",
		Short: "Check a single Docker image for available updates",
		Long: `Check a single Docker image reference for available updates.
Example: shiphoist check ghcr.io/potibm/kasseapparat:2.18.0`,
		Args: cobra.ExactArgs(1),
		Run:  runCheckCommand(&forceRefresh),
	}

	rootCmd.AddCommand(checkCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runRootCommand(forceRefresh, verbose *bool) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		activeFetcher, err := buildFetcher(*forceRefresh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error initializing fetcher: %v\n", err)
			os.Exit(1)
		}

		pipeline := &engine.Pipeline{
			Discoverer: &discovery.ComposeDiscoverer{},
			Fetcher:    activeFetcher,
			Patcher:    &patcher.FilePatcher{},
			Prompter:   tui.NewHuhPrompter(),
			Verbose:    *verbose,
		}

		if *forceRefresh {
			fmt.Println("🔄 Force refresh activated - bypassing cache...")
		}

		fmt.Printf("🚢 Hoisting sails for %s...\n", filePath)

		updates, err := pipeline.ProcessFile(ctx, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error processing file: %v\n", err)
			os.Exit(1)
		}

		if len(updates) == 0 {
			fmt.Println("✅ Everything is up to date! No changes needed.")

			return
		}

		fmt.Printf("✅ Successfully applied %d updates:\n\n", len(updates))

		for _, u := range updates {
			if u.UpdateType == core.UpdateTypeNone {
				fmt.Printf("  📌 %s: pinned to new digest\n", u.Label())
			} else {
				fmt.Printf("  🚀 %s: %s -> %s (%s)\n", u.Label(), u.OldTag, u.NewTag, u.UpdateType)
			}
		}
	}
}

func runCheckCommand(forceRefresh *bool) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		imageRef := args[0]

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		activeFetcher, err := buildFetcher(*forceRefresh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error initializing fetcher: %v\n", err)
			os.Exit(1)
		}

		imageName, oldTag, oldDigest := discovery.ParseImageReference(imageRef)

		fmt.Printf("🔍 Checking %s...\n\n", imageRef)

		current := core.ImageUpdate{
			ImageName: imageName,
			OldTag:    oldTag,
			OldDigest: oldDigest,
		}

		updated, err := activeFetcher.FetchUpdate(ctx, current)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error checking image: %v\n", err)
			os.Exit(1)
		}

		printCheckResult(updated)
	}
}

func printCheckResult(updated core.ImageUpdate) {
	fmt.Printf("📦 Image:  %s\n", updated.Label())

	printCurrentTagInfo(updated)
	printLatestTagInfo(updated)

	if updated.MajorTag != "" {
		fmt.Printf("⚠️  Major available: %s (not preselected)\n", updated.MajorTag)
	}
}

func printCurrentTagInfo(updated core.ImageUpdate) {
	if updated.OldTagMissing {
		fmt.Printf("⚠️  Tag %s not found in registry (deleted?)\n", updated.OldTag)

		return
	}

	fmt.Printf("🏷  Current: %s", updated.OldTag)

	if updated.CurrentDigest != "" {
		fmt.Printf(" (digest: %s)", ui.ShortDigest(updated.CurrentDigest))
	}

	if updated.OldDigest != "" && updated.OldDigest != updated.CurrentDigest {
		fmt.Printf(" [pinned: %s]", ui.ShortDigest(updated.OldDigest))
	}

	fmt.Println()
}

func printLatestTagInfo(updated core.ImageUpdate) {
	switch {
	case updated.Selected && updated.NewTag != updated.OldTag:
		fmt.Printf("🚀 Latest:  %s (%s)", updated.NewTag, updated.UpdateType)

		if updated.NewDigest != "" {
			fmt.Printf(" (digest: %s)", ui.ShortDigest(updated.NewDigest))
		}

		fmt.Println()
	case updated.Selected && updated.NewTag == updated.OldTag:
		fmt.Printf("📌 Pin to digest: %s\n", ui.ShortDigest(updated.NewDigest))
	case updated.UpdateType == core.UpdateTypeNone && updated.NewTag == updated.OldTag:
		printUpToDateStatus(updated)
	default:
		fmt.Printf("📌 Latest:  %s (pinned to digest)\n", updated.NewTag)
	}
}

func printUpToDateStatus(updated core.ImageUpdate) {
	switch {
	case updated.OldTagMissing && updated.NoCompatibleTags:
		fmt.Println("ℹ️  No SemVer-compatible tags in repo (restructured/renamed?)")
	case updated.OldTagMissing:
		fmt.Println("ℹ️  No newer tags available")
	default:
		fmt.Println("✅ Up to date!")
	}
}
