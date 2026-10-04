package reorder

import "fmt"

// DepKind 依赖类型。
type DepKind int

const (
	DepRAW     DepKind = iota // 真依赖：先写后读
	DepWAR                    // 反依赖：先读后写
	DepWAW                    // 输出依赖：先写后写
	DepBarrier                // 屏障顺序
)

func (k DepKind) String() string {
	switch k {
	case DepRAW:
		return "RAW(真依赖)"
	case DepWAR:
		return "WAR(反依赖)"
	case DepWAW:
		return "WAW(输出依赖)"
	case DepBarrier:
		return "barrier(屏障)"
	}
	return "?"
}

// Edge 一条依赖边：原始序列中第 From 条必须在第 To 条之前执行。
// 下标为原始序列中的序号（0 起）。
type Edge struct {
	From int
	To   int
	Kind DepKind
	Loc  Location // 冲突位置（DepBarrier 时无效）
}

func (e Edge) String() string {
	if e.Kind == DepBarrier {
		return fmt.Sprintf("#%d -> #%d（%s）", e.From+1, e.To+1, e.Kind)
	}
	return fmt.Sprintf("#%d -> #%d（%s，位置 %s）", e.From+1, e.To+1, e.Kind, e.Loc)
}

// ComputeEdges 计算原始序列的全部依赖边。
//
// 对每对 i < j：
//   - 任一方为屏障：加入屏障顺序边（屏障两侧指令不得越序）；
//   - W(i) 与 R(j) 相交：RAW 边；
//   - R(i) 与 W(j) 相交：WAR 边；
//   - W(i) 与 W(j) 相交：WAW 边。
//
// 内存区间按半开区间精确求交，每个相交位置对产生一条边。
// 返回的边按 (From, To) 升序排列。
func ComputeEdges(insts []Inst) []Edge {
	var edges []Edge
	for i := 0; i < len(insts); i++ {
		for j := i + 1; j < len(insts); j++ {
			if insts[i].Kind == OpBarrier || insts[j].Kind == OpBarrier {
				edges = append(edges, Edge{From: i, To: j, Kind: DepBarrier})
				continue
			}
			edges = appendOverlapEdges(edges, i, j, DepRAW, insts[i].Writes(), insts[j].Reads())
			edges = appendOverlapEdges(edges, i, j, DepWAR, insts[i].Reads(), insts[j].Writes())
			edges = appendOverlapEdges(edges, i, j, DepWAW, insts[i].Writes(), insts[j].Writes())
		}
	}
	return edges
}

func appendOverlapEdges(edges []Edge, i, j int, kind DepKind, as, bs []Location) []Edge {
	for _, a := range as {
		for _, b := range bs {
			if a.Overlaps(b) {
				edges = append(edges, Edge{From: i, To: j, Kind: kind, Loc: a})
			}
		}
	}
	return edges
}
