package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	columnMarker  = 0
	columnName    = 1
	columnImage   = 2
	columnVersion = 3
	columnKind    = 4
	columnCount   = 5

	// columnGap is the number of spaces between two columns.
	columnGap = 2

	// minColumnWidth is the narrowest a shrinkable column may become before
	// the layout stops trying to make room.
	minColumnWidth = 6

	ellipsis = "…"
)

// shrinkableColumns are the only columns RenderRows will shorten. The marker,
// version and kind carry the meaning of a row, so truncating them would hide
// the very information the row exists to convey.
var shrinkableColumns = [...]int{columnName, columnImage}

// Row is one line of tabular output. Empty fields are allowed but leave a
// visible gap, so prefer omitting a whole column over blanking one field.
type Row struct {
	Marker  string
	Name    string
	Image   string
	Version string
	Kind    string
}

// cell returns the value of the given column.
func (r Row) cell(column int) string {
	switch column {
	case columnMarker:
		return r.Marker
	case columnName:
		return r.Name
	case columnImage:
		return r.Image
	case columnVersion:
		return r.Version
	case columnKind:
		return r.Kind
	default:
		return r.Kind
	}
}

// RenderRows lays out rows into aligned columns that fit within budget display
// columns.
//
// Columns are first padded to their natural width. Only if that overflows are
// the wide, shrinkable columns shortened, widest first, so the common case of a
// short table is never distorted. Budget must leave room for whatever chrome
// the caller renders around the row, or the surrounding renderer may wrap it.
func RenderRows(rows []Row, budget int) []string {
	if len(rows) == 0 {
		return nil
	}

	widths := columnWidths(rows)
	shrinkToFit(widths, budget)

	rendered := make([]string, 0, len(rows))
	for _, row := range rows {
		rendered = append(rendered, renderRow(row, widths))
	}

	return rendered
}

// columnWidths returns the natural display width of each column.
func columnWidths(rows []Row) []int {
	widths := make([]int, columnCount)

	for _, row := range rows {
		for column := range columnCount {
			if width := ansi.StringWidth(row.cell(column)); width > widths[column] {
				widths[column] = width
			}
		}
	}

	return widths
}

// naturalWidth is the width the columns occupy with their gaps. Columns that
// are empty in every row carry neither a cell nor a gap.
func naturalWidth(widths []int) int {
	total := columnGap * max(activeColumns(widths)-1, 0)
	for _, width := range widths {
		total += width
	}

	return total
}

// activeColumns returns how many columns carry content.
func activeColumns(widths []int) int {
	active := 0

	for _, width := range widths {
		if width > 0 {
			active++
		}
	}

	return active
}

// shrinkToFit shortens the widest shrinkable columns until the layout fits the
// budget. It gives up gracefully when every shrinkable column is already at
// minimum width, leaving the overflow to the caller rather than looping.
func shrinkToFit(widths []int, budget int) {
	for overflow := naturalWidth(widths) - budget; overflow > 0; overflow = naturalWidth(widths) - budget {
		column, ok := widestShrinkable(widths)
		if !ok {
			return
		}

		widths[column] -= min(overflow, widths[column]-minColumnWidth)
	}
}

// widestShrinkable returns the shrinkable column with room to shrink.
func widestShrinkable(widths []int) (int, bool) {
	widest := -1
	widestWidth := minColumnWidth

	for _, column := range shrinkableColumns {
		if widths[column] <= widestWidth {
			continue
		}

		widest = column
		widestWidth = widths[column]
	}

	return widest, widest >= 0
}

// renderRow pads every populated cell to its column width and joins them. A
// column that is empty across every row is skipped, so an unused leading
// column cannot push the row out of alignment. Trailing padding is dropped so
// lines carry no invisible whitespace.
func renderRow(row Row, widths []int) string {
	cells := make([]string, 0, columnCount)

	for column := range columnCount {
		if widths[column] == 0 {
			continue
		}

		cells = append(cells, fit(row.cell(column), widths[column]))
	}

	return strings.TrimRight(strings.Join(cells, strings.Repeat(" ", columnGap)), " ")
}

// fit truncates or pads a value to exactly width display columns.
func fit(value string, width int) string {
	if ansi.StringWidth(value) > width {
		return ansi.Truncate(value, width, ellipsis)
	}

	return value + strings.Repeat(" ", width-ansi.StringWidth(value))
}
