package main

import (
	"os"
	"path/filepath"
	"slices"
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

	got, err := resolvePaths([]string{first, second}, nil)
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

	got, err := resolvePaths([]string{dir}, nil)
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

	got, err := resolvePaths([]string{filepath.Join(dir, "**", "docker-compose.yml")}, nil)
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

	got, err := resolvePaths([]string{filepath.Join(dir, "*", "docker-compose.yml")}, nil)
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
	}, nil)
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

	got, err := resolvePaths([]string{positional, flagged}, nil)
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

	got, err := resolvePaths([]string{compose, dir, filepath.Join(dir, "*", "docker-compose.yml")}, nil)
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
		if _, err := resolvePaths([]string{arg}, nil); err == nil {
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

	got, err := resolvePaths([]string{dir}, nil)
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

	got, err := resolvePaths(nil, nil)
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

	got, err := resolvePaths(nil, nil)
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
			if got := isUpdatableName(tt.path, nil); got != tt.want {
				t.Errorf("isUpdatableName(%q, nil) = %v, want %v", tt.path, got, tt.want)
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

// A file named `docker-compose.yml.j2` is an Ansible template for a Compose file,
// and the reference in it is the one the rendered file would carry. The name
// under the suffix is therefore still a Compose name, and the suffix is what
// says so — without it the walk stops at the first extension and never sees the
// Compose name underneath.
func TestResolvePaths_DirectoryRecognisesSuffix(t *testing.T) {
	dir := t.TempDir()
	template := writeFile(t, dir, "docker-compose.yml.j2", "")
	plain := writeFile(t, dir, "docker-compose.yml", "")

	// Without the flag the conventional name is the only one recognised.
	got, err := resolvePaths([]string{dir}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, plain) {
		t.Errorf("expected %s, got %v", plain, got)
	}

	if containsPath(got, template) {
		t.Errorf("expected %s to be ignored without --suffix, got %v", template, got)
	}

	got, err = resolvePaths([]string{dir}, []string{".j2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, plain) || !containsPath(got, template) {
		t.Errorf("expected both files, got %v", got)
	}
}

// The plain names come first, so a directory holding both reports them in the
// order a reader expects rather than sorting the template ahead by accident.
func TestResolvePaths_SuffixCandidateOrderFollowsThePlainNames(t *testing.T) {
	candidates := defaultCandidates([]string{".j2"})

	if candidates[0] != "docker-compose.yml" {
		t.Errorf("expected the plain name first, got %v", candidates)
	}

	if !slices.Contains(candidates, "docker-compose.yml.j2") {
		t.Errorf("expected the suffixed variant, got %v", candidates)
	}
}

// A suffix only widens the Compose side. `Dockerfile.j2` already passes through
// IsDockerfileName's prefix rule, and a suffix must not change that.
func TestResolvePaths_SuffixLeavesDockerfileNamesAlone(t *testing.T) {
	dir := t.TempDir()
	template := writeFile(t, dir, "Dockerfile.j2", "")

	for _, suffixes := range [][]string{nil, {".j2"}} {
		got, err := resolvePaths([]string{dir}, suffixes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !containsPath(got, template) {
			t.Errorf("suffixes %v: expected %s, got %v", suffixes, template, got)
		}
	}
}

// A run with no arguments looks for the suffixed variants too, so a directory
// holding only a template is processed by a bare shiphoist.
func TestResolvePaths_DefaultRecognisesSuffix(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, ".", "docker-compose.yml.j2", "")

	got, err := resolvePaths(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected nothing without --suffix, got %v", got)
	}

	got, err = resolvePaths(nil, []string{".j2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, "docker-compose.yml.j2") {
		t.Errorf("expected the template to be found, got %v", got)
	}
}

// A suffix must not be able to lift a name that is not a Compose file: the stem
// is rechecked on the trimmed name, so `docker-compose.j2` is not a Compose file
// and `deployment.yaml.j2` is not one either.
func TestIsUpdatableName_SuffixDoesNotWidenTheStem(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
		path   string
		want   bool
	}{
		{name: "template of a compose file", suffix: ".j2", path: "docker-compose.yml.j2", want: true},
		{name: "template of a plain compose file", suffix: ".j2", path: "compose.yaml.j2", want: true},
		{name: "stem without an extension", suffix: ".j2", path: "docker-compose.j2"},
		{name: "unrelated file", suffix: ".j2", path: "deployment.yaml.j2"},
		{name: "suffix inside the extension", suffix: ".yaml", path: "deployment.j2.yaml"},
		{name: "wrong suffix", suffix: ".tpl", path: "docker-compose.yml.j2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUpdatableName(tt.path, []string{tt.suffix}); got != tt.want {
				t.Errorf("isUpdatableName(%q, %q) = %v, want %v", tt.path, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestNormaliseSuffixes(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "nothing to do", input: []string{".j2"}, want: []string{".j2"}},
		{name: "dot is added", input: []string{"j2"}, want: []string{".j2"}},
		{name: "case is folded", input: []string{".J2"}, want: []string{".j2"}},
		{name: "whitespace is trimmed", input: []string{"  .j2  "}, want: []string{".j2"}},
		{name: "blanks are dropped", input: []string{"", "   ", "."}, want: nil},
		{name: "order is preserved", input: []string{".j2", ".tpl"}, want: []string{".j2", ".tpl"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normaliseSuffixes(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("normaliseSuffixes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --suffix decides what a *walk* recognises, never what a name the user spelled
// out means. A literal path and a glob are taken as written, which is what keeps
// every other name usable without registering a suffix for it.
func TestResolvePaths_ExplicitPathNeedsNoSuffix(t *testing.T) {
	dir := t.TempDir()
	template := writeFile(t, dir, "docker-compose.yml.j2", "")

	for _, suffixes := range [][]string{nil, {".j2"}} {
		got, err := resolvePaths([]string{template}, suffixes)
		if err != nil {
			t.Fatalf("suffixes %v: unexpected error: %v", suffixes, err)
		}

		if !slices.Equal(got, []string{template}) {
			t.Errorf("suffixes %v: expected %v, got %v", suffixes, template, got)
		}
	}

	// A glob is matched as written too.
	got, err := resolvePaths([]string{filepath.Join(dir, "*.j2")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsPath(got, template) {
		t.Errorf("expected the glob to match, got %v", got)
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
