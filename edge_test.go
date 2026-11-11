package tablegrid

import (
	"reflect"
	"testing"
)

// TestEmptyRows 空 tr 产生空网格行，后续行号顺延。
func TestEmptyRows(t *testing.T) {
	tnode := table(
		tr(),
		tr(td("a")),
		tr(),
	)
	assertNaiveMatch(t, tnode, "empty-rows")
	g := Build(tnode)
	if g.Rows != 3 || g.Cols != 1 {
		t.Fatalf("size = %dx%d, want 3x1", g.Rows, g.Cols)
	}
	if got := g.CellAt(1, 0); got == nil || got.Text != "a" || got.SourceRow != 1 {
		t.Fatalf("cell a = %+v", got)
	}
	if g.CellAt(0, 0) != nil || g.CellAt(2, 0) != nil {
		t.Fatal("empty rows must stay empty")
	}
}

// TestRowspanBeyondTable 末尾单元格 rowspan 超出表格时，网格长出额外空行。
func TestRowspanBeyondTable(t *testing.T) {
	tnode := table(
		tr(td("x", [2]string{"rowspan", "3"}), td("a")),
		tr(td("b")),
	)
	assertNaiveMatch(t, tnode, "rowspan-overflow")
	g := Build(tnode)
	if g.Rows != 3 {
		t.Fatalf("rows = %d, want 3", g.Rows)
	}
	x := g.CellAt(0, 0)
	if x.RowSpan != 3 {
		t.Fatalf("x rowspan = %d, want 3", x.RowSpan)
	}
	if got := g.CellAt(2, 1); got != nil {
		t.Fatalf("overflow slot should be empty, got %+v", got)
	}
}

// TestHeaderRowspan 跨行表头参与每一列的路径构造与去重。
func TestHeaderRowspan(t *testing.T) {
	tnode := table(
		thead(
			tr(
				th("Region", [2]string{"rowspan", "2"}),
				th("Q1", [2]string{"colspan", "2"}),
			),
			tr(th("Jan"), th("Feb")),
		),
		tbody(tr(td("us"), td("10"), td("20"))),
	)
	assertNaiveMatch(t, tnode, "header-rowspan")
	g := Build(tnode)

	want := [][]string{
		{"Region"},
		{"Q1", "Jan"},
		{"Q1", "Feb"},
	}
	for c, p := range want {
		if got := g.Columns[c].HeaderPath; !reflect.DeepEqual(got, p) {
			t.Fatalf("col %d path = %v, want %v", c, got, p)
		}
	}

	region := g.CellAt(0, 0)
	if region.RowSpan != 2 || !reflect.DeepEqual(region.ColPath, []string{"Region"}) {
		t.Fatalf("region header = %+v", region)
	}
}

// TestSourceCoordinates 验证 SourceRow/SourceCol 记录的是 DOM 顺序而非网格位置。
func TestSourceCoordinates(t *testing.T) {
	tnode := table(
		tr(td("a", [2]string{"rowspan", "2"}), td("b")),
		tr(td("c")),
	)
	g := Build(tnode)
	c := g.CellAt(1, 1)
	if c.Text != "c" || c.SourceRow != 1 || c.SourceCol != 0 {
		t.Fatalf("c source coords = %+v", c)
	}
	b := g.CellAt(0, 1)
	if b.Text != "b" || b.SourceRow != 0 || b.SourceCol != 1 || b.Col != 1 {
		t.Fatalf("b coords = %+v", b)
	}
}

// TestCellMatrixRoundTrip CellMatrix 与 CellAt 视图一致且可独立修改。
func TestCellMatrixRoundTrip(t *testing.T) {
	tnode := table(tr(td("a"), td("b")))
	g := Build(tnode)
	m := g.CellMatrix()
	if len(m) != 1 || len(m[0]) != 2 || m[0][0].Text != "a" {
		t.Fatalf("matrix = %+v", m)
	}
	m[0][0] = nil
	if g.CellAt(0, 0) == nil {
		t.Fatal("CellMatrix must return an independent copy")
	}
}
