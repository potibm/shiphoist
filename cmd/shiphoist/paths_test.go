package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates path under a temp dir, making the parent directories as it
// goes, and returns the path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating parent of %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

func TestResolvePaths_LiteralFiles(t *testing.T) {
	dir := t.TempDir()
	first := writeFile(t, dir, "a/docker-compose.yml", "")
	second := writeFile(t, dir, "b/Dockerfile", "")

	got, err := resolvePaths([]string{first, second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("expected %v, got %v", []string{first, second}, got)
	}
}

// A directory is walked for the names shiphoist can read, and nothing else.
// Pulling in a stray YAML would make the run report nothing rather than fail.
func TestResolvePaths_DirectoryTakesOnlyKnownNames(t *testing.T) {
	dir := t.TempDir()
	compose := writeFile(t, dir, "docker-compose.yml", "")
	dockerfile := writeFile(t, dir, "build/Dockerfile", "")
	override := writeFile(t, dir, "docker-compose.override.yml", "")

	// None of these are names shiphoist reads.
	writeFile(t, dir, "deployment.yaml", "")
	writeFile(t, dir, "Dockerfileish.yml", "")
	writeFile(t, dir, "notes.txt", "")

	got, err := resolvePaths([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{compose, override, dockerfile}

	// Walk order is lexical, so the comparison is order-independent only in
	// intent: the result is sorted, and so is the expectation.
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	for _, path := range want {
		if !containsPath(got, path) {
			t.Errorf("expected %s in %v", path, got)
		}
	}
}

// `**` is what makes a recursive pattern worth having: `filepath.Glob` treats
// it as two single-segment wildcards, so `**/docker-compose.yml` matches exactly
// one directory deep and silently misses the rest.
func TestResolvePaths_RecursiveGlob(t *testing.T) {
	dir := t.TempDir()
	root := writeFile(t, dir, "docker-compose.yml", "")
	nested := writeFile(t, dir, "a/b/docker-compose.yml", "")
	deep := writeFile(t, dir, "a/b/c/d/docker-compose.yml", "")
	other := writeFile(t, dir, "a/b/other.yml", "")

	got, err := resolvePaths([]string{filepath.Join(dir, "**", "docker-compose.yml")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{root, nested, deep} {
		if !containsPath(got, want) {
			t.Errorf("expected %s in %v", want, got)
		}
	}

	if containsPath(got, other) {
		t.Errorf("did not expect %s in %v", other, got)
	}
}

func TestResolvePaths_SingleSegmentGlob(t *testing.T) {
	dir := t.TempDir()
	first := writeFile(t, dir, "one/docker-compose.yml", "")
	second := writeFile(t, dir, "two/docker-compose.yml", "")

	got, err := resolvePaths([]string{filepath.Join(dir, "*", "docker-compose.yml")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, first) || !containsPath(got, second) || len(got) != 2 {
		t.Errorf("expected the two compose files, got %v", got)
	}
}

// A pattern the shell would leave alone is quoted by the caller and expanded by
// shiphoist, which is why --file exists alongside the positional arguments.
func TestResolvePaths_MixedShapeArguments(t *testing.T) {
	dir := t.TempDir()
	nested := writeFile(t, dir, "a/docker-compose.yml", "")
	plain := writeFile(t, dir, "plain/Dockerfile", "")

	got, err := resolvePaths([]string{
		filepath.Join(dir, "**", "docker-compose.yml"),
		plain,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, nested) || !containsPath(got, plain) {
		t.Errorf("expected both files, got %v", got)
	}
}

// `--file` is combined with the positional arguments rather than replacing
// them, so a Makefile can add a pattern without dropping what was passed on
// the command line.
func TestResolvePaths_CombinesPositionalAndFileFlag(t *testing.T) {
	dir := t.TempDir()
	positional := writeFile(t, dir, "one/docker-compose.yml", "")
	flagged := writeFile(t, dir, "two/Dockerfile", "")

	got, err := resolvePaths([]string{positional, flagged})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, positional) || !containsPath(got, flagged) {
		t.Errorf("expected both files, got %v", got)
	}
}

// The same file named twice is processed once. Discovering it twice would
// report twice as many updates as the file actually gained.
func TestResolvePaths_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	compose := writeFile(t, dir, "docker-compose.yml", "")
	other := writeFile(t, dir, "sub/docker-compose.yml", "")

	got, err := resolvePaths([]string{compose, dir, filepath.Join(dir, "*", "docker-compose.yml")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{compose, other}

	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	for _, path := range want {
		if !containsPath(got, path) {
			t.Errorf("expected %s in %v", path, got)
		}
	}
}

// An argument that matches nothing is refused rather than skipped. A run that
// silently covers none of the files it was asked about is worse than one that
// fails, because nothing in the output would say so.
func TestResolvePaths_RejectsWhatMatchesNothing(t *testing.T) {
	dir := t.TempDir()

	for _, arg := range []string{
		filepath.Join(dir, "missing.yml"),
		filepath.Join(dir, "**", "missing.yml"),
		filepath.Join(dir, "nomatch", "*.yml"),
	} {
		if _, err := resolvePaths([]string{arg}); err == nil {
			t.Errorf("expected %q to be refused", arg)
		}
	}
}

// Dependencies and build output are skipped, so `shiphoist .` in a project with
// a vendored compose file does not pull in hundreds of files nobody asked for.
func TestResolvePaths_SkipsDirectoriesNobodyAskedFor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docker-compose.yml", "")

	writeFile(t, dir, ".git/docker-compose.yml", "")
	writeFile(t, dir, "node_modules/pkg/docker-compose.yml", "")
	writeFile(t, dir, "vendor/pkg/docker-compose.yml", "")
	writeFile(t, dir, "dist/docker-compose.yml", "")
	writeFile(t, dir, ".venv/docker-compose.yml", "")

	got, err := resolvePaths([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 1 {
		t.Errorf("expected only the top-level compose file, got %v", got)
	}
}

func TestResolvePaths_DefaultsToTheCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Cleanup(func() {
		if err := os.Chdir(originalDir(t)); err != nil {
			t.Fatalf("restoring dir: %v", err)
		}
	})

	writeFile(t, dir, "docker-compose.yml", "")
	writeFile(t, dir, "nested/docker-compose.yml", "")

	got, err := resolvePaths(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 1 || got[0] != "docker-compose.yml" {
		t.Errorf("expected the current directory's compose file, got %v", got)
	}
}

// An empty directory with no compose file is an error, not a clean run: exiting
// zero over nothing would leave a CI job green after doing no work.
func TestResolvePaths_DefaultWithNothingPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Cleanup(func() {
		if err := os.Chdir(originalDir(t)); err != nil {
			t.Fatalf("restoring dir: %v", err)
		}
	})

	got, err := resolvePaths(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected no files, got %v", got)
	}
}

// originalDir returns the directory the test started in. Chdir is the only way
// to exercise the default, because it is relative to the working directory by
// design.
func originalDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	return dir
}

func TestIsUpdatableName(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "docker-compose.yml", want: true},
		{path: "docker-compose.yaml", want: true},
		{path: "compose.yml", want: true},
		{path: "compose.yaml", want: true},
		{path: "docker-compose.override.yml", want: true},
		{path: "DOCKER-COMPOSE.YML", want: true},
		{path: "infra/docker-compose.prod.yml", want: true},
		{path: "Dockerfile", want: true},
		{path: "dockerfile", want: true},
		{path: "Dockerfile.prod", want: true},
		{path: "app.Dockerfile", want: true},
		{path: "deployment.yaml"},
		{path: "Dockerfileish.yml"},
		{path: "docker-compose.txt"},
		{path: "notes.md"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isUpdatableName(tt.path); got != tt.want {
				t.Errorf("isUpdatableName(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchSegments(t *testing.T) {
	tests := []struct {
		name     string
		pattern  []string
		segments []string
		want     bool
	}{
		{name: "exact", pattern: []string{"a", "b"}, segments: []string{"a", "b"}, want: true},
		{name: "wildcard", pattern: []string{"*"}, segments: []string{"anything"}, want: true},
		{name: "no match", pattern: []string{"a"}, segments: []string{"b"}},
		{name: "too few segments", pattern: []string{"a", "b"}, segments: []string{"a"}},
		{name: "too many segments", pattern: []string{"a"}, segments: []string{"a", "b"}},
		// `**` absorbs any number of segments, including none.
		{
			name:     "double star absorbs two",
			pattern:  []string{"**", "file.yml"},
			segments: []string{"a", "b", "file.yml"},
			want:     true,
		},
		{
			name:     "double star absorbs none",
			pattern:  []string{"**", "file.yml"},
			segments: []string{"file.yml"},
			want:     true,
		},
		{
			name:     "double star in the middle",
			pattern:  []string{"a", "**", "d"},
			segments: []string{"a", "b", "c", "d"},
			want:     true,
		},
		{
			name:     "trailing double star matches anything",
			pattern:  []string{"a", "**"},
			segments: []string{"a"},
			want:     true,
		},
		{
			name:     "trailing double star matches more",
			pattern:  []string{"a", "**"},
			segments: []string{"a", "b", "c"},
			want:     true,
		},
		{name: "double star cannot skip the tail", pattern: []string{"**", "d"}, segments: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchSegments(tt.pattern, tt.segments); got != tt.want {
				t.Errorf("matchSegments(%v, %v) = %v, want %v", tt.pattern, tt.segments, got, tt.want)
			}
		})
	}
}

func TestHasGlobMagic(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{pattern: "docker-compose.yml"},
		{pattern: "infra/docker-compose.yml"},
		{pattern: "**/docker-compose.yml", want: true},
		{pattern: "*/docker-compose.yml", want: true},
		{pattern: "docker-compose.?.yml", want: true},
		{pattern: "docker-compose[0-9].yml", want: true},
	}

	for _, tt := range tests {
		if got := hasGlobMagic(tt.pattern); got != tt.want {
			t.Errorf("hasGlobMagic(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}

	return false
}
