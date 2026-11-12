package taskpkg

import (
	"errors"
	"strings"
	"testing"
)

func testCell(text, id string, row, col, rowSpan, colSpan int, rowHeader, columnHeader bool) Cell {
	return Cell{
		Text: text,
		Source: CellRef{
			ID:      id,
			Row:     row,
			Col:     col,
			RowSpan: rowSpan,
			ColSpan: colSpan,
		},
		IsRowHeader:    rowHeader,
		IsColumnHeader: columnHeader,
	}
}

func repeatedCell(cell Cell, count int) []Cell {
	cells := make([]Cell, count)
	for i := range cells {
		cells[i] = cell
	}
	return cells
}

func TestBindNestedAnnualHeadersAndUnits(t *testing.T) {
	parent := testCell("营业收入（单位：万元）", "h-revenue", 0, 1, 1, 4, false, true)
	year2023 := testCell("2023年", "h-2023", 1, 1, 1, 2, false, true)
	year2024 := testCell("2024年", "h-2024", 1, 3, 1, 2, false, true)

	grid := Grid{Rows: [][]Cell{
		{
			testCell("项目", "corner", 0, 0, 1, 1, true, true),
			parent, parent, parent, parent,
		},
		{
			testCell("项目", "r1-label", 1, 0, 1, 1, true, false),
			year2023, year2023, year2024, year2024,
		},
		{
			testCell("项目", "r2-label", 2, 0, 1, 1, true, false),
			testCell("Q1", "h-q1-2023", 2, 1, 1, 1, false, true),
			testCell("Q2", "h-q2-2023", 2, 2, 1, 1, false, true),
			testCell("Q1", "h-q1-2024", 2, 3, 1, 1, false, true),
			testCell("Q2", "h-q2-2024", 2, 4, 1, 1, false, true),
		},
		{
			testCell("收入", "label-income", 3, 0, 1, 1, true, false),
			testCell("100", "d31", 3, 1, 1, 1, false, false),
			testCell("110", "d32", 3, 2, 1, 1, false, false),
			testCell("120", "d33", 3, 3, 1, 1, false, false),
			testCell("130", "d34", 3, 4, 1, 1, false, false),
		},
		{
			testCell("小计", "label-subtotal", 4, 0, 1, 1, true, false),
			testCell("210", "s41", 4, 1, 1, 1, false, false),
			testCell("220", "s42", 4, 2, 1, 1, false, false),
			testCell("250", "s43", 4, 3, 1, 1, false, false),
			testCell("260", "s44", 4, 4, 1, 1, false, false),
		},
		{
			testCell("支出", "label-expense", 5, 0, 1, 1, true, false),
			testCell("50", "e51", 5, 1, 1, 1, false, false),
			testCell("55", "e52", 5, 2, 1, 1, false, false),
			testCell("60", "e53", 5, 3, 1, 1, false, false),
			testCell("65", "e54", 5, 4, 1, 1, false, false),
		},
		{
			testCell("合计", "label-total", 6, 0, 1, 1, true, false),
			testCell("260", "t61", 6, 1, 1, 1, false, false),
			testCell("275", "t62", 6, 2, 1, 1, false, false),
			testCell("310", "t63", 6, 3, 1, 1, false, false),
			testCell("325", "t64", 6, 4, 1, 1, false, false),
		},
		{
			// A repeated header row in the middle of the table must be typed
			// as a header, but must not extend the top header path.
			testCell("项目", "repeat-corner", 7, 0, 1, 1, true, true),
			testCell("年度", "repeat-h1", 7, 1, 1, 1, false, true),
			testCell("季度", "repeat-h2", 7, 2, 1, 1, false, true),
			testCell("年度", "repeat-h3", 7, 3, 1, 1, false, true),
			testCell("季度", "repeat-h4", 7, 4, 1, 1, false, true),
		},
	}}

	config := BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: parent.Source, Role: HeaderRoleUnit},
			{Source: year2023.Source, Role: HeaderRoleYear},
			{Source: year2024.Source, Role: HeaderRoleYear},
		},
		RowRules: []RowRule{
			{Kind: RowKindSubtotal, LabelContains: []string{"小计", "subtotal"}},
			{Kind: RowKindTotal, LabelContains: []string{"合计", "总计", "total"}},
		},
	}

	table, err := Bind(grid, config)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if table.HeaderRows != 3 {
		t.Fatalf("HeaderRows = %d, want 3", table.HeaderRows)
	}
	if len(table.Columns) != 4 {
		t.Fatalf("got %d columns, want 4: %#v", len(table.Columns), table.Columns)
	}

	wantPaths := []string{
		"营业收入（单位：万元） / 2023年 / Q1",
		"营业收入（单位：万元） / 2023年 / Q2",
		"营业收入（单位：万元） / 2024年 / Q1",
		"营业收入（单位：万元） / 2024年 / Q2",
	}
	wantYears := []string{"2023", "2023", "2024", "2024"}

	for i, binding := range table.Columns {
		if binding.Column != i+1 {
			t.Errorf("column %d bound to index %d", i+1, binding.Column)
		}
		if binding.PathText != wantPaths[i] {
			t.Errorf("column %d path = %q, want %q", binding.Column, binding.PathText, wantPaths[i])
		}
		if binding.Year != wantYears[i] {
			t.Errorf("column %d year = %q, want %q", binding.Column, binding.Year, wantYears[i])
		}
		if binding.YearNode == nil {
			t.Errorf("column %d has no year provenance", binding.Column)
		} else {
			wantYearSource := "h-2023"
			if i >= 2 {
				wantYearSource = "h-2024"
			}
			if binding.YearNode.Source.ID != wantYearSource {
				t.Errorf("column %d year source = %q, want %q",
					binding.Column, binding.YearNode.Source.ID, wantYearSource)
			}
		}
		if binding.Unit != "万元" {
			t.Errorf("column %d unit = %q, want 万元", binding.Column, binding.Unit)
		}
		if binding.UnitNode == nil || binding.UnitNode.Source.ColSpan != 4 {
			t.Errorf("column %d unit provenance does not retain the colspan: %#v",
				binding.Column, binding.UnitNode)
		}
		if len(binding.Path) != 3 {
			t.Fatalf("column %d path has %d nodes, want 3", binding.Column, len(binding.Path))
		}
		if len(binding.HeaderSources) != len(binding.Path) {
			t.Errorf("column %d has %d header sources for %d path nodes",
				binding.Column, len(binding.HeaderSources), len(binding.Path))
		}
		if binding.HeaderSources[0].ID != "h-revenue" {
			t.Errorf("column %d header sources = %#v", binding.Column, binding.HeaderSources)
		}
		if binding.Path[0].Source.ID != "h-revenue" {
			t.Errorf("column %d lost parent provenance: %#v", binding.Column, binding.Path[0])
		}
		if !strings.Contains(binding.Identity, "col:") || !strings.Contains(binding.Identity, "@") {
			t.Errorf("column %d has bad identity %q", binding.Column, binding.Identity)
		}
	}

	// The repeated in-body header row must not become an extra path level.
	for _, binding := range table.Columns {
		for _, node := range binding.Path {
			if node.Level >= table.HeaderRows {
				t.Errorf("column %d path contains body header row %d", binding.Column, node.Level)
			}
		}
	}

	if len(table.Rows) != 5 {
		t.Fatalf("got %d body rows, want 5", len(table.Rows))
	}
	wantKinds := []RowKind{
		RowKindData,
		RowKindSubtotal,
		RowKindData,
		RowKindTotal,
		RowKindHeader,
	}
	for i, row := range table.Rows {
		if row.Kind != wantKinds[i] {
			t.Errorf("body row %d kind = %q, want %q", row.Index, row.Kind, wantKinds[i])
		}
	}

	subtotal := table.Rows[1]
	if len(subtotal.Cells) != 4 {
		t.Fatalf("subtotal row has %d bound cells, want 4", len(subtotal.Cells))
	}
	for i, cell := range subtotal.Cells {
		if cell.Column != i+1 {
			t.Errorf("subtotal cell %d bound to column %d", i, cell.Column)
		}
		if cell.ColumnIdentity != table.Columns[i].Identity {
			t.Errorf("subtotal cell %d has identity %q, want %q",
				i, cell.ColumnIdentity, table.Columns[i].Identity)
		}
	}
	if subtotal.Cells[0].Source.ID != "s41" || subtotal.Cells[0].Text != "210" {
		t.Errorf("subtotal first cell provenance = %#v", subtotal.Cells[0])
	}
	if subtotal.LabelSource == nil || subtotal.LabelSource.ID != "label-subtotal" {
		t.Errorf("subtotal label source = %#v", subtotal.LabelSource)
	}
	if len(subtotal.Sources) != 5 {
		t.Errorf("subtotal row has %d unique sources, want 5", len(subtotal.Sources))
	}
}

func TestBindKeepsDuplicatePathsSeparateWithProvenance(t *testing.T) {
	leftGroup := testCell("收入", "group-left", 0, 1, 1, 2, false, true)
	rightGroup := testCell("收入", "group-right", 0, 3, 1, 2, false, true)

	grid := Grid{Rows: [][]Cell{
		{
			testCell("项目", "corner", 0, 0, 1, 1, true, true),
			leftGroup, leftGroup, rightGroup, rightGroup,
		},
		{
			testCell("项目", "label", 1, 0, 1, 1, true, false),
			testCell("2023年", "year-left-a", 1, 1, 1, 1, false, true),
			testCell("2023年", "year-left-b", 1, 2, 1, 1, false, true),
			testCell("2023年", "year-right-a", 1, 3, 1, 1, false, true),
			testCell("2023年", "year-right-b", 1, 4, 1, 1, false, true),
		},
		{
			testCell("数值", "label-value", 2, 0, 1, 1, true, false),
			testCell("1", "v1", 2, 1, 1, 1, false, false),
			testCell("2", "v2", 2, 2, 1, 1, false, false),
			testCell("3", "v3", 2, 3, 1, 1, false, false),
			testCell("4", "v4", 2, 4, 1, 1, false, false),
		},
	}}

	config := BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: CellRef{Row: 1, Col: 1}, Role: HeaderRoleYear},
			{Source: CellRef{Row: 1, Col: 2}, Role: HeaderRoleYear},
			{Source: CellRef{Row: 1, Col: 3}, Role: HeaderRoleYear},
			{Source: CellRef{Row: 1, Col: 4}, Role: HeaderRoleYear},
		},
	}

	table, err := Bind(grid, config)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(table.Columns) != 4 {
		t.Fatalf("got %d column bindings, want 4 separate bindings", len(table.Columns))
	}

	identities := make(map[string]bool)
	sourceKeys := make(map[string]bool)
	for _, binding := range table.Columns {
		if binding.PathText != "收入 / 2023年" {
			t.Errorf("column %d path = %q", binding.Column, binding.PathText)
		}
		if identities[binding.Identity] {
			t.Errorf("duplicate identity %q", binding.Identity)
		}
		identities[binding.Identity] = true
		if sourceKeys[binding.SourceKey] {
			t.Errorf("duplicate source key %q", binding.SourceKey)
		}
		sourceKeys[binding.SourceKey] = true
		if binding.Year != "2023" {
			t.Errorf("column %d year = %q", binding.Column, binding.Year)
		}
		if !strings.Contains(binding.QualifiedPath(), binding.Path[0].Source.ID) {
			t.Errorf("qualified path lacks group provenance: %q", binding.QualifiedPath())
		}
	}

	// Adjacent columns under the same group still differ by year-cell source.
	if table.Columns[0].SourceKey == table.Columns[1].SourceKey {
		t.Error("columns sharing a group were incorrectly merged or unqualified")
	}
	if table.Columns[2].QualifiedPath() == table.Columns[3].QualifiedPath() {
		t.Error("distinct year cells were not retained in the qualified path")
	}
}

func TestBindMissingHeaderDoesNotBorrowNeighbourValues(t *testing.T) {
	parent := testCell("销售", "h-section", 0, 1, 1, 2, false, true)
	grid := Grid{Rows: [][]Cell{
		{
			testCell("指标", "corner", 0, 0, 1, 1, true, true),
			parent, parent,
		},
		{
			testCell("指标", "label", 1, 0, 1, 1, true, false),
			testCell("2023年", "h-year", 1, 1, 1, 1, false, true),
			// The header cell exists but its text is missing. It must not
			// inherit the 2023 label from the column on its left.
			testCell("", "h-missing", 1, 2, 1, 1, false, true),
		},
		{
			testCell("金额", "row-label", 2, 0, 1, 1, true, false),
			testCell("10", "d21", 2, 1, 1, 1, false, false),
			testCell("20", "d22", 2, 2, 1, 1, false, false),
		},
	}}

	config := BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: CellRef{Row: 1, Col: 1}, Role: HeaderRoleYear},
		},
	}
	table, err := Bind(grid, config)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(table.Columns) != 2 {
		t.Fatalf("got %d columns, want 2", len(table.Columns))
	}

	first, second := table.Columns[0], table.Columns[1]
	if first.PathText != "销售 / 2023年" || first.Year != "2023" {
		t.Errorf("first column = %q year %q", first.PathText, first.Year)
	}
	if second.PathText != "销售" {
		t.Errorf("missing-header column path = %q, want parent only", second.PathText)
	}
	if second.Year != "" || second.YearNode != nil {
		t.Errorf("missing-header column borrowed year %q from %#v", second.Year, second.YearNode)
	}
	if len(second.Path) != 1 || second.Path[0].Source.ID != "h-section" {
		t.Errorf("missing-header column parent provenance = %#v", second.Path)
	}

	if len(table.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(table.Rows))
	}
	row := table.Rows[0]
	if len(row.Cells) != 2 {
		t.Fatalf("bound cells = %d, want 2", len(row.Cells))
	}
	if row.Cells[0].Source.ID != "d21" || row.Cells[1].Source.ID != "d22" {
		t.Errorf("row cell provenance = %#v", row.Cells)
	}
	if len(row.Sources) != 3 {
		t.Errorf("row sources = %#v, want label plus two data cells", row.Sources)
	}
	if row.LabelSource == nil || row.LabelSource.ID != "row-label" {
		t.Errorf("label source = %#v", row.LabelSource)
	}
}

func TestParseYearAndUnit(t *testing.T) {
	yearCases := []struct {
		text string
		want string
		ok   bool
	}{
		{"2024年", "2024", true},
		{"FY2023", "2023", true},
		{"2020-2021", "2020", true},
		{"年度", "", false},
	}
	for _, test := range yearCases {
		got, ok := ParseYear(test.text)
		if got != test.want || ok != test.ok {
			t.Errorf("ParseYear(%q) = %q,%v; want %q,%v", test.text, got, ok, test.want, test.ok)
		}
	}

	unitCases := []struct {
		text string
		want string
		ok   bool
	}{
		{"单位：万元", "万元", true},
		{"单位(亿元)", "亿元", true},
		{"（%）", "%", true},
		{"营业收入（万美元）", "万美元", true},
		{"万元", "万元", true},
		{"   ", "", false},
	}
	for _, test := range unitCases {
		got, ok := ParseUnit(test.text)
		if got != test.want || ok != test.ok {
			t.Errorf("ParseUnit(%q) = %q,%v; want %q,%v", test.text, got, ok, test.want, test.ok)
		}
	}
}

func TestBindExplicitValueAndCustomRowRule(t *testing.T) {
	header := testCell("年度", "h-year", 0, 1, 1, 2, false, true)
	grid := Grid{Rows: [][]Cell{
		{
			testCell("指标", "corner", 0, 0, 1, 1, true, true),
			header, header,
		},
		{
			testCell("A", "label-a", 1, 0, 1, 1, true, false),
			testCell("1", "a1", 1, 1, 1, 1, false, false),
			testCell("2", "a2", 1, 2, 1, 1, false, false),
		},
		{
			testCell("分组小计", "label-sub", 2, 0, 1, 1, true, false),
			testCell("3", "s1", 2, 1, 1, 1, false, false),
			testCell("4", "s2", 2, 2, 1, 1, false, false),
		},
	}}

	config := BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: header.Source, Role: HeaderRoleYear, Value: "2025"},
			{Source: header.Source, Role: HeaderRoleUnit, Value: "万元"},
		},
		RowRules: []RowRule{
			{
				Kind: RowKindSubtotal,
				Predicate: func(context RowContext) bool {
					return context.Index == 2 && strings.Contains(context.Label, "小计")
				},
			},
		},
	}
	table, err := Bind(grid, config)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for _, binding := range table.Columns {
		if binding.Year != "2025" || binding.Unit != "万元" {
			t.Errorf("column %d explicit values = year %q unit %q",
				binding.Column, binding.Year, binding.Unit)
		}
	}
	if table.Rows[1].Kind != RowKindSubtotal {
		t.Errorf("custom-rule row kind = %q", table.Rows[1].Kind)
	}
}

func TestBindRejectsUnparsableConfiguredYear(t *testing.T) {
	header := testCell("年度", "h-year", 0, 1, 1, 1, false, true)
	grid := Grid{Rows: [][]Cell{
		{
			testCell("指标", "corner", 0, 0, 1, 1, true, true),
			header,
		},
		{
			testCell("A", "label", 1, 0, 1, 1, true, false),
			testCell("1", "a1", 1, 1, 1, 1, false, false),
		},
	}}
	_, err := Bind(grid, BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: header.Source, Role: HeaderRoleYear},
		},
	})
	if !errors.Is(err, ErrUnparsableHeader) {
		t.Fatalf("Bind error = %v, want ErrUnparsableHeader", err)
	}
}

func TestDeepestConfiguredYearWins(t *testing.T) {
	generic := testCell("年度", "h-generic", 0, 1, 1, 1, false, true)
	year := testCell("2024年", "h-2024", 1, 1, 1, 1, false, true)
	grid := Grid{Rows: [][]Cell{
		{
			testCell("指标", "corner", 0, 0, 1, 1, true, true),
			generic,
		},
		{
			testCell("指标", "label", 1, 0, 1, 1, true, false),
			year,
		},
		{
			testCell("A", "row-a", 2, 0, 1, 1, true, false),
			testCell("1", "a1", 2, 1, 1, 1, false, false),
		},
	}}

	table, err := Bind(grid, BindConfig{
		HeaderSemantics: []HeaderSemantic{
			{Source: generic.Source, Role: HeaderRoleYear},
			{Source: year.Source, Role: HeaderRoleYear},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if table.Columns[0].Year != "2024" {
		t.Errorf("year = %q, want deepest 2024", table.Columns[0].Year)
	}
	if table.Columns[0].YearNode == nil || table.Columns[0].YearNode.Source.ID != "h-2024" {
		t.Errorf("year provenance = %#v, want the deeper node", table.Columns[0].YearNode)
	}
}

func TestBindHonoursConfiguredDataColumnsAndHeaderRowCount(t *testing.T) {
	grid := Grid{Rows: [][]Cell{
		{
			testCell("", "no-c1", 0, 0, 1, 1, false, false),
			testCell("Q1", "h-q1", 0, 1, 1, 1, false, true),
			testCell("Q2", "h-q2", 0, 2, 1, 1, false, true),
		},
		{
			testCell("A", "label", 1, 0, 1, 1, true, false),
			testCell("1", "a1", 1, 1, 1, 1, false, false),
			testCell("2", "a2", 1, 2, 1, 1, false, false),
		},
	}}
	table, err := Bind(grid, BindConfig{
		HeaderRowCount: 1,
		DataColumns:    []int{0, 2},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(table.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(table.Columns))
	}
	if table.Columns[0].Column != 0 || table.Columns[0].PathText != "" {
		t.Errorf("explicit missing-header column = %#v", table.Columns[0])
	}
	if table.Columns[1].Column != 2 || table.Columns[1].PathText != "Q2" {
		t.Errorf("explicit Q2 column = %#v", table.Columns[1])
	}
}

func TestBindRejectsRaggedGrid(t *testing.T) {
	grid := Grid{Rows: [][]Cell{
		{testCell("A", "a", 0, 0, 1, 1, false, true)},
		{
			testCell("B", "b", 0, 0, 1, 1, false, false),
			testCell("C", "c", 0, 1, 1, 1, false, false),
		},
	}}
	if _, err := Bind(grid, BindConfig{}); !errors.Is(err, ErrInvalidGrid) {
		t.Fatalf("Bind error = %v, want ErrInvalidGrid", err)
	}
}
