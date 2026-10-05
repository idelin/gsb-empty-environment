package main

import (
	"fmt"
	"sort"
)

// ScheduleEntry 是一个工序实例的排程结果。
type ScheduleEntry struct {
	ID       string `json:"id"`
	Start    int    `json:"start"`
	Finish   int    `json:"finish"`
	IsRework bool   `json:"is_rework"`
	Origin   string `json:"origin"`
}

// Solution 是一份完整排程：每个实例的区间 + 总完工时间。
type Solution struct {
	Entries  []ScheduleEntry
	Makespan int
	Optimal  bool // false 表示搜索被节点上限截断，结果为当前最优但未证实
}

// solver 在决策点上做分支定界：每个决策点枚举“当前可开工实例”的
// 所有资源可行非空子集，配合关键路径下界剪枝，保证得到最短完工方案。
type solver struct {
	acts    []*Activity
	names   []string // 资源名（固定顺序）
	cap     []int
	demand  [][]int // demand[actIdx][resIdx]
	cpTail  []int
	start   []int // -1 表示未排
	finish  []int
	doneCnt int
	best    []int // 最优解的 start 数组
	bestMk  int
	nodes   int
	limit   int // 搜索节点上限，防止病态输入失控
	aborted bool
}

// SolveOptimal 求最短完工计划。实例数 ≤15 时该精确搜索开销可控。
func SolveOptimal(acts []*Activity, capacity ResourceMap) (Solution, error) {
	names := resourceNames(capacity)
	s := &solver{
		acts:   acts,
		names:  names,
		cap:    make([]int, len(names)),
		demand: make([][]int, len(acts)),
		cpTail: make([]int, len(acts)),
		start:  make([]int, len(acts)),
		finish: make([]int, len(acts)),
		bestMk: int(^uint(0) >> 1),
		limit:  4000000,
	}
	for i, n := range names {
		s.cap[i] = capacity[n]
	}
	for _, a := range acts {
		s.start[a.index] = -1
		s.cpTail[a.index] = a.cpTail
		d := make([]int, len(names))
		for i, n := range names {
			d[i] = a.Demands[n]
		}
		s.demand[a.index] = d
	}
	s.search(0)
	if s.best == nil {
		return Solution{}, fmt.Errorf("未找到可行排程（搜索节点 %d）", s.nodes)
	}
	sol := s.solution()
	sol.Optimal = !s.aborted
	return sol, nil
}

func (s *solver) search(t int) {
	if s.aborted {
		return
	}
	s.nodes++
	if s.nodes > s.limit {
		s.aborted = true
		return
	}
	if s.doneCnt == len(s.acts) {
		mk := 0
		for _, f := range s.finish {
			if f > mk {
				mk = f
			}
		}
		if mk < s.bestMk {
			s.bestMk = mk
			s.best = append([]int(nil), s.start...)
		}
		return
	}
	// 下界：任何未排实例 i 的完工不早于 t + cpTail[i]。
	lb := 0
	for _, a := range s.acts {
		if s.start[a.index] < 0 {
			if v := t + s.cpTail[a.index]; v > lb {
				lb = v
			}
		} else if s.finish[a.index] > lb {
			lb = s.finish[a.index]
		}
	}
	if lb >= s.bestMk {
		return
	}

	avail := s.availAt(t)
	eligible := s.eligibleAt(t, avail)
	if len(eligible) == 0 {
		nt, ok := s.nextCompletion(t)
		if !ok {
			return // 有未排实例但无可运行者：只剩依赖未满足，前进到下一完成时刻
		}
		s.search(nt)
		return
	}
	for _, subset := range feasibleSubsets(eligible, s.demand, avail) {
		for _, i := range subset {
			s.start[i] = t
			s.finish[i] = t + s.acts[i].Duration
			s.doneCnt++
		}
		nt, _ := s.nextCompletion(t)
		s.search(nt)
		for _, i := range subset {
			s.start[i] = -1
			s.finish[i] = 0
			s.doneCnt--
		}
	}
	// 延迟分支：刻意闲置到下一完成时刻。某些最优解要求暂不开工
	// （例如把资源留给即将解锁的长链工序）。仅在有实例运行时才有效。
	if nt, running := s.nextCompletion(t); running {
		s.search(nt)
	}
}

// availAt 计算时刻 t 各资源的剩余容量（t 时刻完成的实例已释放资源）。
func (s *solver) availAt(t int) []int {
	avail := append([]int(nil), s.cap...)
	for _, a := range s.acts {
		i := a.index
		if s.start[i] >= 0 && s.start[i] < t && t < s.finish[i] {
			for r, d := range s.demand[i] {
				avail[r] -= d
			}
		}
	}
	return avail
}

// eligibleAt 返回 t 时刻可开工（前驱已完成且资源足够）的未排实例。
func (s *solver) eligibleAt(t int, avail []int) []int {
	out := []int{}
	for _, a := range s.acts {
		i := a.index
		if s.start[i] >= 0 {
			continue
		}
		ready := true
		for _, p := range a.pred {
			if s.start[p] < 0 || s.finish[p] > t {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		fits := true
		for r, d := range s.demand[i] {
			if d > avail[r] {
				fits = false
				break
			}
		}
		if fits {
			out = append(out, i)
		}
	}
	return out
}

// nextCompletion 返回 t 之后最近的实例完成时刻。
func (s *solver) nextCompletion(t int) (int, bool) {
	best := int(^uint(0) >> 1)
	found := false
	for _, a := range s.acts {
		i := a.index
		if s.start[i] >= 0 && s.finish[i] > t && s.finish[i] < best {
			best = s.finish[i]
			found = true
		}
	}
	return best, found
}

// feasibleSubsets 枚举 eligible 的所有资源可行非空子集，大的在前以加速收敛。
func feasibleSubsets(eligible []int, demand [][]int, avail []int) [][]int {
	out := [][]int{}
	var rec func(pos int, chosen []int, used []int)
	rec = func(pos int, chosen []int, used []int) {
		if pos == len(eligible) {
			if len(chosen) > 0 {
				out = append(out, append([]int(nil), chosen...))
			}
			return
		}
		// 不选
		rec(pos+1, chosen, used)
		// 选
		i := eligible[pos]
		ok := true
		for r, d := range demand[i] {
			if used[r]+d > avail[r] {
				ok = false
				break
			}
		}
		if ok {
			nu := append([]int(nil), used...)
			for r, d := range demand[i] {
				nu[r] += d
			}
			rec(pos+1, append(chosen, i), nu)
		}
	}
	rec(0, nil, make([]int, len(avail)))
	sort.Slice(out, func(a, b int) bool { return len(out[a]) > len(out[b]) })
	return out
}

func (s *solver) solution() Solution {
	entries := make([]ScheduleEntry, 0, len(s.acts))
	mk := 0
	for _, a := range s.acts {
		st := s.best[a.index]
		f := st + a.Duration
		if f > mk {
			mk = f
		}
		entries = append(entries, ScheduleEntry{
			ID:       a.ID,
			Start:    st,
			Finish:   f,
			IsRework: a.IsRework,
			Origin:   a.Origin,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Start != entries[j].Start {
			return entries[i].Start < entries[j].Start
		}
		return entries[i].ID < entries[j].ID
	})
	return Solution{Entries: entries, Makespan: mk}
}
