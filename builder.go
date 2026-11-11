package tablegrid

import "slices"

// Build 按 HTML 表格模型（table model）展开节点树 table。
//
// 支持：
//   - colspan、rowspan 以及 rowspan=0（延伸到所在行组末尾）；
//   - thead/tbody/tfoot 行组，以及行组外裸 <tr> 的隐式 tbody；
//   - 单元格与已占用槽位重叠时按规范插入新列并重走放置流程；
//   - 非法或超限跨度按规范回退与钳制（非正回退为 1，上限 1000）；
//   - 多行 <th> 表头合并为每列的列路径，并记录每个单元格的来源行列。
//
// 非表格元素按不存在处理；嵌套表格作为外层单元格的普通内容，
// 不参与外层网格的行列计算。
func Build(table Node) *Grid {
	b := &builder{
		rowOf: make(map[Node]int),
		index: make(map[Node]*Cell),
	}

	// 第一遍：建立行组结构，统计行数与每个 <tr> 的全局行号。
	b.scanGroups(table)

	// 第二遍：按表格模型放置单元格。
	b.placeCells(table)

	g := &Grid{
		Rows:   len(b.slots),
		Cols:   b.cols,
		Groups: b.groupOrder,
		slots:  b.slots,
	}
	g.Cells = make([]Cell, len(b.cells))
	for i, cell := range b.cells {
		g.Cells[i] = *cell
	}
	// b.slots 中的指针指向 builder 的内部单元格；重建为指向 g.Cells 的最终记录，
	// 保证 CellAt / CellMatrix 返回的指针与 Cells 切片中的元素身份一致。
	g.slots = make([][]*Cell, len(b.slots))
	identity := make(map[*Cell]*Cell, len(b.cells))
	for i := range b.cells {
		identity[b.cells[i]] = &g.Cells[i]
	}
	for r := range b.slots {
		g.slots[r] = make([]*Cell, b.cols)
		for c := range b.slots[r] {
			if cell := b.slots[r][c]; cell != nil {
				g.slots[r][c] = identity[cell]
			}
		}
	}
	g.Columns = b.buildColumns()
	b.assignCellPaths(g)
	return g
}

type builder struct {
	slots      [][]*Cell
	cols       int
	cells      []*Cell
	index      map[Node]*Cell
	groupOrder []RowGroup
	rowOf      map[Node]int
}

// scanGroups 识别显式 thead/tbody/tfoot 与裸 tr 的隐式 tbody，
// 同时记录每个 tr 的全局行号和各行组覆盖的行区间。
func (b *builder) scanGroups(table Node) {
	if table == nil || table.Tag() != "table" {
		return
	}
	rowIndex := 0
	var implicit *RowGroup

	for i := 0; i < table.NumChildren(); i++ {
		child := table.Child(i)
		if child == nil {
			continue
		}
		switch child.Tag() {
		case "thead", "tbody", "tfoot":
			implicit = nil
			rg := RowGroup{Tag: child.Tag(), StartRow: rowIndex}
			rg.Rows = countRows(child)
			forEachRow(child, func(tr Node) {
				b.rowOf[tr] = rowIndex
				rowIndex++
			})
			b.groupOrder = append(b.groupOrder, rg)
		case "tr":
			if implicit == nil {
				implicit = &RowGroup{Tag: "tbody", Implicit: true, StartRow: rowIndex}
				b.groupOrder = append(b.groupOrder, *implicit)
			}
			b.rowOf[child] = rowIndex
			rowIndex++
			implicit.Rows++
			b.groupOrder[len(b.groupOrder)-1] = *implicit
		default:
			// caption、colgroup、col、脚本节点等不产生行。
		}
	}
}

// groupOfRow 返回全局行号 r 所属的行组，找不到时返回 nil。
func (b *builder) groupOfRow(r int) *RowGroup {
	for i := range b.groupOrder {
		rg := &b.groupOrder[i]
		if r >= rg.StartRow && r < rg.StartRow+rg.Rows {
			return rg
		}
	}
	return nil
}

type rowCell struct {
	tr        Node
	cell      Node
	sourceCol int
}

func (b *builder) placeCells(table Node) {
	if table == nil || table.Tag() != "table" {
		return
	}

	var items []rowCell
	walkRows(table, func(tr Node, sourceCol int, cell Node) {
		items = append(items, rowCell{tr: tr, cell: cell, sourceCol: sourceCol})
	})
	// 网格至少包含全部真实 <tr> 行，空 tr 也占据一行；
	// 超大 rowspan 可能让网格继续向下长出额外行。
	totalRows := 0
	for _, r := range b.rowOf {
		if r+1 > totalRows {
			totalRows = r + 1
		}
	}
	b.ensureSize(totalRows, 0)

	for _, it := range items {
		row := b.rowOf[it.tr]
		colspan := clampSpan(AttrInt(it.cell, "colspan", 1))
		rowspan := clampSpan(AttrInt(it.cell, "rowspan", 1))

		if raw, ok := it.cell.Attr("rowspan"); ok {
			if v, ok2 := attrLeadingInt(raw); ok2 && v == 0 {
				// rowspan=0：从当前行延伸到所在行组的最后一行。
				if group := b.groupOfRow(row); group != nil {
					rowspan = group.StartRow + group.Rows - row
				}
				if rowspan < 1 {
					rowspan = 1
				}
			}
		}

		cell := &Cell{
			Text:      TextOf(it.cell),
			IsHeader:  it.cell.Tag() == "th",
			Row:       row,
			RowSpan:   rowspan,
			ColSpan:   colspan,
			SourceRow: row,
			SourceCol: it.sourceCol,
		}
		b.cells = append(b.cells, cell)
		b.index[it.cell] = cell

		for {
			b.ensureSize(row+rowspan, 0)
			col := b.nextFreeSlot(row, colspan)
			cell.Col = col
			b.ensureSize(0, col+colspan)

			if b.rectFree(row, col, rowspan, colspan) {
				b.fillRect(cell, row, col, rowspan, colspan)
				break
			}

			// 重叠冲突：规范要求在当前列前插入新列，再重走全部单元格的放置。
			b.growColumnAt(col)
			b.rewalk(items)
		}
	}
}

// rewalk 清空槽位后按文档顺序重新放置全部已生成的单元格。
func (b *builder) rewalk(items []rowCell) {
	b.slots = make([][]*Cell, len(b.slots))
	for r := range b.slots {
		b.slots[r] = make([]*Cell, b.cols)
	}
	for _, it := range items {
		cell, ok := b.index[it.cell]
		if !ok {
			continue
		}
		row := b.rowOf[it.tr]
		for {
			b.ensureSize(row+cell.RowSpan, 0)
			col := b.nextFreeSlot(row, cell.ColSpan)
			cell.Col = col
			b.ensureSize(0, col+cell.ColSpan)
			if b.rectFree(row, col, cell.RowSpan, cell.ColSpan) {
				b.fillRect(cell, row, col, cell.RowSpan, cell.ColSpan)
				break
			}
			// 重走期间仍可能发现新的重叠：继续插列并重走，直到放置成功。
			b.growColumnAt(col)
			b.rewalk(items)
			return
		}
	}
}

func (b *builder) ensureSize(rows, cols int) {
	for len(b.slots) < rows {
		b.slots = append(b.slots, make([]*Cell, b.cols))
	}
	if cols <= b.cols {
		return
	}
	for r := range b.slots {
		b.slots[r] = append(b.slots[r], make([]*Cell, cols-b.cols)...)
	}
	b.cols = cols
}

// nextFreeSlot 返回行 r 上能容纳 width 个连续空槽位的最小起始列。
func (b *builder) nextFreeSlot(r, width int) int {
	row := b.slots[r]
	for c := 0; c+width <= len(row); c++ {
		free := true
		for k := 0; k < width; k++ {
			if row[c+k] != nil {
				free = false
				break
			}
		}
		if free {
			return c
		}
	}
	return len(row)
}

func (b *builder) rectFree(r, c, height, width int) bool {
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			if b.slots[r+i][c+j] != nil {
				return false
			}
		}
	}
	return true
}

func (b *builder) fillRect(cell *Cell, r, c, height, width int) {
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			b.slots[r+i][c+j] = cell
		}
	}
}

// growColumnAt 在列 c 前插入一个空列，并同步调整已放置单元格的坐标与跨度。
func (b *builder) growColumnAt(c int) {
	for r := range b.slots {
		b.slots[r] = slices.Insert(b.slots[r], c, nil)
	}
	b.cols++

	seen := make(map[*Cell]bool)
	for _, cell := range b.cells {
		if seen[cell] {
			continue
		}
		seen[cell] = true
		switch {
		case cell.Col >= c:
			cell.Col++
		case cell.Col+cell.ColSpan > c:
			cell.ColSpan++
		}
	}
}

func clampSpan(v int) int {
	if v < 1 {
		return 1
	}
	if v > maxSpan {
		return maxSpan
	}
	return v
}

// walkRows 按文档顺序枚举表格中的全部 (tr, 单元格)，
// sourceCol 为单元格在所属 tr 内 td/th 序列中的序号。
// 嵌套表格中的行不会被当作外层表格的行。
func walkRows(table Node, fn func(tr Node, sourceCol int, cell Node)) {
	for i := 0; i < table.NumChildren(); i++ {
		child := table.Child(i)
		if child == nil {
			continue
		}
		switch child.Tag() {
		case "thead", "tbody", "tfoot":
			walkGroupRows(child, fn)
		case "tr":
			emitRowCells(child, fn)
		}
	}
}

func walkGroupRows(group Node, fn func(tr Node, sourceCol int, cell Node)) {
	for i := 0; i < group.NumChildren(); i++ {
		child := group.Child(i)
		if child != nil && child.Tag() == "tr" {
			emitRowCells(child, fn)
		}
	}
}

func emitRowCells(tr Node, fn func(tr Node, sourceCol int, cell Node)) {
	sourceCol := 0
	for j := 0; j < tr.NumChildren(); j++ {
		node := tr.Child(j)
		if node == nil || (node.Tag() != "td" && node.Tag() != "th") {
			continue
		}
		fn(tr, sourceCol, node)
		sourceCol++
	}
}

// countRows 统计行组内直接包含的 tr 数量（不进入嵌套表格）。
func countRows(group Node) int {
	n := 0
	forEachRow(group, func(Node) { n++ })
	return n
}

func forEachRow(group Node, fn func(Node)) {
	for i := 0; i < group.NumChildren(); i++ {
		child := group.Child(i)
		if child != nil && child.Tag() == "tr" {
			fn(child)
		}
	}
}
