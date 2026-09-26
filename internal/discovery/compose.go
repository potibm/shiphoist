package discovery

import (
	"context"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/potibm/shiphoist/internal/core"
)

type ComposeDiscoverer struct{}

func (c *ComposeDiscoverer) Discover(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
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

	var updates []core.ImageUpdate

	c.extractImagesFromNode(node, &updates, filePath)

	return updates, nil
}

func (c *ComposeDiscoverer) extractImagesFromNode(n ast.Node, updates *[]core.ImageUpdate, filePath string) {
	switch v := n.(type) {
	case *ast.MappingNode:
		c.extractImagesFromMapping(v, updates, filePath)
	case *ast.SequenceNode:
		for _, child := range v.Values {
			c.extractImagesFromNode(child, updates, filePath)
		}
	}
}

func (c *ComposeDiscoverer) extractImagesFromMapping(v *ast.MappingNode, updates *[]core.ImageUpdate, filePath string) {
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

			*updates = append(*updates, extractImageUpdate(prop.Value, filePath, serviceName))
		}
	}
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

// Helper function to extract metadata cleanly from the node.
func extractImageUpdate(node ast.Node, filePath, serviceName string) core.ImageUpdate {
	token := node.GetToken()
	originalString := token.Value
	lineNumber := token.Position.Line

	imageName, oldTag, oldDigest := ParseImageReference(originalString)

	return core.ImageUpdate{
		FilePath:       filePath,
		LineNumber:     lineNumber,
		ServiceName:    serviceName,
		OriginalString: originalString,
		ImageName:      imageName,
		OldTag:         oldTag,
		OldDigest:      oldDigest,
	}
}
