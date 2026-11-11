package tablegrid

// buildColumns 为展开后的每一列合并多行表头，生成列路径。
//
// 表头行的识别：
//   - 存在 thead 行组时，取所有 thead 覆盖的网格行；
//   - 否则取表格开头连续的、只包含 <th> 单元格的行。
//
// 同一路径中连续重复的表头文本只保留一次，与主流采集器的列路径约定一致。
func (b *builder) buildColumns() []Column {
	columns := make([]Column, b.cols)
	if b.cols == 0 || len(b.slots) == 0 {
		return columns
	}

	headerRows := b.headerRows()
	paths := make([][]string, b.cols)
	for c := 0; c < b.cols; c++ {
		var path []string
		for _, r := range headerRows {
			cell := b.slots[r][c]
			if cell == nil || !cell.IsHeader {
				continue
			}
			if len(path) > 0 && path[len(path)-1] == cell.Text {
				continue
			}
			path = append(path, cell.Text)
		}
		paths[c] = path
	}

	for c := range columns {
		columns[c] = Column{Index: c, HeaderPath: cloneStrings(paths[c])}
	}
	return columns
}

// assignCellPaths 把列路径写回每个单元格记录。
// 表头单元格只取它自身覆盖到的表头层级，数据单元格取完整列路径。
func (b *builder) assignCellPaths(g *Grid) {
	headerRows := b.headerRows()
	isHeaderRow := make(map[int]bool, len(headerRows))
	for _, r := range headerRows {
		isHeaderRow[r] = true
	}

	for i := range g.Cells {
		cell := &g.Cells[i]
		path := cloneStrings(g.Columns[cell.Col].HeaderPath)
		if cell.IsHeader && isHeaderRow[cell.Row] && cell.Text != "" {
			// 表头单元格的路径只包含到自身为止的表头层级。
			for idx, label := range path {
				if label == cell.Text {
					path = path[:idx+1]
					break
				}
			}
		}
		cell.ColPath = path
	}
}

func (b *builder) headerRows() []int {
	var rows []int
	for _, rg := range b.groupOrder {
		if rg.Tag == "thead" {
			for r := rg.StartRow; r < rg.StartRow+rg.Rows; r++ {
				rows = append(rows, r)
			}
		}
	}
	if len(rows) > 0 {
		return rows
	}

	// 没有 thead：从首行开始取“整行只有 th 单元格”的连续行。
	for r := 0; r < len(b.slots); r++ {
		sawCell := false
		allHeader := true
		for c := 0; c < b.cols; c++ {
			cell := b.slots[r][c]
			if cell == nil {
				continue
			}
			sawCell = true
			if !cell.IsHeader {
				allHeader = false
				break
			}
		}
		if !sawCell || !allHeader {
			break
		}
		rows = append(rows, r)
	}
	return rows
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}
