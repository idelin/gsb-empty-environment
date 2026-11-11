package tablegrid

import (
	"reflect"
	"testing"
)

// TestSpecBasicTable 对应规范中带 caption 的基础表演算。
func TestSpecBasicTable(t *testing.T) {
	tnode := table(
		elem("caption", text("Example")),
		tr(td("1"), td("2"), td("3")),
		tr(td("4"), td("5"), td("6")),
	)
	assertNaiveMatch(t, tnode, "basic")
	g := Build(tnode)
	want := [][]string{{"1", "2", "3"}, {"4", "5", "6"}}
	if got := gridMatrix(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("basic grid = %v, want %v", got, want)
	}
	if len(g.Groups) != 1 || !g.Groups[0].Implicit || g.Groups[0].Tag != "tbody" {
		t.Fatalf("implicit tbody = %+v", g.Groups)
	}
	if g.Groups[0].Rows != 2 || g.Groups[0].StartRow != 0 {
		t.Fatalf("group geometry = %+v", g.Groups[0])
	}
}

// TestSpecSpanTable 对应规范 “colspan and rowspan attributes” 示例。
func TestSpecSpanTable(t *testing.T) {
	tnode := table(
		tr(td("1", [2]string{"colspan", "2"}), td("2"), td("3")),
		tr(td("4", [2]string{"rowspan", "2"}), td("5"), td("6")),
		tr(td("7"), td("8")),
	)
	assertNaiveMatch(t, tnode, "spans")
	g := Build(tnode)
	want := [][]string{
		{"1", "1", "2", "3"},
		{"4", "5", "6", ""},
		{"4", "7", "8", ""},
	}
	if got := gridMatrix(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("span grid = %v, want %v", got, want)
	}
	cell1 := g.CellAt(0, 0)
	if cell1.RowSpan != 1 || cell1.ColSpan != 2 || cell1.SourceRow != 0 || cell1.SourceCol != 0 {
		t.Fatalf("cell1 = %+v", cell1)
	}
	cell4 := g.CellAt(1, 0)
	if cell4.RowSpan != 2 || cell4.Col != 0 || cell4.SourceCol != 0 {
		t.Fatalf("cell4 = %+v", cell4)
	}
	cell7 := g.CellAt(2, 1)
	if cell7.SourceRow != 2 || cell7.SourceCol != 0 || cell7.Col != 1 {
		t.Fatalf("cell7 = %+v", cell7)
	}
}

// TestSpecOverlapConflict 对应规范中单元格与已占用槽位重叠、
// 触发表格模型插入新列（rewalk）的演算示例。
func TestSpecOverlapConflict(t *testing.T) {
	tnode := table(
		tr(td("1", [2]string{"rowspan", "2"}), td("2", [2]string{"colspan", "2"})),
		tr(td("3", [2]string{"colspan", "2"})),
	)
	assertNaiveMatch(t, tnode, "overlap")
	g := Build(tnode)
	want := [][]string{
		{"1", "2", "2"},
		{"1", "3", "3"},
	}
	if got := gridMatrix(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("overlap grid = %v, want %v", got, want)
	}
	cell2 := g.CellAt(0, 1)
	if cell2.Text != "2" || cell2.Col != 1 || cell2.ColSpan != 2 {
		t.Fatalf("cell2 = %+v", cell2)
	}
	cell3 := g.CellAt(1, 1)
	if cell3.Text != "3" || cell3.Col != 1 || cell3.ColSpan != 2 {
		t.Fatalf("cell3 = %+v", cell3)
	}
}

// TestRowSpanZero 验证 rowspan=0 只延伸到所在行组末尾，不跨组。
func TestRowSpanZero(t *testing.T) {
	tnode := table(
		thead(tr(th("H1"), th("H2", [2]string{"rowspan", "0"}))),
		tbody(
			tr(td("a")),
			tr(td("b")),
		),
		tfoot(tr(td("f1"), td("f2"))),
	)
	assertNaiveMatch(t, tnode, "rowspan0")
	g := Build(tnode)

	h2 := g.CellAt(0, 1)
	if h2.Text != "H2" || h2.RowSpan != 1 {
		t.Fatalf("thead rowspan=0 should end at thead boundary, got %+v", h2)
	}
	if got := g.CellAt(1, 1); got != nil {
		t.Fatalf("slot (1,1) should be empty, got %+v", got)
	}
	f2 := g.CellAt(3, 1)
	if f2 == nil || f2.Text != "f2" {
		t.Fatalf("tfoot cell = %+v", f2)
	}

	wantGroups := []RowGroup{
		{Tag: "thead", StartRow: 0, Rows: 1},
		{Tag: "tbody", StartRow: 1, Rows: 2},
		{Tag: "tfoot", StartRow: 3, Rows: 1},
	}
	if !reflect.DeepEqual(g.Groups, wantGroups) {
		t.Fatalf("groups = %+v, want %+v", g.Groups, wantGroups)
	}
}

// TestRowSpanZeroToGroupEnd 在 tbody 内部验证 rowspan=0 吃满剩余行。
func TestRowSpanZeroToGroupEnd(t *testing.T) {
	tnode := table(
		tbody(
			tr(td("x", [2]string{"rowspan", "0"}), td("a")),
			tr(td("b")),
			tr(td("c")),
		),
	)
	assertNaiveMatch(t, tnode, "rowspan0-tbody")
	g := Build(tnode)
	x := g.CellAt(0, 0)
	if x.RowSpan != 3 {
		t.Fatalf("rowspan0 = %d, want 3", x.RowSpan)
	}
	for r := 0; r < 3; r++ {
		if got := g.CellAt(r, 0); got != x {
			t.Fatalf("row %d col 0 not covered by x", r)
		}
	}
}

// TestClamping 验证非法跨度的回退与 1000 上限钳制。
func TestClamping(t *testing.T) {
	tnode := table(
		tr(
			td("bad", [2]string{"colspan", "-2"}),
			td("big", [2]string{"colspan", "99999"}),
		),
	)
	g := Build(tnode)
	bad := g.CellAt(0, 0)
	if bad.ColSpan != 1 {
		t.Fatalf("negative colspan = %d, want 1", bad.ColSpan)
	}
	big := g.CellAt(0, 1)
	if big.ColSpan != maxSpan {
		t.Fatalf("huge colspan = %d, want %d", big.ColSpan, maxSpan)
	}
	if g.Cols != maxSpan+1 {
		t.Fatalf("cols = %d, want %d", g.Cols, maxSpan+1)
	}

	tnode2 := table(tr(td("x", [2]string{"colspan", "abc"}, [2]string{"rowspan", ""})))
	g2 := Build(tnode2)
	x := g2.CellAt(0, 0)
	if x.ColSpan != 1 || x.RowSpan != 1 {
		t.Fatalf("non-numeric spans = %+v, want 1/1", x)
	}

	// rowspan=0 在单行行组中钳为 1 行。
	tnode3 := table(tr(td("z", [2]string{"rowspan", "0"})))
	g3 := Build(tnode3)
	if z := g3.CellAt(0, 0); z.RowSpan != 1 {
		t.Fatalf("single-row rowspan=0 = %d, want 1", z.RowSpan)
	}
}

// TestHeaderPaths 验证多行表头合并成列路径。
func TestHeaderPaths(t *testing.T) {
	tnode := table(
		thead(
			tr(
				th("A", [2]string{"colspan", "2"}),
				th("B", [2]string{"colspan", "2"}),
			),
			tr(th("A1"), th("A2"), th("B1"), th("B2")),
		),
		tbody(
			tr(td("d1"), td("d2"), td("d3"), td("d4")),
		),
	)
	assertNaiveMatch(t, tnode, "header-paths")
	g := Build(tnode)

	wantPaths := [][]string{
		{"A", "A1"},
		{"A", "A2"},
		{"B", "B1"},
		{"B", "B2"},
	}
	for c, want := range wantPaths {
		if got := g.Columns[c].HeaderPath; !reflect.DeepEqual(got, want) {
			t.Fatalf("col %d path = %v, want %v", c, got, want)
		}
	}

	d3 := g.CellAt(2, 2)
	if !reflect.DeepEqual(d3.ColPath, []string{"B", "B1"}) {
		t.Fatalf("data cell path = %v", d3.ColPath)
	}

	a := g.CellAt(0, 0)
	if !reflect.DeepEqual(a.ColPath, []string{"A"}) {
		t.Fatalf("top header path = %v", a.ColPath)
	}
	a2 := g.CellAt(1, 1)
	if !reflect.DeepEqual(a2.ColPath, []string{"A", "A2"}) {
		t.Fatalf("leaf header path = %v", a2.ColPath)
	}
}

// TestImplicitHeaderRows 没有 thead 时，前置全 th 连续行视为表头。
func TestImplicitHeaderRows(t *testing.T) {
	tnode := table(
		tr(th("H1"), th("H2")),
		tr(td("a"), td("b")),
	)
	g := Build(tnode)
	want := [][]string{{"H1"}, {"H2"}}
	for c, p := range want {
		if got := g.Columns[c].HeaderPath; !reflect.DeepEqual(got, p) {
			t.Fatalf("col %d implicit header = %v, want %v", c, got, p)
		}
	}
}

// TestNestedTable 嵌套表格不参与外层网格的行列计算。
func TestNestedTable(t *testing.T) {
	inner := table(tr(td("i1"), td("i2")))
	outer := table(
		tr(td("a"), elem("td", inner)),
		tr(td("b"), td("c")),
	)
	assertNaiveMatch(t, outer, "nested")
	g := Build(outer)
	if g.Rows != 2 || g.Cols != 2 {
		t.Fatalf("outer grid = %dx%d, want 2x2", g.Rows, g.Cols)
	}
}

// TestEmptyAndNonTable 覆盖空表格与非表格输入。
func TestEmptyAndNonTable(t *testing.T) {
	g := Build(table())
	if g.Rows != 0 || g.Cols != 0 || len(g.Cells) != 0 {
		t.Fatalf("empty table = %+v", g)
	}
	g = Build(elem("div"))
	if g.Rows != 0 || g.Cols != 0 {
		t.Fatalf("non-table = %+v", g)
	}
	g = Build(nil)
	if g == nil || g.Rows != 0 {
		t.Fatalf("nil input = %+v", g)
	}
}
