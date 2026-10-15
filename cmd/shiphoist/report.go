package main

import (
	"fmt"
	"io"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

// printCheckResult reports the resolved state of a single image: where it is
// now, where it could go, and the major candidate that is deliberately not
// offered as a default.
func printCheckResult(w io.Writer, updated core.ImageUpdate) {
	fmt.Fprintf(w, "📦 Image:  %s\n", updated.Label())

	printCurrentTagInfo(w, updated)
	printLatestTagInfo(w, updated)

	if updated.MajorTag != "" {
		fmt.Fprintf(w, "⚠️  Major available: %s (not preselected)\n", updated.MajorTag)
	}
}

// printCurrentTagInfo states the resolved current tag, including a pinned digest
// that no longer matches the registry.
func printCurrentTagInfo(w io.Writer, updated core.ImageUpdate) {
	if updated.OldTagMissing {
		fmt.Fprintf(w, "⚠️  Tag %s not found in registry (deleted?)\n", updated.OldTag)

		return
	}

	fmt.Fprintf(w, "🏷  Current: %s", updated.OldTag)

	if updated.CurrentDigest != "" {
		fmt.Fprintf(w, " (digest: %s)", ui.ShortDigest(updated.CurrentDigest))
	}

	if updated.OldDigest != "" && updated.OldDigest != updated.CurrentDigest {
		fmt.Fprintf(w, " [pinned: %s]", ui.ShortDigest(updated.OldDigest))
	}

	fmt.Fprintln(w)
}

// printLatestTagInfo states the resolved target, which is one of three things:
// a version jump, a digest pin at the same tag, or nothing to do.
func printLatestTagInfo(w io.Writer, updated core.ImageUpdate) {
	switch {
	case updated.Selected && updated.NewTag != updated.OldTag:
		fmt.Fprintf(w, "🚀 Latest:  %s (%s)", updated.NewTag, updated.UpdateType)

		if updated.NewDigest != "" {
			fmt.Fprintf(w, " (digest: %s)", ui.ShortDigest(updated.NewDigest))
		}

		fmt.Fprintln(w)
	case updated.Selected && updated.NewTag == updated.OldTag:
		fmt.Fprintf(w, "📌 Pin to digest: %s\n", ui.ShortDigest(updated.NewDigest))
	case updated.UpdateType == core.UpdateTypeNone && updated.NewTag == updated.OldTag:
		printUpToDateStatus(w, updated)
	default:
		fmt.Fprintf(w, "📌 Latest:  %s (pinned to digest)\n", updated.NewTag)
	}
}

// printUpToDateStatus explains *why* nothing was offered, which a bare
// "up to date" would hide.
func printUpToDateStatus(w io.Writer, updated core.ImageUpdate) {
	switch {
	case updated.OldTagMissing && updated.NoCompatibleTags:
		fmt.Fprintln(w, "ℹ️  No SemVer-compatible tags in repo (restructured/renamed?)")
	case updated.OldTagMissing:
		fmt.Fprintln(w, "ℹ️  No newer tags available")
	default:
		fmt.Fprintln(w, "✅ Up to date!")
	}
}
