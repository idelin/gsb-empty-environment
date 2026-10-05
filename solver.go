package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// runTask 表示在某一决策时刻正在执行的实例。
type runTask struct {
	idx int
	end int
}

// solver 在“决策时刻选择可行子集启动”的枚举树上做分支定界，
// 该枚举树覆盖全部可行调度（含主动空闲），因此求得最优最短完工时间。
type solver struct {
	n         int
	dur       []int
	demand    [][]int // [instance][resource]
	preds     [][]int
	succs     [][]int
	tail      []int // 从该实例到任一汇点的最长工期链（含自身）
	afterTail []int // 该实例之后的最长工期链（不含自身，汇点为 0）
	caps      []int
	full      uint32

	best       int
	bestStarts []int
	memo       map[string]int
}

func newSolver(insts []Instance, resNames []string, caps map[string]int) *solver {
	n := len(insts)
	idx := make(map[string]int, n)
	for i, inst := range insts {
		idx[inst.ID] = i
	}
	s := &solver{
		n:      n,
		dur:    make([]int, n),
		demand: make([][]int, n),
		preds:  make([][]int, n),
		succs:  make([][]int, n),
		caps:   make([]int, len(resNames)),
		full:   uint32(1)<<uint(n) - 1,
		best:   int(^uint(0) >> 1),
		memo:   make(map[string]int),
	}
	for k, name := range resNames {
		s.caps[k] = caps[name]
	}
	for i, inst := range insts {
		s.dur[i] = inst.Dur
		s.demand[i] = make([]int, len(resNames))
		for k, name := range resNames {
			s.demand[i][k] = inst.Demands[name]
		}
		for _, p := range inst.Preds {
			j := idx[p]
			s.preds[i] = append(s.preds[i], j)
			s.succs[j] = append(s.succs[j], i)
		}
	}
	// 关键链长度（拓扑逆序，图无环已由 expand 保证）。
	s.tail = make([]int, n)
	s.afterTail = make([]int, n)
	order := topoOrder(s.preds)
	for oi := n - 1; oi >= 0; oi-- {
		i := order[oi]
		best := 0
		for _, su := range s.succs[i] {
			if s.tail[su] > best {
				best = s.tail[su]
			}
		}
		s.afterTail[i] = best
		s.tail[i] = s.dur[i] + best
	}
	return s
}

func topoOrder(preds [][]int) []int {
	indeg := make([]int, len(preds))
	for i := range preds {
		indeg[i] = len(preds[i])
	}
	succs := make([][]int, len(preds))
	for i := range preds {
		for _, p := range preds[i] {
			succs[p] = append(succs[p], i)
		}
	}
	var queue []int
	for i := range indeg {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	var order []int
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, su := range succs[i] {
			indeg[su]--
			if indeg[su] == 0 {
				queue = append(queue, su)
			}
		}
	}
	return order
}

// solve 返回每个实例的最优开工时刻与最短完工时间。
func (s *solver) solve() ([]int, int) {
	// 先用贪心生成一个上界，加速分支定界。
	s.best, s.bestStarts = s.greedy()
	starts := make([]int, s.n)
	for i := range starts {
		starts[i] = -1
	}
	s.search(0, 0, nil, starts)
	return s.bestStarts, s.best
}

// greedy 按关键链长度优先的串行调度生成可行解。
func (s *solver) greedy() (int, []int) {
	starts := make([]int, s.n)
	done := make([]bool, s.n)
	finish := make([]int, s.n)
	remaining := s.n
	makespan := 0
	for remaining > 0 {
		// 选当前可开工实例中关键链最长、且能塞进最早可行时刻的。
		bestI, bestT := -1, 0
		for i := 0; i < s.n; i++ {
			if done[i] {
				continue
			}
			est := 0
			ready := true
			for _, p := range s.preds[i] {
				if !done[p] {
					ready = false
					break
				}
				if finish[p] > est {
					est = finish[p]
				}
			}
			if !ready {
				continue
			}
			t := s.earliestFit(i, est, starts, done, finish)
			if bestI == -1 || t < bestT || (t == bestT && s.tail[i] > s.tail[bestI]) {
				bestI, bestT = i, t
			}
		}
		starts[bestI] = bestT
		done[bestI] = true
		finish[bestI] = bestT + s.dur[bestI]
		if finish[bestI] > makespan {
			makespan = finish[bestI]
		}
		remaining--
	}
	return makespan, starts
}

// earliestFit 在已排实例的资源占用下，求实例 i 不早于 est 的最早可插入时刻。
// 可行性只在已排任务的完工时刻发生变化，因此候选时刻为 est 与各完工时刻。
func (s *solver) earliestFit(i, est int, starts []int, done []bool, finish []int) int {
	cands := []int{est}
	for j := 0; j < s.n; j++ {
		if done[j] && finish[j] > est {
			cands = append(cands, finish[j])
		}
	}
	sort.Ints(cands)
	for _, t := range cands {
		if s.fitsAt(i, t, starts, done, finish) {
			return t
		}
	}
	// 不可达：在全部已排任务结束之后必然可插入。
	return cands[len(cands)-1]
}

// fitsAt 检查实例 i 从时刻 t 开始是否在整段执行区间内满足资源容量。
func (s *solver) fitsAt(i, t int, starts []int, done []bool, finish []int) bool {
	points := []int{t}
	end := t + s.dur[i]
	for j := 0; j < s.n; j++ {
		if !done[j] || j == i {
			continue
		}
		if starts[j] > t && starts[j] < end {
			points = append(points, starts[j])
		}
		if finish[j] > t && finish[j] < end {
			points = append(points, finish[j])
		}
	}
	for _, p := range points {
		for k := range s.caps {
			used := s.demand[i][k]
			for j := 0; j < s.n; j++ {
				if !done[j] || j == i {
					continue
				}
				if starts[j] <= p && p < finish[j] {
					used += s.demand[j][k]
				}
			}
			if used > s.caps[k] {
				return false
			}
		}
	}
	return true
}

// search 在时刻 t 做决策：从合格实例中枚举可启动子集（含主动空闲）。
func (s *solver) search(t int, doneMask uint32, running []runTask, starts []int) {
	if len(running) == 0 && doneMask == s.full {
		mk := 0
		for i := 0; i < s.n; i++ {
			if starts[i]+s.dur[i] > mk {
				mk = starts[i] + s.dur[i]
			}
		}
		if mk < s.best {
			s.best = mk
			s.bestStarts = append([]int(nil), starts...)
		}
		return
	}

	remCap := s.remainingCap(running)

	// 下界剪枝：关键链 + 资源工作量。
	lb := 0
	for _, r := range running {
		if v := r.end - t + s.afterTail[r.idx]; v > lb {
			lb = v
		}
	}
	work := make([]int, len(s.caps))
	for _, r := range running {
		for k := range s.caps {
			work[k] += s.demand[r.idx][k] * (r.end - t)
		}
	}
	for i := 0; i < s.n; i++ {
		bit := uint32(1) << uint(i)
		if doneMask&bit != 0 || isRunning(running, i) {
			continue
		}
		ready := true
		for _, p := range s.preds[i] {
			if doneMask&(uint32(1)<<uint(p)) == 0 {
				ready = false
				break
			}
		}
		if ready && s.tail[i] > lb {
			lb = s.tail[i]
		}
		for k := range s.caps {
			work[k] += s.demand[i][k] * s.dur[i]
		}
	}
	for k := range s.caps {
		if s.caps[k] > 0 {
			if v := (work[k] + s.caps[k] - 1) / s.caps[k]; v > lb {
				lb = v
			}
		}
	}
	if t+lb >= s.best {
		return
	}

	// 记忆化支配：同一（完成集, 运行集及其剩余时长）在更早时刻已访问则剪枝。
	key := stateKey(doneMask, running, t)
	if prev, ok := s.memo[key]; ok && prev <= t {
		return
	}
	s.memo[key] = t

	// 合格实例：前驱均已完成。
	var eligible []int
	for i := 0; i < s.n; i++ {
		bit := uint32(1) << uint(i)
		if doneMask&bit != 0 || isRunning(running, i) {
			continue
		}
		ready := true
		for _, p := range s.preds[i] {
			if doneMask&(uint32(1)<<uint(p)) == 0 {
				ready = false
				break
			}
		}
		if ready {
			eligible = append(eligible, i)
		}
	}

	// 枚举可启动子集：从大到小（大子集优先，更快找到优解）；
	// 空子集表示主动空闲，仅当有任务在运行时才有意义。
	for size := len(eligible); size >= 0; size-- {
		if size == 0 && len(running) == 0 {
			break
		}
		s.enumerateSubsets(eligible, size, 0, nil, t, doneMask, running, starts, remCap)
	}
}

// enumerateSubsets 枚举 eligible 中所有大小为 size 且资源可行的子集并递归。
func (s *solver) enumerateSubsets(eligible []int, size, pos int, chosen []int, t int, doneMask uint32, running []runTask, starts []int, remCap []int) {
	if len(chosen) == size {
		s.applyAndAdvance(chosen, t, doneMask, running, starts)
		return
	}
	for i := pos; i <= len(eligible)-(size-len(chosen)); i++ {
		cand := eligible[i]
		fits := true
		for k := range s.caps {
			used := s.demand[cand][k]
			for _, c := range chosen {
				used += s.demand[c][k]
			}
			if used > remCap[k] {
				fits = false
				break
			}
		}
		if fits {
			s.enumerateSubsets(eligible, size, i+1, append(chosen, cand), t, doneMask, running, starts, remCap)
		}
	}
}

// applyAndAdvance 在时刻 t 启动 chosen，推进到下一个完工事件后递归。
func (s *solver) applyAndAdvance(chosen []int, t int, doneMask uint32, running []runTask, starts []int) {
	newRunning := append([]runTask(nil), running...)
	newStarts := starts
	if len(chosen) > 0 {
		newStarts = append([]int(nil), starts...)
		for _, c := range chosen {
			newStarts[c] = t
			newRunning = append(newRunning, runTask{idx: c, end: t + s.dur[c]})
		}
	}
	// 推进到最近的完工时刻。
	next := newRunning[0].end
	for _, r := range newRunning[1:] {
		if r.end < next {
			next = r.end
		}
	}
	newDone := doneMask
	var still []runTask
	for _, r := range newRunning {
		if r.end == next {
			newDone |= uint32(1) << uint(r.idx)
		} else {
			still = append(still, r)
		}
	}
	s.search(next, newDone, still, newStarts)
}

func (s *solver) remainingCap(running []runTask) []int {
	rem := append([]int(nil), s.caps...)
	for _, r := range running {
		for k := range rem {
			rem[k] -= s.demand[r.idx][k]
		}
	}
	return rem
}

func isRunning(running []runTask, idx int) bool {
	for _, r := range running {
		if r.idx == idx {
			return true
		}
	}
	return false
}

func stateKey(doneMask uint32, running []runTask, t int) string {
	pairs := make([]string, 0, len(running))
	for _, r := range running {
		pairs = append(pairs, strconv.Itoa(r.idx)+":"+strconv.Itoa(r.end-t))
	}
	sort.Strings(pairs)
	return fmt.Sprintf("%x|%s", doneMask, strings.Join(pairs, ","))
}
