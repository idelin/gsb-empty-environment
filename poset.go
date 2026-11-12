package netsec

import "fmt"

// Relation 描述两个安全标记在有限偏序中的相对位置。
type Relation int

const (
	// RelationUnknown 表示至少有一个标记不属于该偏序。
	RelationUnknown Relation = iota
	// RelationLess 表示 a < b（a 严格低于 b）。
	RelationLess
	// RelationEqual 表示 a 与 b 是同一标记。
	RelationEqual
	// RelationGreater 表示 a > b（a 严格高于 b）。
	RelationGreater
	// RelationIncomparable 表示 a、b 同属偏序但互不可比。
	RelationIncomparable
)

// Poset 是由标签集合与其直接偏序边构成的有限偏序。
//
// 标签的安全级别越高，其在偏序中的位置越高；即公开 < 内部 < 机密。
// Edge{Lower, Upper} 声明 Lower 严格低于 Upper，构造时会对传递闭包
// 做自反化，并拒绝会破坏反对称性的环。
type Poset struct {
	labels []string
	index  map[string]int
	less   [][]bool // less[i][j] 为真当且仅当 label i < label j（含传递边）
	lessEq [][]bool // lessEq[i][j] 为真当且仅当 label i <= label j
}

// PosetEdge 声明 Lower 严格低于 Upper 的一条直接偏序边。
type PosetEdge struct {
	Lower string
	Upper string
}

// NewPoset 根据标签与直接偏序边构造有限偏序，并计算传递闭包。
func NewPoset(labels []string, edges []PosetEdge) (*Poset, error) {
	if len(labels) == 0 {
		return nil, fmt.Errorf("netsec: poset requires at least one label")
	}
	index := make(map[string]int, len(labels))
	for i, label := range labels {
		if label == "" {
			return nil, fmt.Errorf("netsec: poset label at index %d is empty", i)
		}
		if _, dup := index[label]; dup {
			return nil, fmt.Errorf("netsec: duplicate poset label %q", label)
		}
		index[label] = i
	}

	n := len(labels)
	less := make([][]bool, n)
	lessEq := make([][]bool, n)
	for i := range less {
		less[i] = make([]bool, n)
		lessEq[i] = make([]bool, n)
		lessEq[i][i] = true
	}
	for _, edge := range edges {
		lo, okLo := index[edge.Lower]
		hi, okHi := index[edge.Upper]
		if !okLo {
			return nil, fmt.Errorf("netsec: poset edge references unknown lower label %q", edge.Lower)
		}
		if !okHi {
			return nil, fmt.Errorf("netsec: poset edge references unknown upper label %q", edge.Upper)
		}
		if lo == hi {
			return nil, fmt.Errorf("netsec: poset edge %q cannot relate a label to itself", edge.Lower)
		}
		less[lo][hi] = true
	}

	// Floyd–Warshall 计算传递闭包。
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !less[i][k] {
				continue
			}
			for j := 0; j < n; j++ {
				if less[k][j] {
					less[i][j] = true
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if less[i][j] {
				lessEq[i][j] = true
				// 环会同时产生 i<j 与 j<i，破坏偏序反对称性。
				if less[j][i] {
					return nil, fmt.Errorf("netsec: poset cycle detected between %q and %q", labels[i], labels[j])
				}
			}
		}
	}

	return &Poset{labels: append([]string(nil), labels...), index: index, less: less, lessEq: lessEq}, nil
}

// Labels 返回偏序中的全部标签（构造顺序）。
func (p *Poset) Labels() []string {
	return append([]string(nil), p.labels...)
}

// Contains 判断标签是否属于该偏序。
func (p *Poset) Contains(label string) bool {
	_, ok := p.index[label]
	return ok
}

func (p *Poset) indexOf(label string) (int, bool) {
	i, ok := p.index[label]
	return i, ok
}

// Order 返回 a 相对 b 的位置；任一标签未知时返回 RelationUnknown。
func (p *Poset) Order(a, b string) Relation {
	i, okI := p.indexOf(a)
	j, okJ := p.indexOf(b)
	if !okI || !okJ {
		return RelationUnknown
	}
	return p.order(i, j)
}

func (p *Poset) order(i, j int) Relation {
	if i == j {
		return RelationEqual
	}
	if p.less[i][j] {
		return RelationLess
	}
	if p.less[j][i] {
		return RelationGreater
	}
	return RelationIncomparable
}

// Leq 判断 a <= b 是否成立；任一标签未知时返回 false。
func (p *Poset) Leq(a, b string) bool {
	i, okI := p.indexOf(a)
	j, okJ := p.indexOf(b)
	if !okI || !okJ {
		return false
	}
	return p.lessEq[i][j]
}
