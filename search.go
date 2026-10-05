package fuzzdict

import (
	"container/heap"
	"errors"
	"sort"
)

// Costs 是四种编辑操作的非负整数代价。
// 距离定义为把词典中的词变换为查询串的最小总代价。
//
// 实现使用带权 Lowrance-Wagner 递推。当 2*Transpose >= Delete+Insert
// 时它与字符串编辑图（顶点为字符串、边为单步编辑操作）上的最短路径
// 完全一致——此时任何"字符连续越过多个字符"的链式交换都不比
// 删除+插入更便宜，最优脚本中每个字符至多参与一次交换。
// 当 2*Transpose < Delete + Insert 时，编辑图最短路的精确计算是
// NP 难的，本库的距离是编辑图距离的一个上界（仍是合法的带权
// Damerau-Levenshtein 距离）。
type Costs struct {
	Insert    int // 插入一个字符
	Delete    int // 删除一个字符
	Replace   int // 替换一个字符
	Transpose int // 交换相邻两个字符
}

func (c Costs) valid() bool {
	return c.Insert >= 0 && c.Delete >= 0 && c.Replace >= 0 && c.Transpose >= 0
}

// ErrNegativeCosts 表示代价中出现负数。
var ErrNegativeCosts = errors.New("fuzzdict: edit costs must be non-negative")

// Query 描述一次模糊查询。
type Query struct {
	Pattern string // 查询串
	K       int    // 返回的最大词条数；<= 0 时返回空
	Costs   Costs  // 编辑代价，必须非负
	MaxCost int    // 只保留代价 <= MaxCost 的结果；< 0 表示不限
	// MaxExpansions 是工作预算：最多展开的字典树节点数。
	// < 0 表示不限。预算耗尽时返回当前最优结果，
	// 并用 Result.Proven 标明哪些名次已被证明。
	MaxExpansions int
}

// Result 是一条命中结果。
type Result struct {
	Word string
	Cost int
	// Proven 为 true 表示：即使继续搜索，该名次及其之前的名次也不会改变。
	// 预算耗尽时，Proven 的结果一定构成返回列表的前缀。
	Proven bool
}

// item 是 best-first 搜索队列中的一个字典树节点状态。
type item struct {
	nd     *trieNode
	row    []int // 该节点的 DP 行，长度为 len(pattern)+1
	parent *item // 父节点状态，用于相邻交换项向上查行
	depth  int
	da     map[byte]int // 词前缀中各字符最后一次出现的深度
	lb     int          // 子树中任意词条代价的下界
}

// itemHeap 是按 (lb, depth) 排序的最小堆。
type itemHeap []*item

func (h itemHeap) Len() int { return len(h) }
func (h itemHeap) Less(i, j int) bool {
	if h[i].lb != h[j].lb {
		return h[i].lb < h[j].lb
	}
	return h[i].depth < h[j].depth
}
func (h itemHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x interface{}) { *h = append(*h, x.(*item)) }
func (h *itemHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Search 返回与 q.Pattern 编辑代价最小的前 K 个不同词条，
// 先按最小代价升序，代价相同按词典序升序。
func (d *Dictionary) Search(q Query) ([]Result, error) {
	if !q.Costs.valid() {
		return nil, ErrNegativeCosts
	}
	if q.K <= 0 || d.root == nil {
		return nil, nil
	}
	s := &searcher{pattern: []byte(q.Pattern), costs: q.Costs, maxCost: q.MaxCost}
	m := len(s.pattern)

	rootRow := make([]int, m+1)
	for j := range rootRow {
		rootRow[j] = j * s.costs.Insert
	}
	root := &item{nd: d.root, row: rootRow, da: make(map[byte]int)}
	root.lb = minOf(rootRow)

	pq := &itemHeap{root}
	heap.Init(pq)
	var cands []Result
	expanded := 0
	for pq.Len() > 0 {
		if q.MaxExpansions >= 0 && expanded >= q.MaxExpansions {
			break // 预算耗尽
		}
		if len(cands) >= q.K && (*pq)[0].lb > kthCost(cands, q.K) {
			break // 队列下界已超过第 K 名代价，结果已精确
		}
		it := heap.Pop(pq).(*item)
		expanded++
		cost := it.row[m]
		if it.nd.terminal && (q.MaxCost < 0 || cost <= q.MaxCost) {
			cands = append(cands, Result{Word: it.nd.word, Cost: cost})
		}
		for _, ce := range it.nd.children {
			child := s.buildItem(it, ce)
			if q.MaxCost >= 0 && child.lb > q.MaxCost {
				continue // 共享前缀剪枝：子树下界已超代价上限
			}
			heap.Push(pq, child)
		}
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Cost != cands[j].Cost {
			return cands[i].Cost < cands[j].Cost
		}
		return cands[i].Word < cands[j].Word
	})
	if len(cands) > q.K {
		cands = cands[:q.K]
	}

	// 未被发现的词条代价 >= 队列最小下界；MaxCost 之外的词条被规则排除。
	frontier := int(^uint(0) >> 1)
	if pq.Len() > 0 {
		frontier = (*pq)[0].lb
	}
	if q.MaxCost >= 0 && q.MaxCost+1 < frontier {
		frontier = q.MaxCost + 1
	}
	for i := range cands {
		cands[i].Proven = cands[i].Cost < frontier
	}
	return cands, nil
}

// searcher 保存一次查询的只读参数。
type searcher struct {
	pattern []byte
	costs   Costs
	maxCost int
}

// buildItem 计算子节点的 DP 行。
// 使用带权的 Lowrance-Wagner 递推（完整 Damerau-Levenshtein 距离，
// 允许同一子串被多次编辑，与字符串编辑图最短路径一致）。
func (s *searcher) buildItem(parent *item, ce childEntry) *item {
	m := len(s.pattern)
	depth := parent.depth + 1
	row := make([]int, m+1)
	row[0] = depth * s.costs.Delete

	da := make(map[byte]int, len(parent.da)+1)
	for k, v := range parent.da {
		da[k] = v
	}
	da[ce.ch] = depth

	lb := parent.lb
	if row[0] < lb {
		lb = row[0]
	}
	db := 0 // 本行中 pattern[db-1] == ce.ch 的最大列号
	for j := 1; j <= m; j++ {
		pc := s.pattern[j-1]
		j1 := db
		rc := s.costs.Replace
		if pc == ce.ch {
			rc = 0
			db = j
		}
		best := parent.row[j] + s.costs.Delete
		if v := row[j-1] + s.costs.Insert; v < best {
			best = v
		}
		if v := parent.row[j-1] + rc; v < best {
			best = v
		}
		// 相邻交换项：词中上次出现 pc 的位置 i1 与当前位置交换，
		// 中间字符分别删除 / 插入。
		if i1 := parent.da[pc]; i1 > 0 && j1 > 0 {
			anc := parent
			for anc.depth > i1-1 {
				anc = anc.parent
			}
			v := anc.row[j1-1] +
				(depth-i1-1)*s.costs.Delete +
				s.costs.Transpose +
				(j-j1-1)*s.costs.Insert
			if v < best {
				best = v
			}
		}
		row[j] = best
		if best < lb {
			lb = best
		}
	}
	return &item{nd: ce.nd, row: row, parent: parent, depth: depth, da: da, lb: lb}
}

// minOf 返回切片最小值。
func minOf(row []int) int {
	m := row[0]
	for _, v := range row[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// kthCost 返回候选中第 k 小的代价（调用前保证 len(cands) >= k）。
func kthCost(cands []Result, k int) int {
	costs := make([]int, len(cands))
	for i, c := range cands {
		costs[i] = c.Cost
	}
	sort.Ints(costs)
	return costs[k-1]
}
