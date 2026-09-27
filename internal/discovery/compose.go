package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/potibm/shiphoist/internal/core"
)

// ComposeDiscoverer finds image references in a Compose file.
//
// It honours the inline ignore directive, because the comment only exists in
// the source text and is gone once the AST has been reduced to tokens.
type ComposeDiscoverer struct {
	filtered []core.Filtered
}

// Discover returns every image reference that is not ignored.
func (c *ComposeDiscoverer) Discover(_ context.Context, filePath string) ([]core.ImageUpdate, error) {
	c.filtered = nil

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	f, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AST: %w", err)
	}

	path, err := yaml.PathString("$.services")
	if err != nil {
		return nil, fmt.Errorf("invalid YAMLPath: %w", err)
	}

	node, err := path.FilterFile(f)
	if err != nil {
		return []core.ImageUpdate{}, nil
	}

	// The raw lines let the directive be read back from the text the token
	// points into, which the AST does not preserve reliably.
	lines := strings.Split(string(data), "\n")

	var updates []core.ImageUpdate

	c.extractImagesFromNode(node, &updates, filePath, lines)

	return updates, nil
}

// Filtered returns the references skipped by the ignore directive.
func (c *ComposeDiscoverer) Filtered() []core.Filtered {
	return append([]core.Filtered(nil), c.filtered...)
}

func (c *ComposeDiscoverer) extractImagesFromNode(
	n ast.Node,
	updates *[]core.ImageUpdate,
	filePath string,
	lines []string,
) {
	switch v := n.(type) {
	case *ast.MappingNode:
		c.extractImagesFromMapping(v, updates, filePath, lines)
	case *ast.SequenceNode:
		for _, child := range v.Values {
			c.extractImagesFromNode(child, updates, filePath, lines)
		}
	}
}

func (c *ComposeDiscoverer) extractImagesFromMapping(
	v *ast.MappingNode,
	updates *[]core.ImageUpdate,
	filePath string,
	lines []string,
) {
	for _, service := range v.Values {
		serviceDef, ok := service.Value.(*ast.MappingNode)
		if !ok {
			continue
		}

		serviceName := serviceNameOf(service)

		for _, prop := range serviceDef.Values {
			key, ok := prop.Key.(*ast.StringNode)
			if !ok || key.Value != "image" {
				continue
			}

			if isNullNode(prop.Value) {
				continue
			}

			if c.isIgnored(prop.Value, lines) {
				continue
			}

			*updates = append(*updates, extractImageUpdate(prop.Value, filePath, serviceName))
		}
	}
}

// isIgnored reports whether the reference at node carries the inline directive,
// recording the skip when it does.
func (c *ComposeDiscoverer) isIgnored(node ast.Node, lines []string) bool {
	token := node.GetToken()
	if token == nil {
		return false
	}

	line, ok := lineAt(lines, token.Position.Line)
	if !ok {
		return false
	}

	if !hasIgnoreDirective(line, token.Position.Column) {
		return false
	}

	// Report the repository, not the literal reference, so it reads the same
	// way as a --exclude match.
	imageName, _, _ := ParseImageReference(token.Value)

	c.filtered = append(c.filtered, core.Filtered{
		Image:      imageName,
		LineNumber: token.Position.Line,
		Reason:     ReasonIgnoreDirective,
	})

	return true
}

// lineAt returns the 1-based line, reporting false when the position is outside
// the file.
func lineAt(lines []string, number int) (string, bool) {
	if number < 1 || number > len(lines) {
		return "", false
	}

	return lines[number-1], true
}

// isNullNode reports whether a value carries no reference at all, as in
// `image:` with nothing after it. Without this guard the literal text "null"
// would be treated as an image name and written back into the file.
func isNullNode(node ast.Node) bool {
	if _, ok := node.(*ast.NullNode); ok {
		return true
	}

	tkn := node.GetToken()

	return tkn == nil || tkn.Value == ""
}

// serviceNameOf returns the Compose service key, or an empty string when the
// mapping key is not a plain scalar.
func serviceNameOf(service *ast.MappingValueNode) string {
	key, ok := service.Key.(*ast.StringNode)
	if !ok {
		return ""
	}

	return key.Value
}

// extractImageUpdate turns an image value node into an update, parsing the
// reference into its components.
func extractImageUpdate(node ast.Node, filePath, serviceName string) core.ImageUpdate {
	token := node.GetToken()

	imageName, oldTag, oldDigest := ParseImageReference(token.Value)

	return core.ImageUpdate{
		FilePath:       filePath,
		LineNumber:     token.Position.Line,
		ServiceName:    serviceName,
		OriginalString: token.Value,
		ImageName:      imageName,
		OldTag:         oldTag,
		OldDigest:      oldDigest,
	}
}
