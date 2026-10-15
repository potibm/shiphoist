package discovery

import "strings"

// fromStatement is a parsed `FROM` line.
type fromStatement struct {
	// Image is the reference exactly as written, without the trailing alias.
	Image string

	// Alias is the `AS` name, empty when the stage is unnamed.
	Alias string
}

// reservedBase is the empty image. It has no registry, no tags and no digest,
// so there is nothing to resolve or update.
const reservedBase = "scratch"

// fromKeyword and asKeyword are case-insensitive in a Dockerfile.
const (
	fromKeyword = "from"
	asKeyword   = "as"

	// aliasTokenCount is an alias keyword plus the name it introduces.
	aliasTokenCount = 2
)

// parseFrom reads a `FROM` instruction from a single line.
//
// It returns ok false for anything that is not a usable FROM: another
// instruction, a malformed line, or one whose image is a build-argument
// reference, which cannot be updated safely because `--build-arg` can override it
// at build time and the default is then not the truth.
func parseFrom(line string) (fromStatement, bool) {
	fields := fieldsWithoutComment(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], fromKeyword) {
		return fromStatement{}, false
	}

	image, alias, ok := imageAndAlias(fields[1:])
	if !ok {
		return fromStatement{}, false
	}

	return fromStatement{Image: image, Alias: alias}, true
}

// imageAndAlias picks the reference out of the arguments, skipping the flags
// Docker allows in front of it, and then reads the optional stage alias.
func imageAndAlias(args []string) (image, alias string, ok bool) {
	rest := skipFlags(args)

	if len(rest) == 0 {
		return "", "", false
	}

	image = rest[0]
	rest = rest[1:]

	// An `AS` with nothing after it is a malformed line that Docker itself
	// rejects. Reporting it as an unnamed stage would mean claiming to
	// understand a file we do not.
	if len(rest) > 0 && strings.EqualFold(rest[0], asKeyword) {
		if len(rest) < aliasTokenCount {
			return "", "", false
		}

		return image, rest[1], true
	}

	return image, "", true
}

// skipFlags drops `--platform=...` and any other leading option.
func skipFlags(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		args = args[1:]
	}

	return args
}

// fieldsWithoutComment splits a line into arguments, dropping a trailing
// comment.
//
// Only a `#` preceded by whitespace starts a comment, so a `#` inside a token
// is left alone.
func fieldsWithoutComment(line string) []string {
	if idx := commentStart(line); idx >= 0 {
		line = line[:idx]
	}

	return strings.Fields(line)
}

// commentStart returns the index of the comment marker, or -1.
func commentStart(line string) int {
	for i := range len(line) {
		if line[i] != '#' {
			continue
		}

		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return i
		}
	}

	return -1
}

// isVariableReference reports whether a reference is built from a build
// argument, such as `$BASE`, `${BASE}` or `node:$NODE_TAG`.
//
// A `$` cannot appear in a real image reference — the grammar admits only
// alphanumerics, `.`, `_`, `-`, `/`, `:` and `@` — so its presence anywhere in
// the token means a substitution happens here. Looking for it only at the start
// would miss the common `node:$NODE` form, where only the tag is a variable.
//
// These are deliberately not resolved and not updated. The default in the `ARG`
// instruction is only a default: `docker build --build-arg BASE=...` overrides
// it, and rewriting the `ARG` line would change the build's default rather than
// the image the stage actually uses.
func isVariableReference(image string) bool {
	return strings.Contains(image, "$")
}

// isReservedBase reports whether the reference is Docker's empty base image.
func isReservedBase(image string) bool {
	return strings.EqualFold(image, reservedBase)
}
