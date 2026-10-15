package discovery

import "strings"

// ignoreDirective marks a reference the user has pinned deliberately.
const ignoreDirective = "shiphoist-ignore"

// Reasons a reference can be filtered, recorded in core.Filtered.Reason.
const (
	// ReasonIgnoreDirective is a reference carrying the inline directive.
	ReasonIgnoreDirective = "ignore-directive"

	// ReasonExcluded is a reference matched by --exclude.
	ReasonExcluded = "excluded"
)

// hasIgnoreDirective reports whether the declaration carries the inline
// directive, which must appear on the same line as the reference.
//
// column is where the reference starts in line, so the scan skips the YAML in
// front of it. That matters: without it, a comment belonging to the preceding
// key would be read as a directive on this one. Only what follows the reference
// can be a trailing comment, and an image reference cannot itself contain a
// `#`, so the first `#` from there is unambiguously a comment.
//
// The directive has to be the first thing in the comment. A mention is not a
// directive: `# TODO shiphoist-ignore this later` is somebody noting intent, and
// acting on it would silently skip an image the user still wants updated. Once
// the directive is established, a free-form reason may follow, so both
// `# shiphoist-ignore` and `# shiphoist-ignore: pinned by policy` work.
//
// A column outside the line falls back to scanning the whole line. Missing an
// explicit directive means rewriting an image the user asked us to leave alone,
// which is the harmful direction to be wrong in.
func hasIgnoreDirective(line string, column int) bool {
	tail := line
	if column > 1 && column <= len(line) {
		tail = line[column-1:]
	}

	comment := strings.IndexByte(tail, '#')
	if comment < 0 {
		return false
	}

	return startsWithDirective(tail[comment+1:])
}

// startsWithDirective reports whether the comment body opens with the
// directive, followed by nothing or by a word boundary.
func startsWithDirective(body string) bool {
	rest := strings.TrimLeft(body, " \t")
	rest = strings.ToLower(rest)

	if !strings.HasPrefix(rest, ignoreDirective) {
		return false
	}

	// Reject a longer word that merely begins the same way, such as
	// "shiphoist-ignored".
	remainder := rest[len(ignoreDirective):]
	if remainder == "" {
		return true
	}

	return !isWordByte(remainder[0])
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
