package tablegrid

// 规范对跨度的钳制上限：
// https://html.spec.whatwg.org/multipage/tables.html#attribute-limits
const maxSpan = 1000

// Cell 是展开后网格中的一个逻辑单元格。
//
// 一个源单元格恰好产生一条 Cell 记录；被 rowspan/colspan 覆盖的网格位置
// 通过 RowSpan、ColSpan 表达，不会产生重复记录。
type Cell struct {
	// Text 是单元格子树中的文本内容。
	Text string
	// IsHeader 为 true 时对应 <th>，否则对应 <td>。
	IsHeader bool

	// Row 是单元格左上角所在的网格行（从 0 开始，跨整个表格编号）。
	Row int
	// Col 是单元格左上角所在的网格列（从 0 开始）。
	Col int
	// RowSpan 是最终占用的行数；rowspan=0 会被解析为延伸到所在行组末尾。
	RowSpan int
	// ColSpan 是最终占用的列数。
	ColSpan int

	// SourceRow 是该单元格的 <tr> 在整个表格中的序号（从 0 开始），
	// 与 Row 不同：被前面的跨行单元格推挤时 Row 会大于 SourceRow 所在的位置。
	SourceRow int
	// SourceCol 是单元格在其所属 <tr> 的单元格序列中的序号（从 0 开始），
	// 即 DOM 顺序意义上的“第几列单元格”。
	SourceCol int

	// ColPath 是多行表头合并后得到的列路径。
	// 表头单元格的路径只包含到自身为止的表头层级；
	// 数据单元格的路径包含该列完整的表头文本（可能为空切片）。
	ColPath []string
}

// Cover 报告网格坐标 (r, c) 是否落在该单元格覆盖的矩形范围内。
func (c *Cell) Cover(r, c2 int) bool {
	return r >= c.Row && r < c.Row+c.RowSpan && c2 >= c.Col && c2 < c.Col+c.ColSpan
}

// RowGroup 描述一个行组（thead/tbody/tfoot，或为裸 tr 隐式生成的 tbody）。
type RowGroup struct {
	// Tag 为 "thead"、"tbody" 或 "tfoot"；隐式行组同样为 "tbody"。
	Tag string
	// Implicit 为 true 表示该 tbody 是由行组外的裸 <tr> 隐式生成的。
	Implicit bool
	// StartRow 是行组在展开网格中的起始行（全局行号）。
	StartRow int
	// Rows 是行组包含的网格行数。
	Rows int
}

// Column 描述展开后的一列及其表头路径。
type Column struct {
	// Index 是列号（从 0 开始）。
	Index int
	// HeaderPath 是合并多行 <th> 表头后得到的列路径，可能为空。
	HeaderPath []string
}

// Grid 是表格展开的结果。
type Grid struct {
	// Rows、Cols 是展开后网格的行列数。
	Rows int
	Cols int
	// Cells 按 (Row, Col) 文档顺序排列，每个源单元格一条记录。
	Cells []Cell
	// Groups 按文档顺序列出全部行组（含隐式 tbody）。
	Groups []RowGroup
	// Columns 按列号列出每一列及其合并后的表头路径。
	Columns []Column

	// slots 是 rows×cols 的矩形槽位矩阵，nil 表示空槽位。
	slots [][]*Cell
}

// CellAt 返回覆盖网格坐标 (r, c) 的单元格，坐标越界或为空槽位时返回 nil。
func (g *Grid) CellAt(r, c int) *Cell {
	if r < 0 || r >= g.Rows || c < 0 || c >= g.Cols {
		return nil
	}
	return g.slots[r][c]
}

// CellMatrix 返回与网格同形的单元格指针矩阵，空槽位为 nil。
// 每次调用都会分配一份新的底层数组，调用方可自由修改。
func (g *Grid) CellMatrix() [][]*Cell {
	out := make([][]*Cell, g.Rows)
	for r := range out {
		out[r] = append([]*Cell(nil), g.slots[r]...)
	}
	return out
}
