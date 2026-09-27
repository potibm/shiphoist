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

	// dumbTermEnv and dumbTerm are the signal that the terminal cannot render a
	// full-screen widget, which is also exactly when huh switches to its
	// accessible renderer.
	dumbTermEnv = "TERM"
	dumbTerm    = "dumb"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}

	return term.IsTerminal(f.Fd())
}

// CanPrompt reports whether an interactive form can be shown and answered on f.
//
// A real terminal is the normal case, but huh falls back to a numbered,
// line-driven list when TERM is "dumb" (form.go switches on exactly this value),
// which is what makes a piped or scripted run answerable. Asking a stricter
// question than the renderer does would refuse a prompt that works.
func CanPrompt(f *os.File) bool {
	if IsTerminal(f) {
		return true
	}

	return os.Getenv(dumbTermEnv) == dumbTerm
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
