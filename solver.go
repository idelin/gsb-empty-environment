package main

import (
	"fmt"
	"math/bits"
	"sort"
)

// INF 是差分约束与费用流里使用的"无穷大"。
const INF = int64(1) << 60

// maxAbsValue 限制输入数值规模，避免最短路/费用流中间结果溢出。
const maxAbsValue = int64(1) << 50

// Item 是一件商品：id、原始标价、是否锁定（锁定则价格不可改）。
type Item struct {
	ID     string `json:"id"`
	Price  int64  `json:"price"`
	Locked bool   `json:"locked"`
}

// Bound 是一条成对价差约束：Min <= price[To]-price[From] <= Max。
// Min / Max 缺省表示该方向无界。
type Bound struct {
	From string `json:"from"`
	To   string `json:"to"`
	Min  *int64 `json:"min,omitempty"`
	Max  *int64 `json:"max,omitempty"`
}

// Input 是求解器的完整输入。
type Input struct {
	Items  []Item  `json:"items"`
	Bounds []Bound `json:"bounds"`
}

// PriceEntry 是输出中每件商品的最终价格信息。
type PriceEntry struct {
	ID       string `json:"id"`
	Price    int64  `json:"price"`
	Original int64  `json:"originalPrice"`
	Locked   bool   `json:"locked"`
	Changed  bool   `json:"changed"`
}

// Evidence 是不可行证据（负环）。
type Evidence struct {
	Type        string   `json:"type"`
	Constraints []string `json:"constraints"`
	CycleSum    int64    `json:"cycleSum"`
	Message     string   `json:"message"`
}

// Output 是求解器的完整输出。
type Output struct {
	Feasible       bool         `json:"feasible"`
	Error          string       `json:"error,omitempty"`
	Prices         []PriceEntry `json:"prices,omitempty"`
	ModifiedCount  int          `json:"modifiedCount,omitempty"`
	ModifiedItems  []string     `json:"modifiedItems,omitempty"`
	TotalAbsChange int64        `json:"totalAbsChange,omitempty"`
	Evidence       *Evidence    `json:"evidence,omitempty"`
}

// arc 是差分约束图中的一条边：price[to] - price[from] <= w。
type arc struct {
	from, to int
	w        int64
	desc     string
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Solve 求解价格修复问题。
//
// 目标按词典序：
//  1. 修改商品数最少（锁定商品永远不可改）；
//  2. 在修改数最少的前提下，总绝对改价额最小。
//
// 若锁定约束与价差约束本身矛盾，则返回负环作为不可行证据。
func Solve(in *Input) Output {
	n := len(in.Items)
	if n == 0 {
		return Output{Feasible: false, Error: "至少需要 1 件商品"}
	}
	if n > 18 {
		return Output{Feasible: false, Error: fmt.Sprintf("商品数量 %d 超过上限 18", n)}
	}
	idx := make(map[string]int, n)
	orig := make([]int64, n)
	locked := make([]bool, n)
	for i, it := range in.Items {
		if it.ID == "" {
			return Output{Feasible: false, Error: fmt.Sprintf("第 %d 件商品缺少 id", i)}
		}
		if _, dup := idx[it.ID]; dup {
			return Output{Feasible: false, Error: fmt.Sprintf("商品 id %q 重复", it.ID)}
		}
		if abs64(it.Price) > maxAbsValue {
			return Output{Feasible: false, Error: fmt.Sprintf("商品 %q 的价格绝对值超过上限", it.ID)}
		}
		idx[it.ID] = i
		orig[i] = it.Price
		locked[i] = it.Locked
	}

	// 价差约束 -> 差分约束边。
	var pairArcs []arc
	for bi, b := range in.Bounds {
		i, ok := idx[b.From]
		if !ok {
			return Output{Feasible: false, Error: fmt.Sprintf("第 %d 条价差界限：未知商品 %q", bi, b.From)}
		}
		j, ok := idx[b.To]
		if !ok {
			return Output{Feasible: false, Error: fmt.Sprintf("第 %d 条价差界限：未知商品 %q", bi, b.To)}
		}
		if b.Min == nil && b.Max == nil {
			continue
		}
		if b.Min != nil && abs64(*b.Min) > maxAbsValue ||
			b.Max != nil && abs64(*b.Max) > maxAbsValue {
			return Output{Feasible: false, Error: fmt.Sprintf("第 %d 条价差界限的数值绝对值超过上限", bi)}
		}
		if b.Max != nil {
			pairArcs = append(pairArcs, arc{from: i, to: j, w: *b.Max,
				desc: fmt.Sprintf("price[%s] - price[%s] <= %d", in.Items[j].ID, in.Items[i].ID, *b.Max)})
		}
		if b.Min != nil {
			pairArcs = append(pairArcs, arc{from: j, to: i, w: -*b.Min,
				desc: fmt.Sprintf("price[%s] - price[%s] >= %d", in.Items[j].ID, in.Items[i].ID, *b.Min)})
		}
	}

	// 不可行判定：价差约束 + 锁定约束（经接地节点 G 表示 p_i = orig_i）。
	// 该图存在负环 <=> 约束系统不可行。
	ground := n
	evidenceArcs := make([]arc, 0, len(pairArcs)+2*n)
	evidenceArcs = append(evidenceArcs, pairArcs...)
	for i := 0; i < n; i++ {
		if !locked[i] {
			continue
		}
		evidenceArcs = append(evidenceArcs,
			arc{from: ground, to: i, w: orig[i],
				desc: fmt.Sprintf("price[%s] <= %d（锁定）", in.Items[i].ID, orig[i])},
			arc{from: i, to: ground, w: -orig[i],
				desc: fmt.Sprintf("price[%s] >= %d（锁定）", in.Items[i].ID, orig[i])},
		)
	}
	if cyc, sum, ok := findNegativeCycle(n+1, evidenceArcs); ok {
		descs := make([]string, len(cyc))
		for k, a := range cyc {
			descs[k] = a.desc
		}
		return Output{
			Feasible: false,
			Evidence: &Evidence{
				Type:        "negative-cycle",
				Constraints: descs,
				CycleSum:    sum,
				Message: fmt.Sprintf(
					"负环不可行证据：沿环把以上 %d 条约束相加，左端相互抵消为 0，右端之和为 %d，得到 0 <= %d，矛盾",
					len(cyc), sum, sum),
			},
		}
	}

	// 全源最短路（仅价差约束）。dist[a][b] 是 price[b]-price[a] 被约束导出的最紧上界。
	dist := allPairsShortest(n, pairArcs)

	// consistent(a,b)：a、b 同时保持原价是否违反最紧导出界 orig[b]-orig[a] <= dist[a][b]。
	consistent := func(a, b int) bool {
		return dist[a][b] >= INF/2 || orig[b]-orig[a] <= dist[a][b]
	}

	var unlocked []int
	for i := 0; i < n; i++ {
		if !locked[i] {
			unlocked = append(unlocked, i)
		}
	}
	// cand：与全部锁定商品相容、因而"有可能"保持原价的未锁定商品。
	var cand []int
	for _, i := range unlocked {
		ok := true
		for l := 0; l < n && ok; l++ {
			if locked[l] && !(consistent(i, l) && consistent(l, i)) {
				ok = false
			}
		}
		if ok {
			cand = append(cand, i)
		}
	}
	// 相容图：两件未锁定商品可以同时保持原价当且仅当互为相容。
	m := len(cand)
	adj := make([]uint64, m)
	for a := 0; a < m; a++ {
		for b := a + 1; b < m; b++ {
			if consistent(cand[a], cand[b]) && consistent(cand[b], cand[a]) {
				adj[a] |= 1 << uint(b)
				adj[b] |= 1 << uint(a)
			}
		}
	}

	// 最少修改数 = 未锁定商品数 - 相容图最大团大小。
	// 对每个最大团（保持原价的商品集合），用最小费用循环流求该集合下的最小总改价。
	type cliqueInfo struct {
		kept []bool
		lb   int64
	}
	infos := make([]cliqueInfo, 0, 1)
	for _, c := range maxCliques(adj) {
		kept := make([]bool, n)
		for i := 0; i < n; i++ {
			kept[i] = locked[i]
		}
		for _, ci := range c {
			kept[cand[ci]] = true
		}
		var free, keptIdx []int
		for _, i := range unlocked {
			if !kept[i] {
				free = append(free, i)
			}
		}
		for i := 0; i < n; i++ {
			if kept[i] {
				keptIdx = append(keptIdx, i)
			}
		}
		infos = append(infos, cliqueInfo{kept: kept, lb: lowerBound(free, keptIdx, orig, dist)})
	}
	sort.Slice(infos, func(x, y int) bool { return infos[x].lb < infos[y].lb })

	best := INF
	var bestP []int64
	for _, info := range infos {
		if info.lb >= best {
			continue
		}
		p, cost := minChangeForKeptSet(n, pairArcs, orig, info.kept)
		if cost < best {
			best = cost
			bestP = p
		}
	}

	entries := make([]PriceEntry, n)
	var modified []string
	var total int64
	for i := 0; i < n; i++ {
		changed := bestP[i] != orig[i]
		entries[i] = PriceEntry{
			ID:       in.Items[i].ID,
			Price:    bestP[i],
			Original: orig[i],
			Locked:   locked[i],
			Changed:  changed,
		}
		if changed {
			modified = append(modified, in.Items[i].ID)
			total += abs64(bestP[i] - orig[i])
		}
	}
	return Output{
		Feasible:       true,
		Prices:         entries,
		ModifiedCount:  len(modified),
		ModifiedItems:  modified,
		TotalAbsChange: total,
	}
}

// findNegativeCycle 在差分约束图上用 Bellman-Ford 找负环。
// 找到时返回构成环的边（按环的方向排列）与环权值和（必为负）。
func findNegativeCycle(n int, arcs []arc) ([]arc, int64, bool) {
	dist := make([]int64, n) // 全 0 初始化等价于超级源点，能发现任意负环
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}
	x := -1
	for iter := 0; iter < n; iter++ {
		x = -1
		for ai, a := range arcs {
			if dist[a.to] > dist[a.from]+a.w {
				dist[a.to] = dist[a.from] + a.w
				parent[a.to] = ai
				x = a.to
			}
		}
	}
	if x == -1 {
		return nil, 0, false
	}
	// 沿父指针回退 n 步，保证进入环内。
	y := x
	for i := 0; i < n; i++ {
		y = arcs[parent[y]].from
	}
	var cyc []arc
	for cur := y; ; {
		ai := parent[cur]
		cyc = append(cyc, arcs[ai])
		cur = arcs[ai].from
		if cur == y {
			break
		}
	}
	for i, j := 0, len(cyc)-1; i < j; i, j = i+1, j-1 {
		cyc[i], cyc[j] = cyc[j], cyc[i]
	}
	var sum int64
	for _, a := range cyc {
		sum += a.w
	}
	return cyc, sum, true
}

// allPairsShortest 用 Floyd-Warshall 求差分约束图的全源最短路。
// 调用前需保证图中无负环。
func allPairsShortest(n int, arcs []arc) [][]int64 {
	dist := make([][]int64, n)
	for i := range dist {
		dist[i] = make([]int64, n)
		for j := range dist[i] {
			dist[i][j] = INF
		}
		dist[i][i] = 0
	}
	for _, a := range arcs {
		if a.w < dist[a.from][a.to] {
			dist[a.from][a.to] = a.w
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if dist[i][k] >= INF/2 {
				continue
			}
			dik := dist[i][k]
			for j := 0; j < n; j++ {
				if dist[k][j] >= INF/2 {
					continue
				}
				if d := dik + dist[k][j]; d < dist[i][j] {
					dist[i][j] = d
				}
			}
		}
	}
	return dist
}

// lowerBound 计算"kept 保持原价、free 可改"时总绝对改价额的下界：
// 每个自由商品被 kept 商品钳出的可行区间到其原价的距离之和。
func lowerBound(free, kept []int, orig []int64, dist [][]int64) int64 {
	var lb int64
	for _, i := range free {
		lo, hi := -INF, INF
		for _, j := range kept {
			if dist[i][j] < INF/2 {
				if v := orig[j] - dist[i][j]; v > lo {
					lo = v
				}
			}
			if dist[j][i] < INF/2 {
				if v := orig[j] + dist[j][i]; v < hi {
					hi = v
				}
			}
		}
		if lo > -INF/2 && orig[i] < lo {
			lb += lo - orig[i]
		}
		if hi < INF/2 && orig[i] > hi {
			lb += orig[i] - hi
		}
	}
	return lb
}

// maxCliques 枚举相容图中的所有最大团（顶点数 <= 18，Bron-Kerbosch 带枢纽剪枝）。
func maxCliques(adj []uint64) [][]int {
	m := len(adj)
	if m == 0 {
		return [][]int{{}}
	}
	full := uint64(1)<<uint(m) - 1
	maxSize := 0
	var cliques [][]int
	var rec func(r uint64, size int, p, x uint64)
	rec = func(r uint64, size int, p, x uint64) {
		if p == 0 && x == 0 {
			s := make([]int, 0, size)
			for v := 0; v < m; v++ {
				if r>>uint(v)&1 == 1 {
					s = append(s, v)
				}
			}
			if size > maxSize {
				maxSize = size
				cliques = [][]int{s}
			} else if size == maxSize {
				cliques = append(cliques, s)
			}
			return
		}
		if size+bits.OnesCount64(p) < maxSize {
			return
		}
		// 选使 |P ∩ N(u)| 最大的枢纽 u。
		u, bestDeg := -1, -1
		for tmp := p | x; tmp != 0; tmp &= tmp - 1 {
			v := bits.TrailingZeros64(tmp)
			if d := bits.OnesCount64(p & adj[v]); d > bestDeg {
				bestDeg, u = d, v
			}
		}
		for cands := p &^ adj[u]; cands != 0; cands &= cands - 1 {
			v := bits.TrailingZeros64(cands)
			rec(r|1<<uint(v), size+1, p&adj[v], x&adj[v])
			p &^= 1 << uint(v)
			x |= 1 << uint(v)
		}
	}
	rec(0, 0, full, 0)
	return cliques
}
