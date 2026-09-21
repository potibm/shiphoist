package patcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/potibm/shiphoist/internal/core"
)

// FilePatcher implements the core.Patcher interface
type FilePatcher struct{}

// Patch applies the updates to the given file surgically by line number
func (p *FilePatcher) Patch(ctx context.Context, filePath string, updates []core.ImageUpdate) error {
	// 1. Read the entire file line by line
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	file.Close()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	// 2. Apply updates in memory
	for _, update := range updates {
		// Skip if no new digest is provided (safety check)
		if update.NewDigest == "" {
			continue
		}

		// AST line numbers are 1-based, slice indices are 0-based
		lineIdx := update.LineNumber - 1

		// Sanity check to prevent panics
		if lineIdx < 0 || lineIdx >= len(lines) {
			return fmt.Errorf("line number %d out of range for file %s", update.LineNumber, filePath)
		}

		// Construct the new image string: image:tag@digest
		// Note: Depending on your registry fetcher logic, NewTag might already be the OldTag if it hasn't changed.
		newImageString := fmt.Sprintf("%s:%s@%s", update.ImageName, update.NewTag, update.NewDigest)

		// Surgically replace only the exact old image string on this specific line.
		// This preserves all indentation and inline comments.
		lines[lineIdx] = strings.Replace(lines[lineIdx], update.OriginalString, newImageString, 1)
	}

	// 3. Write the modified lines back to the file
	// Open file for writing, truncate existing content
	out, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file for writing %s: %w", filePath, err)
	}
	defer out.Close()

	writer := bufio.NewWriter(out)
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("failed to write line: %w", err)
		}
	}

	return writer.Flush()
}
