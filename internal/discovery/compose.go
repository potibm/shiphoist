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

	// 1. Build the complete AST (preserves all meta information)
	f, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AST: %w", err)
	}

	// 2. YAMLPath: Get the "services" root node
	// We avoid wildcards here because the YAMLPath parser is strict about them
	path, err := yaml.PathString("$.services")
	if err != nil {
		return nil, fmt.Errorf("invalid YAMLPath: %w", err)
	}

	// Apply filter to the AST
	node, err := path.FilterFile(f)
	if err != nil {
		// Nothing found or error during filtering -> return empty array
		return []core.ImageUpdate{}, nil
	}

	var updates []core.ImageUpdate

	// 3. Iterate through the services map safely
	var findImages func(n ast.Node)
	findImages = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.MappingNode:
			// Iterate over all services (e.g., "web", "db", "redis")
			for _, service := range v.Values {
				// A service definition is usually a map itself
				if serviceDef, ok := service.Value.(*ast.MappingNode); ok {
					// Look for the "image" key inside the service definition
					for _, prop := range serviceDef.Values {
						if key, ok := prop.Key.(*ast.StringNode); ok && key.Value == "image" {
							updates = append(updates, extractImageUpdate(prop.Value, filePath))
						}
					}
				}
			}
		case *ast.SequenceNode:
			// FilterFile sometimes wraps results in a SequenceNode
			for _, child := range v.Values {
				findImages(child)
			}
		}
	}

	findImages(node)

	return updates, nil
}

// Helper function to extract metadata cleanly from the node
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
