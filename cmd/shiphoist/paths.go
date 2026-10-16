package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/potibm/shiphoist/internal/discovery"
)

// Compose file stems and extensions. A Compose file is `compose.yml`, or a stem
// with a variant between it and the extension, as in `docker-compose.override.yml`.
//
// Both halves have to be checked. Testing the stem alone would pick up a shell
// script called `docker-compose.sh`, and testing the extension alone would pick
// up any YAML in the tree, neither of which shiphoist can read.
var (
	composeStems     = []string{"docker-compose", "compose"}
	composeExtension = []string{".yml", ".yaml"}
)

// defaultFileNames is what a run with no arguments looks for in the current
// directory, in the order it is reported.
var defaultFileNames = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// skippedDirs are the directories a walk does not descend into. They hold
// dependencies and build output, where an image reference is somebody else's
// to update, and descending into them can produce thousands of matches.
var skippedDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
	"dist":         true,
	"target":       true,
}

// doubleStar is the recursive wildcard `filepath.Glob` does not implement.
const doubleStar = "**"

// resolvePaths expands the command-line arguments into the files to process.
//
// Every argument may be a literal path, a glob pattern or a directory, and the
// three can be mixed freely. An argument that matches nothing is an error: a
// run that silently covers none of the files it was asked about is worse than
// one that refuses, because nothing in the output would say so.
//
// The result is deduplicated and ordered by first appearance, so
// `shiphoist . docker-compose.yml` processes `docker-compose.yml` once, at the
// position the directory gave it.
func resolvePaths(args, suffixes []string) ([]string, error) {
	if len(args) == 0 {
		return resolveDefaultPaths(suffixes), nil
	}

	expanded := make([]string, 0, len(args))

	for _, arg := range args {
		matches, err := expandArg(arg, suffixes)
		if err != nil {
			return nil, err
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf("no files match %q", arg)
		}

		expanded = append(expanded, matches...)
	}

	return dedupe(expanded), nil
}

// resolveDefaultPaths returns the files a run with no arguments looks for in
// the current directory.
//
// It is deliberately not recursive: `shiphoist` with no arguments should do the
// obvious thing in the obvious place, and a walk that silently pulls in a
// hundred files from subdirectories is not that.
//
// A run that names no file finds every candidate that exists, so a directory
// holding both `docker-compose.yml` and its `.j2` template is processed as
// both. That is the point of passing --suffix, and it is why the flag is what
// turns the variants on.
func resolveDefaultPaths(suffixes []string) []string {
	var found []string

	for _, name := range defaultCandidates(suffixes) {
		if _, err := os.Stat(name); err == nil {
			found = append(found, name)
		}
	}

	return found
}

// defaultCandidates lists the names a run with no arguments looks for.
//
// Each --suffix value contributes a candidate next to every conventional name,
// so `--suffix .j2` makes a bare shiphoist find `docker-compose.yml.j2` as well
// as `docker-compose.yml`. The plain names come first, so a directory holding
// both reports them in the order a reader expects.
func defaultCandidates(suffixes []string) []string {
	candidates := slices.Clone(defaultFileNames)

	for _, suffix := range normaliseSuffixes(suffixes) {
		for _, name := range defaultFileNames {
			candidates = append(candidates, name+suffix)
		}
	}

	return candidates
}

// expandArg resolves one argument against the filesystem.
//
// suffixes are only used for a directory: a literal path or a glob is taken as
// written, so `--suffix` decides what a walk recognises and never what a name
// the user spelled out means.
func expandArg(arg string, suffixes []string) ([]string, error) {
	if hasGlobMagic(arg) {
		return expandGlob(arg)
	}

	info, err := os.Stat(arg)
	if err != nil {
		// A literal path that does not exist is reported as a match failure
		// rather than a stat error, so the message is the same whichever shape
		// of argument was used.
		return nil, nil
	}

	if !info.IsDir() {
		return []string{arg}, nil
	}

	return expandDir(arg, suffixes)
}

// hasGlobMagic reports whether a pattern contains any wildcard.
func hasGlobMagic(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// expandGlob matches a pattern against the filesystem, including `**`.
//
// `**` matches any number of path segments, so `**/docker-compose.yml` finds a
// compose file at any depth. `filepath.Glob` implements none of that, so the
// pattern is expanded one segment at a time from the longest prefix that has no
// wildcard in it, and a walk collects the matches from there.
func expandGlob(pattern string) ([]string, error) {
	segments := strings.Split(filepath.ToSlash(pattern), "/")

	root := globRoot(segments)
	rest := segments[len(strings.Split(root, "/")):]

	if root == "" {
		root = "."
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, nil
	}

	var matches []string

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if skipWalkEntry(entry) {
			return fs.SkipDir
		}

		if entry.IsDir() || !matchSegments(rest, pathSegments(path, root)) {
			return nil
		}

		matches = append(matches, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to match %q: %w", pattern, err)
	}

	sort.Strings(matches)

	return matches, nil
}

// globRoot returns the leading part of the pattern with no wildcard in it,
// which is the only place the walk can start from.
func globRoot(segments []string) string {
	for i, segment := range segments {
		if hasGlobMagic(segment) {
			return strings.Join(segments[:i], "/")
		}
	}

	return strings.Join(segments, "/")
}

// skipWalkEntry reports whether a directory entry must not be descended into.
func skipWalkEntry(entry fs.DirEntry) bool {
	return entry.IsDir() && skippedDirs[entry.Name()]
}

// pathSegments strips the root and the separator from a walked path, so the
// remainder can be compared against the pattern segment by segment.
func pathSegments(path, root string) []string {
	relative := strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(root))
	relative = strings.TrimPrefix(relative, "/")

	if relative == "" {
		return nil
	}

	return strings.Split(relative, "/")
}

// matchSegments reports whether a segment list satisfies a pattern, where `**`
// absorbs any number of segments including none.
func matchSegments(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}

	if pattern[0] == doubleStar {
		return matchDoubleStar(pattern, segments)
	}

	if len(segments) == 0 || !matchSegment(pattern[0], segments[0]) {
		return false
	}

	return matchSegments(pattern[1:], segments[1:])
}

// matchDoubleStar lets `**` stand for zero or more segments by trying the rest
// of the pattern at every offset.
func matchDoubleStar(pattern, segments []string) bool {
	if len(pattern) == 1 {
		// A trailing `**` matches whatever is left, including nothing.
		return true
	}

	for i := 0; i <= len(segments); i++ {
		if matchSegments(pattern[1:], segments[i:]) {
			return true
		}
	}

	return false
}

// matchSegment compares one pattern segment.
//
// A malformed pattern reports no match rather than an error: the caller asks
// about many candidates and one unusable pattern should not abort the whole run.
func matchSegment(pattern, segment string) bool {
	matched, err := filepath.Match(pattern, segment)

	return err == nil && matched
}

// expandDir collects the files worth updating under dir, in walk order.
//
// Only names that are recognised as a Compose file or a Dockerfile are taken,
// because anything else in a tree is not something shiphoist knows how to read
// and a run over it would report nothing rather than fail. suffixes widen what
// counts as a Compose file, so `--suffix .j2` makes the walk pick up an Ansible
// template alongside the rendered file.
func expandDir(dir string, suffixes []string) ([]string, error) {
	var found []string

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if skipWalkEntry(entry) {
			return fs.SkipDir
		}

		if entry.IsDir() || !isUpdatableName(path, suffixes) {
			return nil
		}

		found = append(found, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan %s: %w", dir, err)
	}

	sort.Strings(found)

	return found, nil
}

// isUpdatableName reports whether a file is one shiphoist can read.
//
// suffixes are extra --suffix values marking a file generated from a template. The
// name under the suffix is still the Compose name, so `docker-compose.yml.j2` is
// checked as `docker-compose.yml` — the reference in an Ansible template is the
// same reference the rendered file would carry.
func isUpdatableName(path string, suffixes []string) bool {
	if discovery.IsDockerfileName(path) {
		return true
	}

	name := strings.ToLower(filepath.Base(path))

	for _, stem := range composeStems {
		if isComposeVariant(name, stem, suffixes) {
			return true
		}
	}

	return false
}

// isComposeVariant reports whether name is a Compose file built on the given
// isComposeVariant reports whether name is a Compose file built on the given
// stem, either bare or carrying a variant such as `.override` or `.prod`, and with
// or without one of the --suffix values.
//
// The empty suffix stands for the plain name, so one pass covers both cases. The
// stem is rechecked per suffix rather than accepted once: `docker-compose.yml.j2`
// qualifies only with the suffix stripped, and `docker-compose.yml` only with it
// absent, so a suffix must not be able to lift an unrelated stem.
func isComposeVariant(name, stem string, suffixes []string) bool {
	for _, suffix := range append([]string{""}, normaliseSuffixes(suffixes)...) {
		trimmed := strings.TrimSuffix(name, suffix)

		for _, extension := range composeExtension {
			if trimmed == stem+extension {
				return true
			}

			if strings.HasPrefix(trimmed, stem+".") && strings.HasSuffix(trimmed, extension) {
				return true
			}
		}
	}

	return false
}

// normaliseSuffixes makes the --suffix values comparable to a file name.
//
// A missing leading dot is added rather than rejected, because `--suffix j2` is
// as natural to write as `--suffix .j2` and a forgotten dot is not worth an error
// message. Values are lowercased because the name comparison already is, and a
// value with nothing after its dot is dropped: a lone dot is the same slip as
// passing an empty --suffix, and a suffix that can never match anything is not
// worth carrying.
func normaliseSuffixes(values []string) []string {
	normalised := make([]string, 0, len(values))

	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		trimmed = strings.TrimLeft(trimmed, ".")

		if trimmed == "" {
			continue
		}

		normalised = append(normalised, "."+trimmed)
	}

	return normalised
}

// dedupe keeps the first occurrence of each path, so a run over the same file
// twice processes it once.
func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	unique := make([]string, 0, len(paths))

	for _, path := range paths {
		if seen[path] {
			continue
		}

		seen[path] = true
		unique = append(unique, path)
	}

	return unique
}
