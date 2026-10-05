package main

// Step 路径中的一步：应用规则 Rule 后到达状态 State。
type Step struct {
	Rule  string
	State int
}

// Report 保存完整分析结果。
type Report struct {
	Model *Model

	Terminating bool
	TermInput   int    // 可进入循环的最小输入状态
	TermPrefix  []Step // 从输入到循环起点的最短路径
	TermCycle   []Step // 循环本身（最后一步回到循环起点）

	Unique     bool
	UniqInput  int    // 可到达多个最终队列的最小输入状态
	UniqQueueA int    // 分支 A 的最终队列下标
	UniqQueueB int    // 分支 B 的最终队列下标
	UniqPathA  []Step // 到最终队列 A 的最短路径
	UniqPathB  []Step // 到最终队列 B 的最短路径
}

// Analyze 在完整状态空间上做显式状态探索（而非静态规则引用检查），
// 依次判定终止性与最终队列唯一性。
func Analyze(m *Model) *Report {
	n := m.NumStates
	fwdOff, fwdDst := buildCSR(m)
	revOff, revDst := buildReverseCSR(n, fwdOff, fwdDst)

	rep := &Report{Model: m}
	analyzeTermination(m, rep, fwdOff, fwdDst, revOff, revDst)
	analyzeUniqueness(m, rep, fwdOff, revOff, revDst)
	return rep
}

// analyzeTermination 找出所有位于循环上的状态（非平凡强连通分量或自环），
// 反向可达性得到“可能不终止”的状态集合，取编号最小者作为最小输入，
// 并用 BFS 给出最短到达路径与循环本身。
func analyzeTermination(m *Model, rep *Report, fwdOff, fwdDst, revOff, revDst []int32) {
	n := m.NumStates
	comp := kosaraju(n, fwdOff, fwdDst, revOff, revDst)

	compSize := make([]int32, n)
	numComp := int32(0)
	for _, c := range comp {
		compSize[c]++
		if c+1 > numComp {
			numComp = c + 1
		}
	}
	compSize = compSize[:numComp]

	selfLoop := make([]bool, n)
	for v := 0; v < n; v++ {
		for j := fwdOff[v]; j < fwdOff[v+1]; j++ {
			if fwdDst[j] == int32(v) {
				selfLoop[v] = true
				break
			}
		}
	}

	onCycle := make([]bool, n)
	anyCycle := false
	for v := 0; v < n; v++ {
		if compSize[comp[v]] > 1 || selfLoop[v] {
			onCycle[v] = true
			anyCycle = true
		}
	}
	if !anyCycle {
		rep.Terminating = true
		return
	}

	// 反向 BFS：哪些状态可以到达循环。
	reachCycle := make([]bool, n)
	var queue []int
	for v := 0; v < n; v++ {
		if onCycle[v] {
			reachCycle[v] = true
			queue = append(queue, v)
		}
	}
	for head := 0; head < len(queue); head++ {
		v := queue[head]
		for j := revOff[v]; j < revOff[v+1]; j++ {
			w := int(revDst[j])
			if !reachCycle[w] {
				reachCycle[w] = true
				queue = append(queue, w)
			}
		}
	}

	rep.Terminating = false
	input := -1
	for v := 0; v < n; v++ {
		if reachCycle[v] {
			input = v
			break
		}
	}
	rep.TermInput = input
	prefix, entry := m.shortestPath(input, -1, nil, func(s int) bool { return onCycle[s] })
	rep.TermPrefix = prefix
	rep.TermCycle = m.cycleThrough(entry, comp)
}

// analyzeUniqueness 对每个队列做多源反向 BFS，得到每个状态可终止于哪些队列；
// 若某状态可终止于两个以上不同队列，取编号最小者并给出两条最短路径。
func analyzeUniqueness(m *Model, rep *Report, fwdOff, revOff, revDst []int32) {
	n := m.NumStates
	numQueues := len(m.queues)

	terminal := make([]bool, n)
	for v := 0; v < n; v++ {
		terminal[v] = fwdOff[v] == fwdOff[v+1]
	}

	words := (numQueues + 63) / 64
	bits := make([]uint64, n*words)
	for q := 0; q < numQueues; q++ {
		seen := make([]bool, n)
		var queue []int
		for v := 0; v < n; v++ {
			if terminal[v] && m.QueueOf(v) == q {
				seen[v] = true
				queue = append(queue, v)
			}
		}
		for head := 0; head < len(queue); head++ {
			v := queue[head]
			for j := revOff[v]; j < revOff[v+1]; j++ {
				w := int(revDst[j])
				if !seen[w] {
					seen[w] = true
					queue = append(queue, w)
				}
			}
		}
		for _, v := range queue {
			bits[v*words+q/64] |= 1 << (uint(q) % 64)
		}
	}

	rep.Unique = true
	for v := 0; v < n; v++ {
		var qs []int
		for q := 0; q < numQueues; q++ {
			if bits[v*words+q/64]>>(uint(q)%64)&1 == 1 {
				qs = append(qs, q)
			}
		}
		if len(qs) > 1 {
			rep.Unique = false
			rep.UniqInput = v
			rep.UniqQueueA, rep.UniqQueueB = qs[0], qs[1]
			rep.UniqPathA, _ = m.shortestPath(v, -1, nil, func(s int) bool {
				return terminal[s] && m.QueueOf(s) == qs[0]
			})
			rep.UniqPathB, _ = m.shortestPath(v, -1, nil, func(s int) bool {
				return terminal[s] && m.QueueOf(s) == qs[1]
			})
			return
		}
	}
}

// buildCSR 构建前向邻接表的压缩稀疏行表示。
func buildCSR(m *Model) (off, dst []int32) {
	n := m.NumStates
	off = make([]int32, n+1)
	for s := 0; s < n; s++ {
		off[s+1] = off[s] + int32(len(m.Successors(s)))
	}
	dst = make([]int32, off[n])
	pos := make([]int32, n)
	copy(pos, off[:n])
	for s := 0; s < n; s++ {
		for _, tr := range m.Successors(s) {
			dst[pos[s]] = int32(tr.Next)
			pos[s]++
		}
	}
	return off, dst
}

// buildReverseCSR 由前向邻接表构建反向邻接表。
func buildReverseCSR(n int, off, dst []int32) (roff, rdst []int32) {
	roff = make([]int32, n+1)
	for _, w := range dst {
		roff[w+1]++
	}
	for i := 0; i < n; i++ {
		roff[i+1] += roff[i]
	}
	rdst = make([]int32, len(dst))
	pos := make([]int32, n)
	copy(pos, roff[:n])
	for v := 0; v < n; v++ {
		for j := off[v]; j < off[v+1]; j++ {
			w := dst[j]
			rdst[pos[w]] = int32(v)
			pos[w]++
		}
	}
	return roff, rdst
}

// kosaraju 迭代版 Kosaraju 算法，返回每个状态所属的强连通分量编号。
func kosaraju(n int, fwdOff, fwdDst, revOff, revDst []int32) []int32 {
	visited := make([]bool, n)
	order := make([]int32, 0, n)
	type frame struct {
		v  int32
		ci int32
	}
	for s := 0; s < n; s++ {
		if visited[s] {
			continue
		}
		visited[s] = true
		stack := []frame{{int32(s), fwdOff[s]}}
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.ci < fwdOff[f.v+1] {
				w := fwdDst[f.ci]
				f.ci++
				if !visited[w] {
					visited[w] = true
					stack = append(stack, frame{w, fwdOff[w]})
				}
			} else {
				order = append(order, f.v)
				stack = stack[:len(stack)-1]
			}
		}
	}
	comp := make([]int32, n)
	for i := range comp {
		comp[i] = -1
	}
	var nc int32
	for i := len(order) - 1; i >= 0; i-- {
		s := order[i]
		if comp[s] >= 0 {
			continue
		}
		stack := []int32{s}
		comp[s] = nc
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for j := revOff[v]; j < revOff[v+1]; j++ {
				w := revDst[j]
				if comp[w] < 0 {
					comp[w] = nc
					stack = append(stack, w)
				}
			}
		}
		nc++
	}
	return comp
}

// shortestPath 用 BFS 求从 src 到任一满足 isTarget 状态的最短路径。
// restrictComp >= 0 时只允许经过该强连通分量内的状态。
// 返回路径步骤（不含起点）与目标状态；不可达时目标为 -1。
func (m *Model) shortestPath(src int, restrictComp int32, comp []int32, isTarget func(int) bool) ([]Step, int) {
	if isTarget(src) {
		return nil, src
	}
	parent := make([]int32, m.NumStates)
	ruleUsed := make([]int32, m.NumStates)
	for i := range parent {
		parent[i] = -1
	}
	parent[src] = int32(src)
	queue := []int{src}
	target := -1
	for head := 0; head < len(queue) && target < 0; head++ {
		v := queue[head]
		for _, tr := range m.Successors(v) {
			w := tr.Next
			if restrictComp >= 0 && comp[w] != restrictComp {
				continue
			}
			if parent[w] != -1 {
				continue
			}
			parent[w] = int32(v)
			ruleUsed[w] = int32(tr.Rule)
			if isTarget(w) {
				target = w
				break
			}
			queue = append(queue, w)
		}
	}
	if target < 0 {
		return nil, -1
	}
	var rev []Step
	for v := target; v != src; v = int(parent[v]) {
		rev = append(rev, Step{Rule: m.rules[ruleUsed[v]].name, State: v})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, target
}

// cycleThrough 在 t 所在的强连通分量内构造一条从 t 出发回到 t 的循环路径。
func (m *Model) cycleThrough(t int, comp []int32) []Step {
	c := comp[t]
	for _, tr := range m.Successors(t) {
		if tr.Next == t {
			return []Step{{Rule: m.rules[tr.Rule].name, State: t}}
		}
	}
	parent := make([]int32, m.NumStates)
	ruleUsed := make([]int32, m.NumStates)
	for i := range parent {
		parent[i] = -1
	}
	parent[t] = int32(t)
	var queue []int
	for _, tr := range m.Successors(t) {
		w := tr.Next
		if comp[w] != c || parent[w] != -1 {
			continue
		}
		parent[w] = int32(t)
		ruleUsed[w] = int32(tr.Rule)
		queue = append(queue, w)
	}
	for head := 0; head < len(queue); head++ {
		v := queue[head]
		for _, tr := range m.Successors(v) {
			w := tr.Next
			if comp[w] != c {
				continue
			}
			if w == t {
				rev := []Step{{Rule: m.rules[tr.Rule].name, State: t}}
				for u := v; u != t; u = int(parent[u]) {
					rev = append(rev, Step{Rule: m.rules[ruleUsed[u]].name, State: u})
				}
				for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
					rev[i], rev[j] = rev[j], rev[i]
				}
				return rev
			}
			if parent[w] == -1 {
				parent[w] = int32(v)
				ruleUsed[w] = int32(tr.Rule)
				queue = append(queue, w)
			}
		}
	}
	return nil
}
