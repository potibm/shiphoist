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

		for _, prop := range serviceDef.Values {
			key, ok := prop.Key.(*ast.StringNode)
			if !ok || key.Value != "image" {
				continue
			}

			*updates = append(*updates, extractImageUpdate(prop.Value, filePath))
		}
	}
}

// Helper function to extract metadata cleanly from the node.
func extractImageUpdate(node ast.Node, filePath string) core.ImageUpdate {
	token := node.GetToken()
	originalString := token.Value
	lineNumber := token.Position.Line

	imageName, oldTag, oldDigest := ParseImageReference(originalString)

	return core.ImageUpdate{
		FilePath:       filePath,
		LineNumber:     lineNumber,
		OriginalString: originalString,
		ImageName:      imageName,
		OldTag:         oldTag,
		OldDigest:      oldDigest,
	}
}
