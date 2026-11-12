package taskpkg

import (
	"fmt"
	"strings"
)

// CellRef identifies the original cell from which an expanded grid slot
// originated. ID is optional. When it is present it is the strongest
// identifier; Row and Col always refer to the original table coordinates,
// not to an expanded slot.
type CellRef struct {
	ID      string
	Row     int
	Col     int
	RowSpan int
	ColSpan int
}

// Key returns a stable identity for a source cell.
func (r CellRef) Key() string {
	if r.ID != "" {
		return "id:" + r.ID
	}
	return fmt.Sprintf("at:%d:%d", r.Row, r.Col)
}

// Matches reports whether two references denote the same original cell.
// Coordinate matching remains available when only one side carries an ID,
// which lets host configuration address cells either by ID or by position.
func (r CellRef) Matches(other CellRef) bool {
	if r.ID != "" && other.ID != "" {
		return r.ID == other.ID
	}
	return r.Row == other.Row && r.Col == other.Col
}

// Cell is one occupied slot in an expanded grid.
type Cell struct {
	Text string

	// Source is the original cell provenance. Slots produced by a span
	// expansion share this value.
	Source CellRef

	// IsRowHeader and IsColumnHeader are supplied by the extraction host.
	// A top-left corner cell may have both flags set.
	IsRowHeader    bool
	IsColumnHeader bool
}

// Grid is an expanded table. All rows must have the same width.
type Grid struct {
	Rows [][]Cell
}

// Validate checks the structural invariants the binder relies on.
func (g Grid) Validate() error {
	if len(g.Rows) == 0 {
		return fmt.Errorf("%w: grid has no rows", ErrInvalidGrid)
	}
	width := len(g.Rows[0])
	if width == 0 {
		return fmt.Errorf("%w: grid has no columns", ErrInvalidGrid)
	}
	for i, row := range g.Rows {
		if len(row) != width {
			return fmt.Errorf("%w: row %d has width %d, want %d", ErrInvalidGrid, i, len(row), width)
		}
	}
	return nil
}

// Width returns the number of columns in the expanded grid, or zero when the
// grid is empty. Call Validate before relying on a rectangular shape.
func (g Grid) Width() int {
	if len(g.Rows) == 0 {
		return 0
	}
	return len(g.Rows[0])
}

func isBlank(text string) bool {
	return strings.TrimSpace(text) == ""
}

func rowHasColumnHeader(row []Cell) bool {
	for _, cell := range row {
		if cell.IsColumnHeader {
			return true
		}
	}
	return false
}

func isDataColumn(grid Grid, column, headerRows int) bool {
	hasHeader := false
	hasBodyData := false

	for rowIndex, row := range grid.Rows {
		if column >= len(row) {
			continue
		}
		cell := row[column]
		if cell.IsColumnHeader && !cell.IsRowHeader {
			hasHeader = true
		}
		if rowIndex >= headerRows && !isBlank(cell.Text) &&
			!cell.IsColumnHeader && !cell.IsRowHeader {
			hasBodyData = true
		}
	}

	// A column with a column header is always bindable, including one whose
	// header text is empty. A column without a header is still bindable when
	// it contains data, which is how a completely missing header is retained.
	// A row-label column has neither and is excluded.
	return hasHeader || hasBodyData
}

func headerRowCount(grid Grid, configured int) (int, error) {
	if configured < 0 {
		return 0, fmt.Errorf("%w: HeaderRowCount must not be negative", ErrInvalidConfig)
	}
	if configured > len(grid.Rows) {
		return 0, fmt.Errorf("%w: HeaderRowCount %d exceeds %d rows",
			ErrInvalidConfig, configured, len(grid.Rows))
	}
	if configured > 0 {
		return configured, nil
	}

	count := 0
	for count < len(grid.Rows) && rowHasColumnHeader(grid.Rows[count]) {
		count++
	}
	return count, nil
}

func selectDataColumns(grid Grid, headerRows int, configured []int) ([]int, error) {
	width := grid.Width()
	if len(configured) == 0 {
		columns := make([]int, 0, width)
		for column := 0; column < width; column++ {
			if isDataColumn(grid, column, headerRows) {
				columns = append(columns, column)
			}
		}
		return columns, nil
	}

	seen := make(map[int]bool, len(configured))
	columns := make([]int, 0, len(configured))
	for _, column := range configured {
		if column < 0 || column >= width {
			return nil, fmt.Errorf("%w: data column %d is outside [0,%d)",
				ErrInvalidConfig, column, width)
		}
		if seen[column] {
			return nil, fmt.Errorf("%w: data column %d is listed twice", ErrInvalidConfig, column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	return columns, nil
}
