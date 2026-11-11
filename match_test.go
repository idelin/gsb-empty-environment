package tablegrid

import (
	"reflect"
	"testing"
)

func gridMatrix(g *Grid) [][]string {
	out := make([][]string, g.Rows)
	for r := range out {
		out[r] = make([]string, g.Cols)
		for c := 0; c < g.Cols; c++ {
			if cell := g.CellAt(r, c); cell != nil {
				out[r][c] = cell.Text
			}
		}
	}
	return out
}

func naiveMatrix(m *naiveModel) [][]string {
	out := make([][]string, len(m.slots))
	for r := range out {
		out[r] = make([]string, m.cols)
		for c := 0; c < m.cols; c++ {
			if cell := m.slots[r][c]; cell != nil {
				out[r][c] = cell.text
			}
		}
	}
	return out
}

// assertNaiveMatch 对照朴素模型验证网格尺寸、槽位内容、单元格几何与来源行列。
func assertNaiveMatch(t *testing.T, tnode Node, label string) {
	t.Helper()
	g := Build(tnode)
	m := runNaiveModel(tnode)

	if got, want := g.Rows, len(m.slots); got != want {
		t.Fatalf("%s: rows = %d, want %d", label, got, want)
	}
	if got, want := g.Cols, m.cols; got != want {
		t.Fatalf("%s: cols = %d, want %d", label, got, want)
	}
	if got, want := gridMatrix(g), naiveMatrix(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: grid mismatch\n got %v\nwant %v", label, got, want)
	}
	if got, want := len(g.Cells), len(m.cells); got != want {
		t.Fatalf("%s: cell count = %d, want %d", label, got, want)
	}

	for r := 0; r < g.Rows; r++ {
		for c := 0; c < g.Cols; c++ {
			cell := g.CellAt(r, c)
			ref := m.slots[r][c]
			if (cell == nil) != (ref == nil) {
				t.Fatalf("%s: slot nil-ness mismatch at (%d,%d)", label, r, c)
			}
			if cell == nil {
				continue
			}
			if cell.Text != ref.text || cell.IsHeader != ref.header {
				t.Fatalf("%s: content mismatch at (%d,%d): %+v vs %+v", label, r, c, cell, ref)
			}
			if cell.Row != ref.row || cell.Col != ref.col ||
				cell.RowSpan != ref.rowSpan || cell.ColSpan != ref.colSpan ||
				cell.SourceRow != ref.sourceRow || cell.SourceCol != ref.sourceCol {
				t.Fatalf("%s: geometry mismatch at (%d,%d): %+v vs %+v", label, r, c, cell, ref)
			}
			if !cell.Cover(cell.Row, cell.Col) ||
				!cell.Cover(cell.Row+cell.RowSpan-1, cell.Col+cell.ColSpan-1) {
				t.Fatalf("%s: cover check failed for %+v", label, cell)
			}
		}
	}

	// 每个单元格在网格中占据的槽位数必须等于 rowSpan*colSpan。
	counts := make(map[*Cell]int)
	for r := 0; r < g.Rows; r++ {
		for c := 0; c < g.Cols; c++ {
			if cell := g.CellAt(r, c); cell != nil {
				counts[cell]++
			}
		}
	}
	for i := range g.Cells {
		cell := &g.Cells[i]
		if got, want := counts[cell], cell.RowSpan*cell.ColSpan; got != want {
			t.Fatalf("%s: cell %q covers %d slots, want %d", label, cell.Text, got, want)
		}
	}
}
