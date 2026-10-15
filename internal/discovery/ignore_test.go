package discovery

import "testing"

func TestHasIgnoreDirective(t *testing.T) {
	const (
		imageAt = "    image: nginx:1.25.0"
		column  = 12
	)

	tests := []struct {
		name   string
		line   string
		column int
		want   bool
	}{
		{
			name:   "directive on the same line",
			line:   imageAt + " # shiphoist-ignore",
			column: column,
			want:   true,
		},
		{
			name:   "directive with a reason",
			line:   imageAt + " # shiphoist-ignore pinned by policy",
			column: column,
			want:   true,
		},
		{
			name:   "directive is case-insensitive",
			line:   imageAt + " # SHIPHOIST-IGNORE",
			column: column,
			want:   true,
		},
		{
			name:   "no comment at all",
			line:   imageAt,
			column: column,
			want:   false,
		},
		{
			name:   "an unrelated comment",
			line:   imageAt + " # keep in sync with staging",
			column: column,
			want:   false,
		},
		{
			// A comment belonging to the preceding key must not be read as a
			// directive on this reference, which is what the column is for.
			name:   "comment before the reference is ignored",
			line:   "    # shiphoist-ignore" + "\n" + imageAt,
			column: column,
			want:   false,
		},
		{
			name:   "out-of-range column falls back to the whole line",
			line:   imageAt + " # shiphoist-ignore",
			column: 0,
			want:   true,
		},
		{
			name:   "column past the end still finds the directive",
			line:   imageAt + " # shiphoist-ignore",
			column: 999,
			want:   true,
		},
		{
			name:   "digest-pinned reference",
			line:   "    image: nginx:1.25.0@sha256:abc # shiphoist-ignore",
			column: column,
			want:   true,
		},
		{
			// A mention is not a directive. Acting on it would skip an image
			// the user still wants updated.
			name:   "a mention is not a directive",
			line:   imageAt + " # TODO shiphoist-ignore this later",
			column: column,
			want:   false,
		},
		{
			name:   "a longer word is not a directive",
			line:   imageAt + " # shiphoist-ignored",
			column: column,
			want:   false,
		},
		{
			name:   "no space after the hash",
			line:   imageAt + " #shiphoist-ignore",
			column: column,
			want:   true,
		},
		{
			name:   "leading whitespace is tolerated",
			line:   imageAt + " #   shiphoist-ignore",
			column: column,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasIgnoreDirective(tt.line, tt.column); got != tt.want {
				t.Errorf("hasIgnoreDirective = %v, want %v", got, tt.want)
			}
		})
	}
}
