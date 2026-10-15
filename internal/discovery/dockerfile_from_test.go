package discovery

import "testing"

func TestParseFrom(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		image string
		alias string
		ok    bool
	}{
		{
			name:  "plain reference",
			line:  "FROM nginx:1.25.0",
			image: "nginx:1.25.0",
			ok:    true,
		},
		{
			name:  "implicit latest",
			line:  "FROM nginx",
			image: "nginx",
			ok:    true,
		},
		{
			name:  "registry with a port",
			line:  "FROM localhost:5000/team/app:1.0",
			image: "localhost:5000/team/app:1.0",
			ok:    true,
		},
		{
			name:  "digest pinned",
			line:  "FROM nginx:1.25.0@sha256:abc123",
			image: "nginx:1.25.0@sha256:abc123",
			ok:    true,
		},
		{
			name:  "with an alias",
			line:  "FROM golang:1.22 AS builder",
			image: "golang:1.22",
			alias: "builder",
			ok:    true,
		},
		{
			// The keyword is case-insensitive, and so is the alias keyword.
			name:  "lowercase keywords and alias",
			line:  "from golang:1.22 as builder",
			image: "golang:1.22",
			alias: "builder",
			ok:    true,
		},
		{
			name:  "mixed case alias keyword",
			line:  "FROM golang:1.22 As builder",
			image: "golang:1.22",
			alias: "builder",
			ok:    true,
		},
		{
			name:  "platform flag",
			line:  "FROM --platform=linux/amd64 golang:1.22 AS builder",
			image: "golang:1.22",
			alias: "builder",
			ok:    true,
		},
		{
			name:  "several flags",
			line:  "FROM --platform=linux/arm64 --foo=bar nginx:1.25.0",
			image: "nginx:1.25.0",
			ok:    true,
		},
		{
			name:  "leading and trailing space",
			line:  "   FROM    nginx:1.25.0   ",
			image: "nginx:1.25.0",
			ok:    true,
		},
		{
			name:  "trailing comment",
			line:  "FROM nginx:1.25.0 # bump me",
			image: "nginx:1.25.0",
			ok:    true,
		},
		{
			name:  "comment between the reference and the alias",
			line:  "FROM golang:1.22 # need this for the build\n",
			image: "golang:1.22",
			ok:    true,
		},
		{
			name:  "variable reference",
			line:  "FROM $BASE",
			image: "$BASE",
			ok:    true,
		},
		{
			name:  "braced variable reference",
			line:  "FROM ${BASE}",
			image: "${BASE}",
			ok:    true,
		},
		{
			// Only the tag is a variable, which a prefix check would miss.
			name:  "variable tag",
			line:  "FROM node:$NODE AS builder",
			image: "node:$NODE",
			alias: "builder",
			ok:    true,
		},
		{
			name:  "scratch",
			line:  "FROM scratch",
			image: "scratch",
			ok:    true,
		},
		{
			// Not FROM lines.
			name: "another instruction",
			line: "RUN echo FROM nginx:1.25.0",
		},
		{
			name: "copy from a stage is not a base image",
			line: "COPY --from=builder /app /app",
		},
		{
			name: "arg instruction",
			line: "ARG BASE=nginx:1.25.0",
		},
		{
			name: "comment",
			line: "# FROM nginx:1.25.0",
		},
		{
			name: "blank line",
			line: "   ",
		},
		{
			name: "from with no image",
			line: "FROM",
		},
		{
			name: "from with only a flag",
			line: "FROM --platform=linux/amd64",
		},
		{
			name: "dangling AS keyword",
			line: "FROM nginx:1.25.0 AS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseFrom(tt.line)

			if ok != tt.ok {
				t.Fatalf("parseFrom(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}

			if !ok {
				return
			}

			if got.Image != tt.image {
				t.Errorf("image = %q, want %q", got.Image, tt.image)
			}

			if got.Alias != tt.alias {
				t.Errorf("alias = %q, want %q", got.Alias, tt.alias)
			}
		})
	}
}

func TestFieldsWithoutComment(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "no comment",
			line: "FROM nginx:1.25.0",
			want: []string{"FROM", "nginx:1.25.0"},
		},
		{
			name: "trailing comment",
			line: "FROM nginx:1.25.0 # bump",
			want: []string{"FROM", "nginx:1.25.0"},
		},
		{
			name: "leading comment",
			line: "# FROM nginx:1.25.0",
			want: nil,
		},
		{
			// A hash that is not preceded by whitespace belongs to the token, so
			// it is not a comment.
			name: "hash inside a token",
			line: "FROM repo/image#fragment",
			want: []string{"FROM", "repo/image#fragment"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fieldsWithoutComment(tt.line)

			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("position %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsVariableReference(t *testing.T) {
	tests := []struct {
		image string
		want  bool
	}{
		{image: "$BASE", want: true},
		{image: "${BASE}", want: true},
		{image: "$BASE_IMAGE", want: true},
		{image: "node:$NODE", want: true},
		{image: "node:${NODE}", want: true},
		{image: "nginx:1.25.0", want: false},
		{image: "ghcr.io/acme/app:1.0", want: false},
		{image: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			if got := isVariableReference(tt.image); got != tt.want {
				t.Errorf("isVariableReference(%q) = %v, want %v", tt.image, got, tt.want)
			}
		})
	}
}

func TestIsReservedBase(t *testing.T) {
	for _, image := range []string{"scratch", "SCRATCH", "Scratch"} {
		if !isReservedBase(image) {
			t.Errorf("expected %q to be reserved", image)
		}
	}

	if isReservedBase("scratchpad") {
		t.Error("expected scratchpad not to be reserved")
	}
}
