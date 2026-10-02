// Package matcher 实现带权二分图的最大权匹配（Kuhn–Munkres / 匈牙利算法）。
//
// 模型：左侧为工人（worker），右侧为任务（task），边权表示产值。
// 目标：求一组互不相邻的边，使全局总产值最大。允许工人或任务未匹配
// （未匹配等价于连接权值为 0 的虚拟节点，因此不存在的边与 0 权边
// 对最优总产值等价）。
//
// 除一次性求解 Match 外，还支持动态操作后的增量修复 Rematch：
//   - UpdateEdge                调整单条边权
//   - MaskWorker / MaskTask     屏蔽节点（如工人临时请假下线）
//   - UnmaskWorker / UnmaskTask 恢复节点
//
// 增量修复保持对偶可行性（顶标 lx/ly），仅局部重置受影响顶点的顶标，
// 断开不再紧绷的匹配边，再从失配顶点出发寻找交错增广轨，
// 从而快速切换到次优匹配，避免全量重算。
//
// 边权取值范围建议 |w| <= 1<<50，以保证顶标运算不溢出。
package matcher

import "math"

// Pair 表示一对匹配及其边权（产值）。
type Pair struct {
	Worker int
	Task   int
	Weight int64
}

// Result 为匹配输出。
type Result struct {
	Pairs            []Pair // 匹配成功的 (工人, 任务, 产值)
	Total            int64  // 全局总产值
	UnmatchedWorkers []int  // 未匹配的工人（不含被屏蔽者）
	UnmatchedTasks   []int  // 未匹配的任务（不含被屏蔽者）
	OfflineWorkers   []int  // 被屏蔽（下线）的工人
	OfflineTasks     []int  // 被屏蔽的任务
	// WorkerOpportunity / TaskOpportunity 记录每个未匹配顶点的机会成本：
	// 该顶点让渡匹配机会所放弃的最大产值（其到对侧未屏蔽顶点的最大边权，
	// 无边时为 0）。
	WorkerOpportunity map[int]int64
	TaskOpportunity   map[int]int64
	// MinOpportunityCost 为所有未匹配顶点机会成本的最小值；无未匹配顶点时为 0。
	MinOpportunityCost int64
}

// Matcher 维护一个工人×任务的带权二分图及其最优匹配状态。
type Matcher struct {
	workers int
	tasks   int
	// size 为补全方阵的规模 workers+tasks：左侧含 tasks 个虚拟工人、
	// 右侧含 workers 个虚拟任务（权 0），保证每个真实顶点都能
	// “逃”到虚拟节点，从而允许其在最优解中保持未匹配。
	size int

	has     [][]bool  // 边是否存在 workers×tasks
	w       [][]int64 // 原始边权 workers×tasks（仅 has 为真时有效）
	a       [][]int64 // 实际参与运算的 size×size 方阵：边存在且两端未屏蔽时取 w，否则为 0
	maskedW []bool
	maskedT []bool

	lx, ly []int64 // 顶标（对偶变量），不变式：lx[u]+ly[v] >= a[u][v]
	matchL []int   // 左点 -> 右点，-1 表示未匹配
	matchR []int   // 右点 -> 左点，-1 表示未匹配
	solved bool    // 是否已完成首次求解
}

// New 创建 workers×tasks 的匹配器，初始没有任何边。
func New(workers, tasks int) *Matcher {
	if workers < 0 || tasks < 0 {
		panic("matcher: negative size")
	}
	size := workers + tasks
	m := &Matcher{
		workers: workers,
		tasks:   tasks,
		size:    size,
		maskedW: make([]bool, workers),
		maskedT: make([]bool, tasks),
	}
	m.has = make([][]bool, workers)
	m.w = make([][]int64, workers)
	for u := 0; u < workers; u++ {
		m.has[u] = make([]bool, tasks)
		m.w[u] = make([]int64, tasks)
	}
	m.a = make([][]int64, size)
	for i := range m.a {
		m.a[i] = make([]int64, size) // 零值即 0：虚拟节点边与无边均为 0
	}
	return m
}

// SetEdge 设置边权（不触发重算，供初始建图批量调用）。
func (m *Matcher) SetEdge(worker, task int, weight int64) {
	m.checkVertex(worker, task)
	m.has[worker][task] = true
	m.w[worker][task] = weight
	if !m.maskedW[worker] && !m.maskedT[task] {
		m.a[worker][task] = weight
	}
}

// RemoveEdge 删除一条边（不触发重算）。
func (m *Matcher) RemoveEdge(worker, task int) {
	m.checkVertex(worker, task)
	m.has[worker][task] = false
	if !m.maskedW[worker] && !m.maskedT[task] {
		m.a[worker][task] = 0
	}
}

// UpdateEdge 调整单条边权并增量重算，返回最新匹配结果。
func (m *Matcher) UpdateEdge(worker, task int, weight int64) Result {
	m.SetEdge(worker, task, weight)
	return m.Rematch()
}

// MaskWorker 屏蔽工人（如临时请假下线）并增量重算，快速切换到次优匹配。
func (m *Matcher) MaskWorker(worker int) Result {
	m.checkWorker(worker)
	m.maskedW[worker] = true
	m.rebuildRow(worker)
	return m.Rematch()
}

// UnmaskWorker 恢复工人并增量重算。
func (m *Matcher) UnmaskWorker(worker int) Result {
	m.checkWorker(worker)
	m.maskedW[worker] = false
	m.rebuildRow(worker)
	return m.Rematch()
}

// MaskTask 屏蔽任务并增量重算。
func (m *Matcher) MaskTask(task int) Result {
	m.checkTask(task)
	m.maskedT[task] = true
	for u := 0; u < m.workers; u++ {
		m.a[u][task] = 0
	}
	return m.Rematch()
}

// UnmaskTask 恢复任务并增量重算。
func (m *Matcher) UnmaskTask(task int) Result {
	m.checkTask(task)
	m.maskedT[task] = false
	for u := 0; u < m.workers; u++ {
		m.a[u][task] = m.effective(u, task)
	}
	return m.Rematch()
}

// effective 计算边 (u, v) 参与运算的权值：边存在且两端未屏蔽时取原边权，否则 0。
func (m *Matcher) effective(u, v int) int64 {
	if m.has[u][v] && !m.maskedW[u] && !m.maskedT[v] {
		return m.w[u][v]
	}
	return 0
}

func (m *Matcher) rebuildRow(worker int) {
	for v := 0; v < m.tasks; v++ {
		m.a[worker][v] = m.effective(worker, v)
	}
}

// Match 全量求解最大权匹配（KM 算法，O(n^3)），返回完全匹配结果。
func (m *Matcher) Match() Result {
	n := m.size
	m.lx = make([]int64, n)
	m.ly = make([]int64, n)
	m.matchL = make([]int, n)
	m.matchR = make([]int, n)
	for u := 0; u < n; u++ {
		m.matchL[u] = -1
		m.matchR[u] = -1
		for j := 0; j < n; j++ {
			if m.a[u][j] > m.lx[u] {
				m.lx[u] = m.a[u][j]
			}
		}
	}
	for u := 0; u < n; u++ {
		m.augment(u)
	}
	m.solved = true
	return m.Result()
}

// Rematch 在动态修改后做增量修复：
//  1. 局部重置顶标：边权升高可能破坏对偶可行性 lx[u]+ly[v] >= a[u][v]，
//     把受影响左点的 lx 提升到恰好可行，尽量保留原有紧绷边；
//  2. 断开不再紧绷（lx+ly != a）的匹配边；
//  3. 从每个失配左点出发寻找交错增广轨，切换到新的最优匹配。
func (m *Matcher) Rematch() Result {
	if !m.solved {
		return m.Match()
	}
	n := m.size
	for u := 0; u < n; u++ {
		best := int64(math.MinInt64)
		for j := 0; j < n; j++ {
			if d := m.a[u][j] - m.ly[j]; d > best {
				best = d
			}
		}
		if m.lx[u] < best {
			m.lx[u] = best
		}
	}
	for u := 0; u < n; u++ {
		if j := m.matchL[u]; j >= 0 && m.lx[u]+m.ly[j] != m.a[u][j] {
			m.matchR[j] = -1
			m.matchL[u] = -1
		}
	}
	for u := 0; u < n; u++ {
		if m.matchL[u] == -1 {
			m.augment(u)
		}
	}
	return m.Result()
}

// augment 从自由的左点 root 出发，在相等子图中寻找交错增广轨；
// 若暂时不存在，则按最小 slack 调整顶标扩大相等子图，直至增广成功。
func (m *Matcher) augment(root int) {
	n := m.size
	prev := make([]int, n)  // 右点在交错轨上的前驱左点
	from := make([]int, n)  // 使 slack 取到最小值的左点
	visL := make([]bool, n) // 已进入交错树的左点
	visR := make([]bool, n) // 已进入交错树的右点
	slack := make([]int64, n)
	for j := 0; j < n; j++ {
		slack[j] = m.lx[root] + m.ly[j] - m.a[root][j]
		from[j] = root
	}
	visL[root] = true
	for {
		// 取未访问右点中的最小 slack。
		var delta int64
		jmin := -1
		for j := 0; j < n; j++ {
			if !visR[j] && (jmin == -1 || slack[j] < delta) {
				delta = slack[j]
				jmin = j
			}
		}
		if jmin == -1 {
			panic("matcher: augment failed (no reachable right vertex)")
		}
		if delta > 0 {
			// 调整顶标：交错树内左点 lx-=delta，右点 ly+=delta。
			for u := 0; u < n; u++ {
				if visL[u] {
					m.lx[u] -= delta
				}
			}
			for j := 0; j < n; j++ {
				if visR[j] {
					m.ly[j] += delta
				} else {
					slack[j] -= delta
				}
			}
		}
		j := jmin
		visR[j] = true
		prev[j] = from[j]
		if m.matchR[j] == -1 {
			// 找到增广轨，翻转轨上匹配状态。
			for {
				u := prev[j]
				old := m.matchL[u]
				m.matchL[u] = j
				m.matchR[j] = u
				if u == root {
					break
				}
				j = old
			}
			return
		}
		// j 已匹配，沿匹配边扩展到左点 u，继续生长交错树。
		u := m.matchR[j]
		visL[u] = true
		for j2 := 0; j2 < n; j2++ {
			if !visR[j2] {
				if s := m.lx[u] + m.ly[j2] - m.a[u][j2]; s < slack[j2] {
					slack[j2] = s
					from[j2] = u
				}
			}
		}
	}
}

// Result 读取当前匹配状态，输出完全匹配结果与未匹配最小机会成本。
func (m *Matcher) Result() Result {
	res := Result{
		WorkerOpportunity: make(map[int]int64),
		TaskOpportunity:   make(map[int]int64),
	}
	for u := 0; u < m.workers; u++ {
		if m.maskedW[u] {
			res.OfflineWorkers = append(res.OfflineWorkers, u)
			continue
		}
		j := m.matchL[u]
		if j >= 0 && j < m.tasks && !m.maskedT[j] && m.has[u][j] {
			res.Pairs = append(res.Pairs, Pair{Worker: u, Task: j, Weight: m.w[u][j]})
			res.Total += m.w[u][j]
		} else {
			res.UnmatchedWorkers = append(res.UnmatchedWorkers, u)
			res.WorkerOpportunity[u] = m.bestForgoneWorker(u)
		}
	}
	for v := 0; v < m.tasks; v++ {
		if m.maskedT[v] {
			res.OfflineTasks = append(res.OfflineTasks, v)
			continue
		}
		i := m.matchR[v]
		if !(i >= 0 && i < m.workers && !m.maskedW[i] && m.has[i][v]) {
			res.UnmatchedTasks = append(res.UnmatchedTasks, v)
			res.TaskOpportunity[v] = m.bestForgoneTask(v)
		}
	}
	first := true
	var minCost int64
	consider := func(c int64) {
		if first || c < minCost {
			minCost = c
			first = false
		}
	}
	for _, c := range res.WorkerOpportunity {
		consider(c)
	}
	for _, c := range res.TaskOpportunity {
		consider(c)
	}
	res.MinOpportunityCost = minCost
	return res
}

// bestForgoneWorker 返回未匹配工人 u 放弃的最大产值（到未屏蔽任务的最大边权）。
func (m *Matcher) bestForgoneWorker(u int) int64 {
	var best int64
	for v := 0; v < m.tasks; v++ {
		if !m.maskedT[v] && m.has[u][v] && m.w[u][v] > best {
			best = m.w[u][v]
		}
	}
	return best
}

// bestForgoneTask 返回未匹配任务 v 放弃的最大产值（到未屏蔽工人的最大边权）。
func (m *Matcher) bestForgoneTask(v int) int64 {
	var best int64
	for u := 0; u < m.workers; u++ {
		if !m.maskedW[u] && m.has[u][v] && m.w[u][v] > best {
			best = m.w[u][v]
		}
	}
	return best
}

// Labels 返回当前顶标（对偶变量）的副本，lx 为工人侧、ly 为任务侧（含虚拟节点）。
func (m *Matcher) Labels() (lx, ly []int64) {
	lx = append([]int64(nil), m.lx...)
	ly = append([]int64(nil), m.ly...)
	return lx, ly
}

// Total 返回当前匹配的全局总产值。
func (m *Matcher) Total() int64 {
	var total int64
	for u := 0; u < m.workers; u++ {
		if m.maskedW[u] {
			continue
		}
		if j := m.matchL[u]; j >= 0 && j < m.tasks && !m.maskedT[j] && m.has[u][j] {
			total += m.w[u][j]
		}
	}
	return total
}

func (m *Matcher) checkVertex(worker, task int) {
	m.checkWorker(worker)
	m.checkTask(task)
}

func (m *Matcher) checkWorker(worker int) {
	if worker < 0 || worker >= m.workers {
		panic("matcher: worker index out of range")
	}
}

func (m *Matcher) checkTask(task int) {
	if task < 0 || task >= m.tasks {
		panic("matcher: task index out of range")
	}
}
