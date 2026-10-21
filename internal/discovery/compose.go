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
//
// Files generated from a template are read through a mask: template syntax is
// not YAML, and a Compose file in an Ansible role carries it on almost every
// value. Masking lets the same AST walk find the images without touching the
// template expressions that the renderer still needs.
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

	mask := newTemplateMask()

	f, err := parser.ParseBytes(mask.mask(data), 0)
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

	c.extractImagesFromNode(node, &updates, filePath, lines, mask)

	return updates, nil
}

// Filtered returns the references skipped before the registry was consulted.
func (c *ComposeDiscoverer) Filtered() []core.Filtered {
	return append([]core.Filtered(nil), c.filtered...)
}

func (c *ComposeDiscoverer) extractImagesFromNode(
	n ast.Node,
	updates *[]core.ImageUpdate,
	filePath string,
	lines []string,
	mask *templateMask,
) {
	switch v := n.(type) {
	case *ast.MappingNode:
		c.extractImagesFromMapping(v, updates, filePath, lines, mask)
	case *ast.SequenceNode:
		for _, child := range v.Values {
			c.extractImagesFromNode(child, updates, filePath, lines, mask)
		}
	}
}

func (c *ComposeDiscoverer) extractImagesFromMapping(
	v *ast.MappingNode,
	updates *[]core.ImageUpdate,
	filePath string,
	lines []string,
	mask *templateMask,
) {
	for _, service := range v.Values {
		c.extractImagesFromService(service, updates, filePath, lines, mask)
	}
}

// extractImagesFromService collects the image of one service, if it has one.
func (c *ComposeDiscoverer) extractImagesFromService(
	service *ast.MappingValueNode,
	updates *[]core.ImageUpdate,
	filePath string,
	lines []string,
	mask *templateMask,
) {
	serviceDef, ok := service.Value.(*ast.MappingNode)
	if !ok {
		return
	}

	serviceName := serviceNameOf(service)
	if hasTemplate(serviceName) {
		serviceName = mask.restore(serviceName)
	}

	for _, prop := range serviceDef.Values {
		if update, ok := c.imageUpdateOf(prop, filePath, serviceName, lines, mask); ok {
			*updates = append(*updates, update)
		}
	}
}

// imageUpdateOf turns one service property into an update, reporting false when
// the property is not an updatable image reference.
func (c *ComposeDiscoverer) imageUpdateOf(
	prop *ast.MappingValueNode,
	filePath, serviceName string,
	lines []string,
	mask *templateMask,
) (core.ImageUpdate, bool) {
	key, ok := prop.Key.(*ast.StringNode)
	if !ok || key.Value != "image" {
		return core.ImageUpdate{}, false
	}

	if isNullNode(prop.Value) {
		return core.ImageUpdate{}, false
	}

	// A templated reference is checked before the directive: its placeholder
	// may have shifted the columns the directive scan relies on, and the
	// reference is skipped either way.
	if c.recordTemplated(prop.Value, filePath, mask) {
		return core.ImageUpdate{}, false
	}

	if c.isIgnored(prop.Value, filePath, lines) {
		return core.ImageUpdate{}, false
	}

	return extractImageUpdate(prop.Value, filePath, serviceName), true
}

// recordTemplated reports a reference whose value is a template expression and
// returns whether the node was one.
//
// Its repository only exists after rendering, so a registry lookup would be
// guesswork and a rewrite would replace a piece of the template. Reporting the
// skip keeps the run honest: the reference is not silently missing from the
// count.
func (c *ComposeDiscoverer) recordTemplated(node ast.Node, filePath string, mask *templateMask) bool {
	token := node.GetToken()
	if token == nil || !hasTemplate(token.Value) {
		return false
	}

	c.filtered = append(c.filtered, core.Filtered{
		FilePath:   filePath,
		Image:      mask.restore(token.Value),
		LineNumber: token.Position.Line,
		Reason:     ReasonTemplated,
	})

	return true
}

// isIgnored reports whether the reference at node carries the inline directive,
// recording the skip when it does.
func (c *ComposeDiscoverer) isIgnored(node ast.Node, filePath string, lines []string) bool {
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
		FilePath:   filePath,
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
