package discovery

import (
	"fmt"
	"regexp"
	"strings"
)

// templatePlaceholderPrefix marks a value that came from a template expression.
//
// The marker is a valid YAML scalar and contains none of the characters the
// patterns below look for, so masking pattern by pattern never rewrites its own
// output.
const templatePlaceholderPrefix = "__SHIPHOIST_TEMPLATE_"

// templatePlaceholderPattern matches a placeholder the mask wrote.
//
// The digits are matched greedily, so a placeholder is always recognised whole:
// restoring one can never eat the first half of the next, and the order the
// placeholders are restored in does not matter.
var templatePlaceholderPattern = regexp.MustCompile(templatePlaceholderPrefix + `\d+_\d+__`)

// goTemplateStatementPattern matches a Go template statement.
//
// A Go template statement has the same delimiters as an expression, so the
// keyword that opens it is what tells the two apart.
const goTemplateStatementPattern = `\{\{-?\s*(?:if|else|end|range|with|define|block|template|include)\b.*?\}\}`

// templateStatements are constructs that produce no value of their own.
//
// Dropping them leaves the surrounding YAML intact: a `{% if %}` around a
// service, a label or a network is a line of its own, and a blank line is valid
// wherever a statement can appear. Statements that sit inline are removed the
// same way, which joins the text around them as if the branch were taken.
//
// A comment is dropped for the same reason and may span several lines. A Go
// template statement has the same delimiters as an expression, so it is
// recognised by the keyword that opens it; a Handlebars block opens with `#`
// and closes with `/`.
var templateStatements = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\{#.*?#\}`),             // Jinja2 and Liquid comment
	regexp.MustCompile(`(?s)\{\{-?/\*.*?\*/-?\}\}`), // Go template comment
	regexp.MustCompile(`\{\{!.*?\}\}`),              // Handlebars comment
	regexp.MustCompile(`\{%.*?%\}`),                 // Jinja2 statement
	regexp.MustCompile(goTemplateStatementPattern),  // Go template statement
	regexp.MustCompile(`\{\{[#/^].*?\}\}`),          // Handlebars block
}

// templateExpressions are constructs that produce a value. Each is replaced by
// a placeholder so the parser sees valid YAML.
//
// The order matters: `${...}` has to be masked before `$VAR`, or the shorter
// pattern would match its body and leave the braces behind. The syntaxes cover
// the engines that turn up around Compose files: Jinja2 and Ansible, Helm and
// other Go templates, Liquid, Mustache and Handlebars, envsubst and Terraform,
// ERB and EJS.
var templateExpressions = []*regexp.Regexp{
	regexp.MustCompile(`\{\{.*?\}\}`),                 // Jinja2, Go templates, Liquid, Mustache, Handlebars
	regexp.MustCompile(`\$\{(?:[^{}]|\{[^{}]*\})*\}`), // envsubst, Terraform
	regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`),    // envsubst
	regexp.MustCompile(`<%[=#]?.*?%>`),                // ERB, EJS
}

// templateMask rewrites template syntax into YAML the parser accepts while
// keeping the original expressions available for reporting.
type templateMask struct {
	originals map[string]string
}

// newTemplateMask returns an empty mask.
func newTemplateMask() *templateMask {
	return &templateMask{originals: map[string]string{}}
}

// mask rewrites template syntax in data.
//
// The rewrite never adds or removes a line, and never inserts one, so the line
// numbers the parser reports still address the original file and the patcher
// can keep working by line. Placeholders live only in the text handed to the
// parser; the file itself is read and written untouched.
func (m *templateMask) mask(data []byte) []byte {
	text := string(data)

	for _, statement := range templateStatements {
		text = dropStatement(text, statement)
	}

	for patternIndex, expression := range templateExpressions {
		counter := 0

		text = expression.ReplaceAllStringFunc(text, func(match string) string {
			placeholder := fmt.Sprintf("%s%d_%d__", templatePlaceholderPrefix, patternIndex, counter)
			counter++

			m.originals[placeholder] = match

			return placeholder
		})
	}

	return []byte(text)
}

// dropStatement removes a statement from the text and keeps the line count.
//
// A statement is usually a line of its own, but a comment may span several.
// The parser reports line numbers, so the newlines the statement contained are
// put back as blank lines rather than taken away with it.
func dropStatement(text string, statement *regexp.Regexp) string {
	return statement.ReplaceAllStringFunc(text, func(match string) string {
		return strings.Repeat("\n", strings.Count(match, "\n"))
	})
}

// hasTemplate reports whether a parsed value came from a template expression.
func hasTemplate(value string) bool {
	return templatePlaceholderPattern.MatchString(value)
}

// restore puts the original expressions back, so a reported reference reads the
// way it does in the file.
func (m *templateMask) restore(value string) string {
	if len(m.originals) == 0 {
		return value
	}

	return templatePlaceholderPattern.ReplaceAllStringFunc(value, func(placeholder string) string {
		original, ok := m.originals[placeholder]
		if !ok {
			return placeholder
		}

		return original
	})
}
