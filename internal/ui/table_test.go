package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func sampleRows() []Row {
	return []Row{
		{Marker: "🟢", Name: "grafana", Image: "grafana/grafana", Version: "11.6.16 → 11.6.17", Kind: "pin"},
		{Marker: "🟡", Name: "web", Image: "ghcr.io/potibm/funkapparat", Version: "0.2 → 0.3", Kind: "minor"},
	}
}

// columnOf returns the display column where needle begins, or -1 when absent.
// Offsets are display cells rather than bytes, because the markers are wide.
func columnOf(row, needle string) int {
	index := strings.Index(row, needle)
	if index < 0 {
		return -1
	}

	return ansi.StringWidth(row[:index])
}

func TestRenderRows_Empty(t *testing.T) {
	if got := RenderRows(nil, 80); got != nil {
		t.Errorf("expected nil for no rows, got %v", got)
	}
}

func TestRenderRows_AlignsColumns(t *testing.T) {
	rows := RenderRows(sampleRows(), 200)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// The image column must start at the same display column on both rows.
	first := columnOf(rows[0], "grafana/grafana")
	second := columnOf(rows[1], "ghcr.io/potibm/funkapparat")

	if first != second {
		t.Errorf("expected the image column to be aligned, got %d and %d:\n%q\n%q", first, second, rows[0], rows[1])
	}
}

func TestRenderRows_DropsTrailingWhitespace(t *testing.T) {
	rows := RenderRows([]Row{{Image: "nginx"}}, 200)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0] != "nginx" {
		t.Errorf("expected no padding when other columns are empty, got %q", rows[0])
	}
}

func TestRenderRows_StaysWithinBudget(t *testing.T) {
	long := strings.Repeat("a", 60)

	rows := []Row{
		{
			Marker:  "🟢",
			Name:    "billedapparat-collector-protokolapparat-timetable",
			Image:   long,
			Version: "1.0 → 2.0",
			Kind:    "minor",
		},
		{Marker: "🔵", Name: "web", Image: "nginx", Version: "1.0", Kind: "pin"},
	}

	for _, budget := range []int{200, 120, 80, 60, 40} {
		rendered := RenderRows(rows, budget)

		for i, row := range rendered {
			if got := ansi.StringWidth(row); got > budget {
				t.Errorf("budget %d: row %d is %d columns wide:\n%q", budget, i, got, row)
			}
		}
	}
}

// Truncation is mandatory, not cosmetic: a renderer that wraps instead will
// break every column beneath the overflow.
func TestRenderRows_TruncatesRatherThanWrapping(t *testing.T) {
	rows := RenderRows([]Row{{
		Name:    strings.Repeat("n", 80),
		Image:   strings.Repeat("i", 80),
		Version: "1.0.0",
	}}, 80)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if strings.Contains(rows[0], "\n") {
		t.Errorf("expected a single line, got %q", rows[0])
	}

	if !strings.Contains(rows[0], ellipsis) {
		t.Errorf("expected truncation to be marked with %q, got %q", ellipsis, rows[0])
	}

	// The version column must survive intact, since it is the point of the row.
	if !strings.Contains(rows[0], "1.0.0") {
		t.Errorf("expected the version to be preserved, got %q", rows[0])
	}
}

// The marker column carries meaning, so it must never be truncated away.
func TestRenderRows_NeverShrinksMeaningfulColumns(t *testing.T) {
	rows := RenderRows([]Row{
		{
			Marker:  "🔴",
			Name:    strings.Repeat("n", 90),
			Image:   strings.Repeat("i", 90),
			Version: "1.0 → 2.0",
			Kind:    "major",
		},
	}, 70)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	for _, want := range []string{"🔴", "1.0 → 2.0", "major"} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("expected %q to survive truncation, got %q", want, rows[0])
		}
	}
}

// The version column must still line up after truncation.
func TestRenderRows_AlignsAfterTruncation(t *testing.T) {
	rows := RenderRows([]Row{
		{Marker: "🟢", Name: strings.Repeat("n", 40), Image: "short", Version: "1.0.0", Kind: "pin"},
		{Marker: "🔵", Name: "web", Image: strings.Repeat("i", 40), Version: "2.0.0", Kind: "minor"},
	}, 80)

	first := columnOf(rows[0], "1.0.0")
	second := columnOf(rows[1], "2.0.0")

	if first < 0 || second < 0 {
		t.Fatalf("expected both versions to survive, got:\n%q\n%q", rows[0], rows[1])
	}

	if first != second {
		t.Errorf("expected the version column to stay aligned:\n%q\n%q", rows[0], rows[1])
	}
}

// Wide runes must be measured in display cells, not bytes, or every column
// after them drifts.
func TestRenderRows_HandlesWideRunes(t *testing.T) {
	rows := []Row{
		{Name: "日本語のサービス", Image: "img", Version: "1", Kind: "pin"},
		{Name: "web", Image: "img", Version: "1", Kind: "pin"},
	}

	rendered := RenderRows(rows, 200)

	first := columnOf(rendered[0], "img")
	second := columnOf(rendered[1], "img")

	if first < 0 || second < 0 {
		t.Fatalf("expected both rows to render, got:\n%q\n%q", rendered[0], rendered[1])
	}

	if first != second {
		t.Errorf("expected alignment with wide runes:\n%q\n%q", rendered[0], rendered[1])
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		width    int
		expected string
	}{
		{name: "pads short value", value: "ab", width: 5, expected: "ab   "},
		{name: "exact fit", value: "abcde", width: 5, expected: "abcde"},
		{name: "truncates long value", value: "abcdefgh", width: 5, expected: "abcd" + ellipsis},
		{name: "zero width", value: "abc", width: 0, expected: ""},
		{name: "empty value", value: "", width: 3, expected: "   "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fit(tc.value, tc.width); got != tc.expected {
				t.Errorf("fit(%q, %d) = %q, want %q", tc.value, tc.width, got, tc.expected)
			}
		})
	}
}

func TestShrinkToFit_StopsAtMinimumColumnWidth(t *testing.T) {
	widths := []int{2, 40, 40, 10, 5}

	// Even the minimum layout cannot fit this budget; the function must give
	// up rather than spin or produce a negative width.
	shrinkToFit(widths, 1)

	for i, width := range widths {
		if width < 0 {
			t.Errorf("column %d went negative: %d", i, width)
		}
	}

	if widths[columnMarker] != 2 || widths[columnVersion] != 10 || widths[columnKind] != 5 {
		t.Errorf("expected non-shrinkable columns to be untouched, got %v", widths)
	}
}

func TestWidestShrinkable(t *testing.T) {
	column, ok := widestShrinkable([]int{2, 30, 40, 10, 5})
	if !ok || column != columnImage {
		t.Errorf("expected the image column, got %d (ok=%v)", column, ok)
	}

	if _, ok := widestShrinkable([]int{2, 6, 6, 10, 5}); ok {
		t.Error("expected no candidate once every shrinkable column is at minimum")
	}
}

func TestRowCell(t *testing.T) {
	row := Row{Marker: "M", Name: "N", Image: "I", Version: "V", Kind: "K"}

	tests := []struct {
		column   int
		expected string
	}{
		{columnMarker, "M"},
		{columnName, "N"},
		{columnImage, "I"},
		{columnVersion, "V"},
		{columnKind, "K"},
		{99, "K"},
	}

	for _, tc := range tests {
		if got := row.cell(tc.column); got != tc.expected {
			t.Errorf("cell(%d) = %q, want %q", tc.column, got, tc.expected)
		}
	}
}

func TestNaturalWidth(t *testing.T) {
	got := naturalWidth([]int{2, 4, 6, 3, 5})
	want := 2 + 4 + 6 + 3 + 5 + columnGap*(columnCount-1)

	if got != want {
		t.Errorf("naturalWidth() = %d, want %d", got, want)
	}
}
