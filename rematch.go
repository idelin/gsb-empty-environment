// Package rematch 解决“部分边失效后的员工-岗位重新匹配”问题。
//
// 问题描述：给定员工集合、岗位集合、当前可用的员工-岗位边（部分原有边已失效），
// 以及原来的匹配方案（original[e] 为员工 e 原来的岗位，-1 表示原本未分配），
// 需要求一个新的匹配，依次满足三个目标：
//
//  1. 匹配上的员工人数最多；
//  2. 在满足 1 的前提下，“岗位变更”次数最少。
//     岗位变更的定义：原本已分配岗位的员工，最终岗位与原来不同（包括失去岗位）。
//     原本未分配的员工获得岗位不算岗位变更；
//  3. 在满足 1、2 的前提下，按员工顺序的岗位向量字典序最小。
//     向量第 e 个分量是员工 e 的岗位编号，未匹配者取哨兵值 numPositions
//     （即大于任何真实岗位编号，因此越靠前的员工越优先获得小编号真实岗位）。
//
// 输出除新匹配外，还包括失配人员（岗位发生变化的员工）以及调整链
// （新旧匹配对称差分解出的交替路径 / 环）。
package rematch

// Unassigned 表示“未分配岗位”的哨兵值。
const Unassigned = -1

// Move 描述一名员工的岗位变动。From / To 为岗位编号，Unassigned 表示未分配。
type Move struct {
	Employee int
	From     int
	To       int
}

// Chain 是一条调整链：新旧匹配对称差中的一条交替路径或交替环。
// Moves 中相邻两名员工共享同一岗位（前者的 To 等于后者的 From），
// 即前者“顶替”了后者，形成连锁调整。Cyclic 为 true 表示成环
// （链尾员工的 To 等于链首员工的 From）。
type Chain struct {
	Moves  []Move
	Cyclic bool
}

// Result 是重新匹配的结果。
type Result struct {
	// Assignment[e] 为员工 e 的新岗位，Unassigned 表示未匹配。
	Assignment []int
	// Moves 列出所有岗位发生变化的员工（失配人员），按员工编号升序。
	Moves []Move
	// Chains 为新旧匹配对称差分解出的调整链。
	Chains []Chain
}

// 内部哨兵：forced[e] 的取值。
const (
	notForced  = -1 // 未固定
	forceDummy = -2 // 固定为“不分配”
)

// ---------- 最小费用流 ----------

type mfEdge struct {
	to   int
	rev  int
	cap  int
	cost int
}

type minCostFlow struct {
	g [][]mfEdge
}

func newMinCostFlow(n int) *minCostFlow {
	return &minCostFlow{g: make([][]mfEdge, n)}
}

func (f *minCostFlow) addEdge(from, to, cap, cost int) {
	f.g[from] = append(f.g[from], mfEdge{to: to, rev: len(f.g[to]), cap: cap, cost: cost})
	f.g[to] = append(f.g[to], mfEdge{to: from, rev: len(f.g[from]) - 1, cap: 0, cost: -cost})
}

const inf = int(^uint(0) >> 2)

// flow 从 s 到 t 求流量为 need 的最小费用流。
// 返回 (实际流量, 费用)；所有费用非负，使用势 + Dijkstra。
func (f *minCostFlow) flow(s, t, need int) (int, int) {
	n := len(f.g)
	pot := make([]int, n)
	totalFlow, totalCost := 0, 0
	prevV := make([]int, n)
	prevE := make([]int, n)
	dist := make([]int, n)
	for totalFlow < need {
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		// 节点数少，使用 O(V^2) 的 Dijkstra。
		used := make([]bool, n)
		for {
			v := -1
			for i := 0; i < n; i++ {
				if !used[i] && dist[i] < inf && (v == -1 || dist[i] < dist[v]) {
					v = i
				}
			}
			if v == -1 {
				break
			}
			used[v] = true
			for ei, e := range f.g[v] {
				if e.cap > 0 {
					nd := dist[v] + e.cost + pot[v] - pot[e.to]
					if dist[e.to] > nd {
						dist[e.to] = nd
						prevV[e.to] = v
						prevE[e.to] = ei
					}
				}
			}
		}
		if dist[t] == inf {
			break
		}
		for v := 0; v < n; v++ {
			if dist[v] < inf {
				pot[v] += dist[v]
			}
		}
		d := need - totalFlow
		for v := t; v != s; v = prevV[v] {
			if c := f.g[prevV[v]][prevE[v]].cap; c < d {
				d = c
			}
		}
		totalFlow += d
		totalCost += d * pot[t]
		for v := t; v != s; v = prevV[v] {
			e := &f.g[prevV[v]][prevE[v]]
			e.cap -= d
			f.g[e.to][e.rev].cap += d
		}
	}
	return totalFlow, totalCost
}

// ---------- 求解器 ----------

type solver struct {
	n      int     // 员工数
	m      int     // 岗位数
	adj    [][]int // 每个员工当前可用的岗位（边失效后）
	orig   []int   // 原岗位，Unassigned 表示原本未分配
	forced []int   // 字典序阶段逐员工固定的选择：notForced / forceDummy / 岗位编号
	w0     int     // 未匹配的惩罚权重（大于最大变更数，保证人数优先）
}

// buildCost 计算当前 forced 约束下的最小费用。
// 费用 = w0*未匹配人数 + 岗位变更次数。
// 返回 (费用, 是否可行)。
func (s *solver) buildCost() (int, bool) {
	n, m := s.n, s.m
	// 节点：0=源, 1..n=员工, 1+n..n+m=岗位, 1+n+m..2n+m=虚拟岗位(每人一个), 汇点
	src := 0
	empBase := 1
	posBase := empBase + n
	dumBase := posBase + m
	sink := dumBase + n
	f := newMinCostFlow(sink + 1)
	for e := 0; e < n; e++ {
		f.addEdge(src, empBase+e, 1, 0)
	}
	for p := 0; p < m; p++ {
		f.addEdge(posBase+p, sink, 1, 0)
	}
	for e := 0; e < n; e++ {
		f.addEdge(dumBase+e, sink, 1, 0)
	}
	for e := 0; e < n; e++ {
		switch s.forced[e] {
		case notForced:
			for _, p := range s.adj[e] {
				cost := 0
				if s.orig[e] != Unassigned && s.orig[e] != p {
					cost = 1
				}
				f.addEdge(empBase+e, posBase+p, 1, cost)
			}
			dumCost := s.w0
			if s.orig[e] != Unassigned {
				dumCost++ // 原本有岗位却失去，算一次变更
			}
			f.addEdge(empBase+e, dumBase+e, 1, dumCost)
		case forceDummy:
			dumCost := s.w0
			if s.orig[e] != Unassigned {
				dumCost++
			}
			f.addEdge(empBase+e, dumBase+e, 1, dumCost)
		default:
			p := s.forced[e]
			cost := 0
			if s.orig[e] != Unassigned && s.orig[e] != p {
				cost = 1
			}
			f.addEdge(empBase+e, posBase+p, 1, cost)
		}
	}
	fl, cost := f.flow(src, sink, n)
	if fl < n {
		return 0, false
	}
	return cost, true
}

// solve 求出满足三级目标的最优匹配。
func (s *solver) solve() []int {
	s.forced = make([]int, s.n)
	for e := range s.forced {
		s.forced[e] = notForced
	}
	base, _ := s.buildCost()
	// 第三级目标：按员工顺序，逐员工尝试字典序最小的可行选择。
	for e := 0; e < s.n; e++ {
		fixed := false
		for p := 0; p < s.m; p++ {
			if !s.hasEdge(e, p) {
				continue
			}
			s.forced[e] = p
			if c, ok := s.buildCost(); ok && c == base {
				fixed = true
				break
			}
		}
		if !fixed {
			s.forced[e] = forceDummy
			// 由阶段一可知必能取到 base，无需再校验。
		}
	}
	// 由 forced 结果重建匹配。
	assign := make([]int, s.n)
	used := make([]bool, s.m)
	for e := 0; e < s.n; e++ {
		if s.forced[e] >= 0 {
			assign[e] = s.forced[e]
			used[s.forced[e]] = true
		} else {
			assign[e] = Unassigned
		}
	}
	return assign
}

func (s *solver) hasEdge(e, p int) bool {
	for _, q := range s.adj[e] {
		if q == p {
			return true
		}
	}
	return false
}

// ---------- 调整链分解 ----------

// 对称差图中的节点：员工节点为 e，岗位节点为 n+p。
func buildChains(n int, orig, assign []int) []Chain {
	type halfEdge struct {
		to    int
		isNew bool
	}
	adj := make(map[int][]halfEdge)
	addHalf := func(a, b int, isNew bool) {
		adj[a] = append(adj[a], halfEdge{to: b, isNew: isNew})
	}
	for e := 0; e < n; e++ {
		o, w := orig[e], assign[e]
		if o == w {
			continue
		}
		if o != Unassigned {
			addHalf(e, n+o, false)
			addHalf(n+o, e, false)
		}
		if w != Unassigned {
			addHalf(e, n+w, true)
			addHalf(n+w, e, true)
		}
	}
	// 访问标记：以无序节点对标记边。
	type pair struct{ a, b int }
	visited := make(map[pair]bool)
	mark := func(a, b int) { visited[pair{a, b}] = true; visited[pair{b, a}] = true }
	seen := func(a, b int) bool { return visited[pair{a, b}] }

	moveOf := func(e int) Move {
		return Move{Employee: e, From: orig[e], To: assign[e]}
	}

	// 从 start 出发沿未访问边走，返回途经的员工序列（有序）。
	walk := func(start int) ([]int, bool) {
		var emps []int
		cur := start
		for {
			if cur < n {
				emps = append(emps, cur)
			}
			next := -1
			for _, h := range adj[cur] {
				if !seen(cur, h.to) {
					next = h.to
					break
				}
			}
			if next == -1 {
				return emps, false
			}
			mark(cur, next)
			if next == start {
				return emps, true
			}
			cur = next
		}
	}

	var chains []Chain
	// 统一链方向：使相邻员工满足 前者.To == 后者.From（顶岗方向）。
	normalize := func(emps []int) {
		if len(emps) >= 2 && assign[emps[0]] != orig[emps[1]] {
			for i, j := 0, len(emps)-1; i < j; i, j = i+1, j-1 {
				emps[i], emps[j] = emps[j], emps[i]
			}
		}
	}
	// 先处理路径（端点为度 1 的节点）。
	var endpoints []int
	for v := range adj {
		if len(adj[v]) == 1 {
			endpoints = append(endpoints, v)
		}
	}
	// 排序保证输出确定性。
	sortInts(endpoints)
	for _, v := range endpoints {
		if len(adj[v]) == 0 || seen(v, adj[v][0].to) {
			continue
		}
		emps, _ := walk(v)
		normalize(emps)
		chains = append(chains, makeChain(emps, false, moveOf))
	}
	// 再处理环。
	var nodes []int
	for v := range adj {
		nodes = append(nodes, v)
	}
	sortInts(nodes)
	for _, v := range nodes {
		hasUnvisited := false
		for _, h := range adj[v] {
			if !seen(v, h.to) {
				hasUnvisited = true
				break
			}
		}
		if !hasUnvisited {
			continue
		}
		emps, _ := walk(v)
		normalize(emps)
		chains = append(chains, makeChain(emps, true, moveOf))
	}
	return chains
}

func makeChain(emps []int, cyclic bool, moveOf func(int) Move) Chain {
	c := Chain{Cyclic: cyclic}
	for _, e := range emps {
		c.Moves = append(c.Moves, moveOf(e))
	}
	return c
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// ---------- 对外接口 ----------

// Reassign 求解重新匹配问题。
//
// numEmployees 为员工数（编号 0..numEmployees-1），numPositions 为岗位数
// （编号 0..numPositions-1）。edges 为当前可用的员工-岗位边（已剔除失效边），
// 可含重复，顺序无关。original[e] 为员工 e 原来的岗位，Unassigned 表示原本未分配；
// 原岗位对应的边可以已经失效（不在 edges 中）。
func Reassign(numEmployees, numPositions int, edges [][2]int, original []int) Result {
	n, m := numEmployees, numPositions
	adj := make([][]int, n)
	seen := make([]map[int]bool, n)
	for e := 0; e < n; e++ {
		seen[e] = make(map[int]bool)
	}
	for _, e := range edges {
		emp, pos := e[0], e[1]
		if emp < 0 || emp >= n || pos < 0 || pos >= m {
			continue
		}
		if !seen[emp][pos] {
			seen[emp][pos] = true
			adj[emp] = append(adj[emp], pos)
		}
	}
	for e := 0; e < n; e++ {
		sortInts(adj[e])
	}
	orig := make([]int, n)
	copy(orig, original)
	for e := 0; e < n; e++ {
		if orig[e] < 0 || orig[e] >= m {
			orig[e] = Unassigned
		}
	}

	s := &solver{n: n, m: m, adj: adj, orig: orig, w0: n + 1}
	assign := s.solve()

	res := Result{Assignment: assign}
	for e := 0; e < n; e++ {
		if orig[e] != assign[e] {
			res.Moves = append(res.Moves, Move{Employee: e, From: orig[e], To: assign[e]})
		}
	}
	res.Chains = buildChains(n, orig, assign)
	return res
}
