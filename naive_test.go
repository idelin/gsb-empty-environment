package tablegrid

// naiveCell 是朴素模型中的单元格记录，字段语义与 Cell 一一对应。
type naiveCell struct {
	text                 string
	header               bool
	row, col             int
	rowSpan, colSpan     int
	sourceRow, sourceCol int
	node                 Node
}

type naiveModel struct {
	slots    [][]*naiveCell
	cols     int
	cells    []*naiveCell
	rowOf    map[Node]int
	items    []rowCell
	groups   []RowGroup
	zeroRows map[*naiveCell]int
}

// runNaiveModel 完全按 HTML 规范的表格模型步骤逐步实现：
// 逐行处理、逐格找槽、rowspan=0 的单元格在“后续行”算法中逐行延伸、
// 冲突时插入新列并从头重走（rewalk）全部单元格。
func runNaiveModel(t Node) *naiveModel {
	m := &naiveModel{
		rowOf:    map[Node]int{},
		zeroRows: map[*naiveCell]int{},
	}

	// 与生产实现共用行/行组枚举，保证输入视图一致。
	rowIndex := 0
	var implicit *RowGroup
	for i := 0; i < t.NumChildren(); i++ {
		child := t.Child(i)
		if child == nil {
			continue
		}
		switch child.Tag() {
		case "thead", "tbody", "tfoot":
			implicit = nil
			rg := RowGroup{Tag: child.Tag(), StartRow: rowIndex, Rows: countRows(child)}
			forEachRow(child, func(r Node) {
				m.rowOf[r] = rowIndex
				rowIndex++
			})
			m.groups = append(m.groups, rg)
		case "tr":
			if implicit == nil {
				implicit = &RowGroup{Tag: "tbody", Implicit: true, StartRow: rowIndex}
				m.groups = append(m.groups, *implicit)
			}
			m.rowOf[child] = rowIndex
			rowIndex++
			implicit.Rows++
			m.groups[len(m.groups)-1] = *implicit
		}
	}

	walkRows(t, func(tr Node, sourceCol int, cell Node) {
		m.items = append(m.items, rowCell{tr: tr, cell: cell, sourceCol: sourceCol})
	})

	// 逐行（规范的 “processing a row”）处理。
	maxRow := -1
	for _, r := range m.rowOf {
		if r > maxRow {
			maxRow = r
		}
	}
	rowItems := make([][]rowCell, maxRow+1)
	for _, it := range m.items {
		r := m.rowOf[it.tr]
		rowItems[r] = append(rowItems[r], it)
	}
	m.ensureRows(len(rowItems))

	for r := 0; r < len(rowItems); r++ {
		m.ensureRows(r + 1)
		// 规范“后续行”第一步：让 rowspan=0 的活动单元格向下延伸一行。
		m.growZeroCells(r)
		for _, it := range rowItems[r] {
			m.placeCell(r, it)
		}
	}

	// 行组结束时 rowspan=0 不再延伸，跨度已在 growZeroCells 中累计。
	return m
}

func (m *naiveModel) ensureRows(rows int) {
	for len(m.slots) < rows {
		m.slots = append(m.slots, make([]*naiveCell, m.cols))
	}
}

func (m *naiveModel) ensureCols(cols int) {
	if cols <= m.cols {
		return
	}
	for r := range m.slots {
		m.slots[r] = append(m.slots[r], make([]*naiveCell, cols-m.cols)...)
	}
	m.cols = cols
}

// growZeroCells 对应规范后续行算法的第 1、2 步：
// 若行 r 属于 rowspan=0 单元格所在的行组，则该单元格向新行延伸。
func (m *naiveModel) growZeroCells(r int) {
	for cell, startRow := range m.zeroRows {
		group := m.groupOfRow(startRow)
		if group == nil || r < group.StartRow || r >= group.StartRow+group.Rows {
			continue
		}
		for c := cell.col; c < cell.col+cell.colSpan; c++ {
			if m.slots[r][c] == nil {
				m.slots[r][c] = cell
			}
		}
		cell.rowSpan++
	}
}

func (m *naiveModel) groupOfRow(r int) *RowGroup {
	for i := range m.groups {
		rg := &m.groups[i]
		if r >= rg.StartRow && r < rg.StartRow+rg.Rows {
			return rg
		}
	}
	return nil
}

func (m *naiveModel) placeCell(row int, it rowCell) {
	node := it.cell
	colspan := clampSpan(AttrInt(node, "colspan", 1))
	rowspan := clampSpan(AttrInt(node, "rowspan", 1))
	zero := false
	if raw, ok := node.Attr("rowspan"); ok {
		if v, ok2 := attrLeadingInt(raw); ok2 && v == 0 {
			zero = true
			rowspan = 1
		}
	}

	cell := &naiveCell{
		text:      TextOf(node),
		header:    node.Tag() == "th",
		row:       row,
		rowSpan:   rowspan,
		colSpan:   colspan,
		sourceRow: row,
		sourceCol: it.sourceCol,
		node:      node,
	}
	m.cells = append(m.cells, cell)
	if zero {
		m.zeroRows[cell] = row
	}

	for {
		m.ensureRows(row + rowspan)
		col := m.nextFreeSlot(row, colspan)
		cell.col = col
		m.ensureCols(col + colspan)
		if m.rectFree(row, col, rowspan, colspan) {
			m.fill(cell, row, col, rowspan, colspan)
			return
		}
		// 冲突：插入新列，重走全部单元格（包括 rowspan=0 的逐行延伸）。
		m.growColumnAt(col)
		m.rewalk()
	}
}

// rewalk 丢弃槽位并从零重放整段逐行算法，单元格记录沿用既有实例。
func (m *naiveModel) rewalk() {
	byNode := make(map[Node]*naiveCell, len(m.cells))
	for _, cell := range m.cells {
		byNode[cell.node] = cell
	}

	rows := len(m.slots)
	m.slots = make([][]*naiveCell, rows)
	for r := range m.slots {
		m.slots[r] = make([]*naiveCell, m.cols)
	}

	rowItems := make([][]rowCell, len(m.slots))
	for _, it := range m.items {
		r := m.rowOf[it.tr]
		rowItems[r] = append(rowItems[r], it)
	}

	// 重走时 rowspan=0 的最终跨度由 growZeroCells 重新累计，先复位。
	for cell := range m.zeroRows {
		cell.rowSpan = 1
	}

	for r := 0; r < len(rowItems); r++ {
		m.growZeroCells(r)
		for _, it := range rowItems[r] {
			cell := byNode[it.cell]
			for {
				m.ensureRows(r + cell.rowSpan)
				col := m.nextFreeSlot(r, cell.colSpan)
				cell.row = r
				cell.col = col
				m.ensureCols(col + cell.colSpan)
				if m.rectFree(r, col, cell.rowSpan, cell.colSpan) {
					m.fill(cell, r, col, cell.rowSpan, cell.colSpan)
					break
				}
				// 重走中再次冲突：继续插列并整体重放。
				m.growColumnAt(col)
				m.rewalk()
				return
			}
		}
	}
}

func (m *naiveModel) nextFreeSlot(r, width int) int {
	row := m.slots[r]
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

func (m *naiveModel) rectFree(r, c, height, width int) bool {
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			if m.slots[r+i][c+j] != nil {
				return false
			}
		}
	}
	return true
}

func (m *naiveModel) fill(cell *naiveCell, r, c, height, width int) {
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			m.slots[r+i][c+j] = cell
		}
	}
}

func (m *naiveModel) growColumnAt(c int) {
	for r := range m.slots {
		row := make([]*naiveCell, 0, m.cols+1)
		row = append(row, m.slots[r][:c]...)
		row = append(row, nil)
		row = append(row, m.slots[r][c:]...)
		m.slots[r] = row
	}
	m.cols++

	seen := map[*naiveCell]bool{}
	for _, cell := range m.cells {
		if seen[cell] {
			continue
		}
		seen[cell] = true
		switch {
		case cell.col >= c:
			cell.col++
		case cell.col+cell.colSpan > c:
			cell.colSpan++
		}
	}
}
