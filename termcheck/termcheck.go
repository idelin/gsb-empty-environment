// Package termcheck 为 Go 安全宿主提供租户递归规则的终止性证明。
//
// 发布服务在执行租户规则前，把规则里的每个函数调用边（含互递归）
// 编码为 Edge 交给 Prove。每条边携带一个关系矩阵，描述被调方每个
// 实参与调用方每个形参之间的规模关系：Less（严格变小）、Leq（不增）、
// Unknown（未知）。参数限定为自然数，运算合法性（无下溢、无负值）
// 由调用方保证；本包只依赖自然数的良基性：严格下降的自然数序列必然
// 有穷。
//
// Prove 采用规模变化终止（size-change termination）原则：对所有调用
// 边做复合闭包，闭包中每个幂等的循环矩阵（G∘G = G 且起止于同一函数）
// 必须存在某个严格下降的对角项（某参数在整段循环后严格变小），才判定
// 终止。无法证明时返回全部违反条件的组合边链。
//
// 注意：本检查是可靠但不完备的——未证明不等于实际会无限循环，只表示
// 宿主不应放行该规则。
package termcheck

// Rel 是被调方某一实参相对调用方某一形参的规模关系。
type Rel int

const (
	Unknown Rel = iota // 未知：不提供任何规模保证
	Leq                // 不增：被调方实参 <= 调用方形参
	Less               // 严格下降：被调方实参 < 调用方形参
)

func (r Rel) String() string {
	switch r {
	case Less:
		return "<"
	case Leq:
		return "<="
	default:
		return "?"
	}
}

// then 复合沿调用路径相继的两个关系：先 a 后 b。
// 任一环节未知则整体未知；任一环节严格下降则整体严格下降。
func then(a, b Rel) Rel {
	if a == Unknown || b == Unknown {
		return Unknown
	}
	if a == Less || b == Less {
		return Less
	}
	return Leq
}

// Matrix 是调用边的关系矩阵。行按被调方实参编号，列按调用方形参
// 编号：At(i, j) 是被调方第 i 个实参与调用方第 j 个形参的关系。
// 两侧参数个数可以不同，越界位置一律按 Unknown 处理。
type Matrix struct {
	Rows, Cols int
	Cells      []Rel // 行主序，长度 Rows*Cols
}

// NewMatrix 按行主序构造关系矩阵，便于测试与宿主编码。
func NewMatrix(rows, cols int, cells ...Rel) Matrix {
	if len(cells) != rows*cols {
		panic("termcheck: cell count mismatch")
	}
	return Matrix{Rows: rows, Cols: cols, Cells: cells}
}

// At 返回第 i 行第 j 列的关系，越界视为 Unknown。
func (m Matrix) At(i, j int) Rel {
	if i < 0 || i >= m.Rows || j < 0 || j >= m.Cols {
		return Unknown
	}
	return m.Cells[i*m.Cols+j]
}

// Equal 报告两个矩阵是否逐元素相同（尺寸不同即不同）。
func (m Matrix) Equal(o Matrix) bool {
	if m.Rows != o.Rows || m.Cols != o.Cols {
		return false
	}
	for i, c := range m.Cells {
		if c != o.Cells[i] {
			return false
		}
	}
	return true
}

// key 返回矩阵内容的规范化表示，用于闭包去重。
func (m Matrix) key() string {
	b := make([]byte, 0, len(m.Cells)+8)
	b = append(b, byte(m.Rows), byte(m.Cols))
	for _, c := range m.Cells {
		b = append(b, byte(c))
	}
	return string(b)
}

// Compose 复合两条首尾相接的调用边的矩阵：a 是 f→g，b 是 g→h，
// 结果是 f→h 的关系矩阵。结果第 i 行第 k 列取所有中间参数 j 上
// then(a[j][k], b[i][j]) 中最强的关系。
func Compose(a, b Matrix) Matrix {
	rows, cols := b.Rows, a.Cols
	cells := make([]Rel, rows*cols)
	for i := 0; i < rows; i++ {
		for k := 0; k < cols; k++ {
			best := Unknown
			for j := 0; j < a.Rows && j < b.Cols; j++ {
				if r := then(a.At(j, k), b.At(i, j)); r > best {
					best = r
				}
			}
			cells[i*cols+k] = best
		}
	}
	return Matrix{Rows: rows, Cols: cols, Cells: cells}
}

// Edge 是一条函数调用边：From 在规则体内调用 To，M 描述实参规模关系。
type Edge struct {
	From, To string
	M        Matrix
}

// Violation 是一条无法证明终止的组合边链：沿 Chain 复合得到的矩阵
// 是幂等循环矩阵，但没有任何严格下降的对角项。
type Violation struct {
	Func  string // 循环闭合处的函数（链首链尾相同）
	Chain []Edge // 违反条件的组合边链，首尾相接且回到 Func
	M     Matrix // Chain 复合出的幂等矩阵
}

// Report 是 Prove 的结论。
type Report struct {
	// Terminating 为真表示已证明所有递归都会终止。
	// 为假只表示证明失败，不代表实际一定无限循环。
	Terminating bool
	// Violations 列出全部违反条件的组合边链。
	Violations []Violation
	// ClosureSize 是闭包中不同复合图的数量，供宿主观测开销。
	ClosureSize int
}

// graph 是闭包中的一个复合图：一条已复合的调用路径及其见证边链。
type graph struct {
	from, to string
	m        Matrix
	chain    []int // 见证边在输入 edges 中的下标序列
}

func (g *graph) key() string {
	return g.from + "\x00" + g.to + "\x00" + g.m.key()
}

// Prove 对调用边集合做规模变化闭包，并检查每个幂等循环矩阵是否
// 存在严格下降对角项。全部满足则判定终止；否则返回全部违反链。
func Prove(edges []Edge) Report {
	var graphs []*graph
	index := make(map[string]struct{})
	var work []*graph

	add := func(g *graph) {
		k := g.key()
		if _, ok := index[k]; ok {
			return
		}
		index[k] = struct{}{}
		graphs = append(graphs, g)
		work = append(work, g)
	}

	for i, e := range edges {
		add(&graph{from: e.From, to: e.To, m: e.M, chain: []int{i}})
	}

	// 工作表法求复合闭包：新图与所有已有图在两侧复合。
	for len(work) > 0 {
		g := work[0]
		work = work[1:]
		for _, h := range graphs {
			if g.to == h.from {
				add(&graph{
					from:  g.from,
					to:    h.to,
					m:     Compose(g.m, h.m),
					chain: append(append([]int{}, g.chain...), h.chain...),
				})
			}
			if h.to == g.from {
				add(&graph{
					from:  h.from,
					to:    g.to,
					m:     Compose(h.m, g.m),
					chain: append(append([]int{}, h.chain...), g.chain...),
				})
			}
		}
	}

	rep := Report{Terminating: true, ClosureSize: len(graphs)}
	seen := make(map[string]struct{})
	for _, g := range graphs {
		if g.from != g.to || !Compose(g.m, g.m).Equal(g.m) || hasLessDiag(g.m) {
			continue
		}
		k := g.from + "\x00" + g.m.key()
		if _, ok := seen[k]; ok {
			continue // 同一函数同一幂等矩阵只报一条见证链
		}
		seen[k] = struct{}{}
		chain := make([]Edge, len(g.chain))
		for i, idx := range g.chain {
			chain[i] = edges[idx]
		}
		rep.Violations = append(rep.Violations, Violation{Func: g.from, Chain: chain, M: g.m})
		rep.Terminating = false
	}
	return rep
}

// hasLessDiag 报告矩阵对角线上是否存在严格下降项。
func hasLessDiag(m Matrix) bool {
	n := m.Rows
	if m.Cols < n {
		n = m.Cols
	}
	for i := 0; i < n; i++ {
		if m.At(i, i) == Less {
			return true
		}
	}
	return false
}
