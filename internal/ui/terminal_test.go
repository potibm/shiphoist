package ui

import (
	"os"
	"testing"
)

func TestCanPrompt(t *testing.T) {
	tests := []struct {
		name string
		term string
		want bool
	}{
		{name: "dumb terminal is answerable", term: "dumb", want: true},
		{name: "a real terminal name is not enough", term: "xterm-256color", want: false},
		{name: "unset is not answerable", term: "", want: false},
	}

	// A regular file is never a terminal, so the answer comes from TERM alone.
	notATerminal := os.NewFile(0, "not-a-terminal")

	t.Cleanup(func() { notATerminal.Close() })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(dumbTermEnv, tt.term)

			if got := CanPrompt(notATerminal); got != tt.want {
				t.Errorf("CanPrompt with TERM=%q = %v, want %v", tt.term, got, tt.want)
			}
		})
	}
}
