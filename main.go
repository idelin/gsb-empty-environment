package main

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Record 是一条带 ID 的时间区间记录，区间为半开语义 [Start, End)。
type Record struct {
	ID    string
	Start int64
	End   int64
}

// overlapLen 返回两条记录的正长度重叠；端点相接（如 [0,1] 与 [1,2]）返回 0，不算重叠。
func overlapLen(a, b Record) int64 {
	lo := a.Start
	if b.Start > lo {
		lo = b.Start
	}
	hi := a.End
	if b.End < hi {
		hi = b.End
	}
	if hi > lo {
		return hi - lo
	}
	return 0
}

// buildCandidates 利用区间结构生成候选对：把 B 组按 Start 排序后，
// 对每条 A 记录二分定位 Start < a.End 的前缀，再过滤出 End > a.Start 的记录。
// 复杂度 O((n+m) log m + 候选数)，避免 O(n*m) 全量两两比较。
func buildCandidates(as, bs []Record) [][]int {
	order := make([]int, len(bs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		if bs[order[i]].Start != bs[order[j]].Start {
			return bs[order[i]].Start < bs[order[j]].Start
		}
		return bs[order[i]].End < bs[order[j]].End
	})
	starts := make([]int64, len(order))
	for i, idx := range order {
		starts[i] = bs[idx].Start
	}

	cands := make([][]int, len(as))
	for i, a := range as {
		// 第一个 Start >= a.End 的位置，之前的 B 才可能与 a 重叠。
		hi := sort.Search(len(starts), func(k int) bool { return starts[k] >= a.End })
		for k := 0; k < hi; k++ {
			j := order[k]
			if bs[j].End > a.Start { // 正长度重叠（严格大于，端点相接被排除）
				cands[i] = append(cands[i], j)
			}
		}
	}
	return cands
}

// Match 表示一对匹配及其收益（重叠长度）。
type Match struct {
	A, B    Record
	Overlap int64
}

// Result 是匹配结果：匹配对 + 两侧未匹配记录。
type Result struct {
	Matches    []Match
	UnmatchedA []Record
	UnmatchedB []Record
}

// ---------- 最小费用最大流求解器 ----------
// 目标按字典序：先最大化匹配数，再最大化总收益。
// 建模：S->A(容量1,费用0)，A->B(容量1,费用=-重叠)，B->T(容量1,费用0)。
// 持续沿最短路增广直到不可增广：流量即最大匹配数，
// 该流量下的最小费用即最大总收益。

type edge struct {
	to, rev      int
	cap          int
	cost         int64
	candA, candB int // 仅 A->B 边有效，用于提取匹配
}

type graph struct {
	g [][]edge
}

func (g *graph) addEdge(from, to int, cap int, cost int64, a, b int) {
	g.g[from] = append(g.g[from], edge{to: to, rev: len(g.g[to]), cap: cap, cost: cost, candA: a, candB: b})
	g.g[to] = append(g.g[to], edge{to: from, rev: len(g.g[from]) - 1, cap: 0, cost: -cost, candA: -1, candB: -1})
}

const inf = math.MaxInt64 / 4

type pqItem struct {
	v    int
	dist int64
}
type priorityQueue []pqItem

func (q priorityQueue) Len() int            { return len(q) }
func (q priorityQueue) Less(i, j int) bool  { return q[i].dist < q[j].dist }
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// bellmanFord 计算初始势（图含负费用边，不能直接用 Dijkstra）。
func bellmanFord(g *graph, s int) []int64 {
	n := len(g.g)
	pot := make([]int64, n)
	for i := range pot {
		pot[i] = inf
	}
	pot[s] = 0
	for iter := 0; iter < n; iter++ {
		updated := false
		for v := 0; v < n; v++ {
			if pot[v] == inf {
				continue
			}
			for _, e := range g.g[v] {
				if e.cap > 0 && pot[v]+e.cost < pot[e.to] {
					pot[e.to] = pot[v] + e.cost
					updated = true
				}
			}
		}
		if !updated {
			break
		}
	}
	return pot
}

// augment 在约化费用上跑 Dijkstra，找到最短路则增广 1 单位流量。
func augment(g *graph, s, t int, pot []int64) bool {
	n := len(g.g)
	dist := make([]int64, n)
	prevV := make([]int, n)
	prevE := make([]int, n)
	for i := range dist {
		dist[i] = inf
	}
	dist[s] = 0
	pq := &priorityQueue{{v: s, dist: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if it.dist > dist[it.v] {
			continue
		}
		for ei, e := range g.g[it.v] {
			if e.cap <= 0 {
				continue
			}
			nd := it.dist + e.cost + pot[it.v] - pot[e.to]
			if nd < dist[e.to] {
				dist[e.to] = nd
				prevV[e.to] = it.v
				prevE[e.to] = ei
				heap.Push(pq, pqItem{v: e.to, dist: nd})
			}
		}
	}
	if dist[t] == inf {
		return false
	}
	for v := 0; v < n; v++ {
		if dist[v] < inf {
			pot[v] += dist[v]
		}
	}
	for v := t; v != s; v = prevV[v] {
		e := &g.g[prevV[v]][prevE[v]]
		e.cap--
		rev := &g.g[v][e.rev]
		rev.cap++
	}
	return true
}

// Solve 求字典序最优（匹配数最大，其次总收益最大）的匹配。
func Solve(as, bs []Record) Result {
	cands := buildCandidates(as, bs)
	n, m := len(as), len(bs)
	s, t := 0, n+m+1
	g := &graph{g: make([][]edge, n+m+2)}
	for i := 0; i < n; i++ {
		g.addEdge(s, 1+i, 1, 0, -1, -1)
	}
	for j := 0; j < m; j++ {
		g.addEdge(1+n+j, t, 1, 0, -1, -1)
	}
	for i := 0; i < n; i++ {
		for _, j := range cands[i] {
			g.addEdge(1+i, 1+n+j, 1, -overlapLen(as[i], bs[j]), i, j)
		}
	}

	pot := bellmanFord(g, s)
	for augment(g, s, t, pot) {
	}

	res := Result{}
	usedA := make([]bool, n)
	usedB := make([]bool, m)
	for i := 0; i < n; i++ {
		for _, e := range g.g[1+i] {
			if e.candA >= 0 && e.cap == 0 { // A->B 候选边被用掉
				usedA[e.candA] = true
				usedB[e.candB] = true
				res.Matches = append(res.Matches, Match{
					A: as[e.candA], B: bs[e.candB],
					Overlap: overlapLen(as[e.candA], bs[e.candB]),
				})
			}
		}
	}
	for i := 0; i < n; i++ {
		if !usedA[i] {
			res.UnmatchedA = append(res.UnmatchedA, as[i])
		}
	}
	for j := 0; j < m; j++ {
		if !usedB[j] {
			res.UnmatchedB = append(res.UnmatchedB, bs[j])
		}
	}
	sort.Slice(res.Matches, func(i, j int) bool { return res.Matches[i].A.ID < res.Matches[j].A.ID })
	return res
}

// TotalProfit 返回匹配结果的总收益。
func (r Result) TotalProfit() int64 {
	var p int64
	for _, mt := range r.Matches {
		p += mt.Overlap
	}
	return p
}

// ---------- 暴力枚举（小数据核对用） ----------

// bruteForce 枚举 A->B 的所有单射分配，返回字典序最优的 (匹配数, 总收益)。
func bruteForce(as, bs []Record) (int, int64) {
	bestCount := 0
	var bestProfit int64
	used := make([]bool, len(bs))
	var rec func(i, count int, profit int64)
	rec = func(i, count int, profit int64) {
		if i == len(as) {
			if count > bestCount || (count == bestCount && profit > bestProfit) {
				bestCount, bestProfit = count, profit
			}
			return
		}
		rec(i+1, count, profit) // 跳过 as[i]
		for j := range bs {
			if used[j] {
				continue
			}
			w := overlapLen(as[i], bs[j])
			if w <= 0 {
				continue
			}
			used[j] = true
			rec(i+1, count+1, profit+w)
			used[j] = false
		}
	}
	rec(0, 0, 0)
	return bestCount, bestProfit
}

// ---------- 未匹配证据 ----------

// ExplainUnmatched 为每条未匹配记录给出证据：
// 要么与对侧所有记录都没有正长度重叠（端点相接不算），
// 要么列出所有候选及它们各自被谁占用。
func ExplainUnmatched(r Record, mine, other []Record, otherMatchedTo map[string]string) []string {
	var lines []string
	any := false
	for _, o := range other {
		w := overlapLen(r, o)
		if w <= 0 {
			continue
		}
		any = true
		if owner, ok := otherMatchedTo[o.ID]; ok {
			lines = append(lines, fmt.Sprintf("候选 %s 重叠 %d，但已匹配给 %s", o.ID, w, owner))
		} else {
			lines = append(lines, fmt.Sprintf("候选 %s 重叠 %d，未被匹配", o.ID, w))
		}
	}
	if !any {
		lines = append(lines, "与对侧所有记录均无正长度重叠（端点相接不计）")
	}
	return lines
}

func printResult(as, bs []Record, res Result) {
	fmt.Printf("匹配数 %d，总收益 %d\n", len(res.Matches), res.TotalProfit())
	for _, mt := range res.Matches {
		fmt.Printf("  %s [%d,%d] <-> %s [%d,%d]  重叠 %d\n",
			mt.A.ID, mt.A.Start, mt.A.End, mt.B.ID, mt.B.Start, mt.B.End, mt.Overlap)
	}
	matchedA := make(map[string]string)
	matchedB := make(map[string]string)
	for _, mt := range res.Matches {
		matchedA[mt.A.ID] = mt.B.ID
		matchedB[mt.B.ID] = mt.A.ID
	}
	for _, a := range res.UnmatchedA {
		fmt.Printf("未匹配 A 侧 %s [%d,%d]：\n", a.ID, a.Start, a.End)
		for _, l := range ExplainUnmatched(a, as, bs, matchedB) {
			fmt.Printf("    - %s\n", l)
		}
	}
	for _, b := range res.UnmatchedB {
		fmt.Printf("未匹配 B 侧 %s [%d,%d]：\n", b.ID, b.Start, b.End)
		for _, l := range ExplainUnmatched(b, bs, as, matchedA) {
			fmt.Printf("    - %s\n", l)
		}
	}
}

func main() {
	// 反例场景：长区间 A1 覆盖多个短区间 B1..B3。
	// 若贪心地把 A1 配给重叠最大的 B3，A2 将无对象可配（只剩 1 对）；
	// 最优是 A1-B1、A2-B3，共 2 对。
	// A3 与 B4 端点相接（End==Start），不算重叠，应双双落选并给出证据。
	as := []Record{
		{ID: "A1", Start: 0, End: 100},
		{ID: "A2", Start: 89, End: 120},
		{ID: "A3", Start: 200, End: 210},
	}
	bs := []Record{
		{ID: "B1", Start: 0, End: 30},
		{ID: "B2", Start: 30, End: 60},
		{ID: "B3", Start: 60, End: 90},
		{ID: "B4", Start: 210, End: 220},
	}

	fmt.Println("== 反例场景：长区间覆盖多个短区间 ==")
	res := Solve(as, bs)
	printResult(as, bs, res)
	if c, p := bruteForce(as, bs); c != len(res.Matches) || p != res.TotalProfit() {
		fmt.Printf("不一致！暴力枚举得到 (%d, %d)\n", c, p)
	}

	// 小数据随机交叉核对：求解器 vs 暴力枚举。
	fmt.Println("\n== 随机交叉核对（求解器 vs 暴力枚举） ==")
	rng := rand.New(rand.NewSource(42))
	trials := 3000
	for trial := 0; trial < trials; trial++ {
		n := rng.Intn(5) + 1
		m := rng.Intn(5) + 1
		ra := make([]Record, n)
		rb := make([]Record, m)
		for i := range ra {
			s := int64(rng.Intn(20))
			ra[i] = Record{ID: fmt.Sprintf("a%d", i), Start: s, End: s + 1 + int64(rng.Intn(12))}
		}
		for j := range rb {
			s := int64(rng.Intn(20))
			rb[j] = Record{ID: fmt.Sprintf("b%d", j), Start: s, End: s + 1 + int64(rng.Intn(12))}
		}
		got := Solve(ra, rb)
		ec, ep := bruteForce(ra, rb)
		if len(got.Matches) != ec || got.TotalProfit() != ep {
			fmt.Printf("第 %d 轮不一致：求解器 (%d,%d)，暴力 (%d,%d)\nA=%v\nB=%v\n",
				trial, len(got.Matches), got.TotalProfit(), ec, ep, ra, rb)
			return
		}
	}
	fmt.Printf("%d 轮随机用例全部一致\n", trials)
}
