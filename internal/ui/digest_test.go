package ui

import (
	"strings"
	"testing"
)

func TestShortDigest(t *testing.T) {
	const long = "sha256:1234567890abcdef"

	tests := []struct {
		name     string
		digest   string
		expected string
	}{
		{name: "empty", digest: "", expected: ""},
		{name: "short value is untouched", digest: "sha256:abc", expected: "sha256:abc"},
		{
			name:     "exactly the limit",
			digest:   strings.Repeat("a", shortDigestLength),
			expected: strings.Repeat("a", shortDigestLength),
		},
		{name: "long value is trimmed", digest: long, expected: "sha256:123456789..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShortDigest(tc.digest); got != tc.expected {
				t.Errorf("ShortDigest(%q) = %q, want %q", tc.digest, got, tc.expected)
			}
		})
	}
}

func TestIsTerminal_NilFile(t *testing.T) {
	if IsTerminal(nil) {
		t.Error("expected nil not to be a terminal")
	}
}

func TestTerminalWidth_FallsBackForNonTerminal(t *testing.T) {
	// A pipe is never a terminal, so the fallback must be returned.
	if got := TerminalWidth(nil); got != FallbackWidth {
		t.Errorf("TerminalWidth(nil) = %d, want %d", got, FallbackWidth)
	}
}
