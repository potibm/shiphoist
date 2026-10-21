package discovery

import (
	"fmt"
	"regexp"
	"strings"
)

// ReasonTemplated is a reference whose value is a template expression.
//
// The expression is resolved by the template engine before the Compose file is
// read, so the repository, tag or digest it produces cannot be known here.
// Rewriting a line that only exists after rendering would corrupt the template,
// so the reference is reported instead of checked.
const ReasonTemplated = "templated-reference"

// templatePlaceholderPrefix marks a value that came from a template expression.
//
// The marker is a valid YAML scalar and contains none of the characters the
// patterns below look for, so masking pattern by pattern never rewrites its own
// output.
const templatePlaceholderPrefix = "__SHIPHOIST_TEMPLATE_"

// replacementsPerTemplate is how many strings one placeholder contributes to
// the replacer that restores it: the placeholder and the expression it stands
// for.
const replacementsPerTemplate = 2

// templateStatements are constructs that produce no value of their own.
//
// Dropping them leaves the surrounding YAML intact: a `{% if %}` around a
// service, a label or a network is a line of its own, and a blank line is valid
// wherever a statement can appear. Statements that sit inline are removed the
// same way, which joins the text around them as if the branch were taken.
var templateStatements = []*regexp.Regexp{
	regexp.MustCompile(`\{#.*?#\}`),             // Jinja2 and Liquid comment
	regexp.MustCompile(`\{\{-?/\*.*?\*/-?\}\}`), // Go template comment
	regexp.MustCompile(`\{\{!.*?\}\}`),          // Handlebars comment
	regexp.MustCompile(`\{%.*?%\}`),             // Jinja2 and Go template statement
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
		text = statement.ReplaceAllString(text, "")
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

// hasTemplate reports whether a parsed value came from a template expression.
func hasTemplate(value string) bool {
	return strings.Contains(value, templatePlaceholderPrefix)
}

// restore puts the original expressions back, so a reported reference reads the
// way it does in the file.
func (m *templateMask) restore(value string) string {
	replacements := make([]string, 0, len(m.originals)*replacementsPerTemplate)

	for placeholder, original := range m.originals {
		replacements = append(replacements, placeholder, original)
	}

	if len(replacements) == 0 {
		return value
	}

	return strings.NewReplacer(replacements...).Replace(value)
}
