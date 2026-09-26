// Package ui renders terminal output: column layout, progress reporting and
// digest formatting. It is kept free of pipeline logic so that every function
// here can be unit tested without a terminal.
package ui

import (
	"os"

	"github.com/charmbracelet/x/term"
)

const (
	// FallbackWidth is used when the output is not an interactive terminal
	// and its real width is therefore unknowable.
	FallbackWidth = 100

	// minUsableWidth guards against absurdly small reported sizes, which
	// some CI environments report for non-terminal file descriptors.
	minUsableWidth = 20
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}

	return term.IsTerminal(f.Fd())
}

// TerminalWidth returns the width of f in columns. It falls back to
// FallbackWidth when f is not a terminal or its size cannot be determined,
// which is the case for pipes, files and CI logs.
func TerminalWidth(f *os.File) int {
	if !IsTerminal(f) {
		return FallbackWidth
	}

	width, _, err := term.GetSize(f.Fd())
	if err != nil || width < minUsableWidth {
		return FallbackWidth
	}

	return width
}
