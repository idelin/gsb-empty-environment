package spp

import "sort"

// Assignment 是一个路径分配：每个 AS 映射到它当前选中的路径；
// nil（空切片）表示该 AS 没有可用路由（空选择）。
type Assignment map[int]Path

// Options 控制精确求解器的枚举规模。
type Options struct {
	// MaxAssignments 限制返回的稳态数量；0 表示不限。
	MaxAssignments int
	// MaxBranches 限制搜索分支数，防止病态大图组合爆炸；0 表示不限。
	MaxBranches int
}

// SolveResult 是求解输出。
type SolveResult struct {
	Assignments []Assignment
	// Truncated 为真表示命中 MaxAssignments 或 MaxBranches，结果可能不全。
	Truncated bool
	// Branches 是实际搜索的分支计数。
	Branches int
}

// possibleChoices 返回节点 as 在部分分配下仍可能选中的候选（保持偏好序，
// 末尾附加空选择）。下一跳已确定且不再通告对应扩展的路径被剔除。
func (g *Graph) possibleChoices(as int, partial Assignment) []Path {
	var choices []Path
	for _, p := range g.Preferences(as) {
		if len(p) < 2 {
			continue
		}
		hop := p[1]
		if chosen, ok := partial[hop]; ok {
			if chosen == nil || !chosen.Equal(p[1:]) {
				continue // 邻居已确定不通告该扩展
			}
		}
		choices = append(choices, p)
	}
	return choices
}

// bestAvailable 返回当前已可证明可用的候选中偏好最高者；没有则返回 nil。
// 第二返回值表示是否存在已可证明可用的路径。
func (g *Graph) bestAvailable(as int, partial Assignment) (Path, bool) {
	for _, p := range g.Preferences(as) {
		if len(p) < 2 {
			continue
		}
		hop := p[1]
		chosen, ok := partial[hop]
		if ok && chosen != nil && chosen.Equal(p[1:]) {
			return p, true
		}
	}
	return nil, false
}

// fullyStable 在完整分配上做最终核验：每个 AS 的选择必须恰好是当前所有
// 邻居通告的合法可扩展路径中偏好最高者。
func (g *Graph) fullyStable(a Assignment) bool {
	for _, as := range g.SortedNodes() {
		if as == g.Destination {
			continue
		}
		best, available := g.bestAvailable(as, a)
		chosen := a[as]
		if chosen == nil {
			if available {
				return false
			}
			continue
		}
		if !available || !best.Equal(chosen) {
			return false
		}
	}
	return true
}

// assignmentSnapshot 保存部分分配的深拷贝状态。
type assignmentSnapshot struct {
	data map[int]Path
}

func snapshot(a Assignment) assignmentSnapshot {
	cp := make(map[int]Path, len(a))
	for k, v := range a {
		cp[k] = copyPath(v)
	}
	return assignmentSnapshot{data: cp}
}

func cloneAssigned(m map[int]bool) map[int]bool {
	cp := make(map[int]bool, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func (g *Graph) pickUnassigned(partial Assignment) int {
	var picked int
	bestSize := int(^uint(0) >> 1)
	for _, as := range g.SortedNodes() {
		if as == g.Destination {
			continue
		}
		if _, ok := partial[as]; ok {
			continue
		}
		size := len(g.possibleChoices(as, partial)) + 1 // 含空选择
		if size < bestSize {
			bestSize = size
			picked = as
		}
	}
	return picked
}

// propagate 对当前部分分配做不动点式约束传播，实施两类安全强制：
//
//   - 节点偏好序中的最优路径已可证明可用：任何稳定选择都不可能绕过它，
//     该节点被唯一钉为该最优路径；
//   - 节点没有任何允许路径（或全部候选的下一跳都已明确拒绝通告）：
//     空路由是唯一可能选择，直接钉为空。
//
// 除此之外的“当前最优但依赖未决定邻居”不会被钉死——尚未决定的邻居
// 之后可能通告更优路径。这保证传播不做任何过度剪枝，枚举保持完备。
func (g *Graph) propagate(partial Assignment, assigned map[int]bool) (map[int]bool, bool) {
	forced := map[int]bool{}
	changed := true
	for changed {
		changed = false
		for _, as := range g.SortedNodes() {
			if as == g.Destination || assigned[as] {
				continue
			}
			prefs := g.Preferences(as)
			topAvailable := len(prefs) > 0 && g.pathAvailable(as, prefs[0], partial)
			switch {
			case topAvailable:
				top := prefs[0]
				if cur, ok := partial[as]; ok && (cur == nil || !cur.Equal(top)) {
					return forced, false
				}
				if _, ok := partial[as]; !ok {
					partial[as] = top
					forced[as] = true
					changed = true
				}
			case len(g.possibleChoices(as, partial)) == 0:
				if cur, ok := partial[as]; ok && cur != nil {
					return forced, false
				}
				if _, ok := partial[as]; !ok {
					partial[as] = Path(nil)
					forced[as] = true
					changed = true
				}
			}
		}
	}
	return forced, true
}

// pathAvailable 判断路径 p 的下一跳在当前部分分配下是否已确定通告其扩展。
func (g *Graph) pathAvailable(as int, p Path, partial Assignment) bool {
	if len(p) < 2 {
		return false
	}
	hop := p[1]
	chosen, ok := partial[hop]
	return ok && chosen != nil && chosen.Equal(p[1:])
}

// Solve 精确枚举全部稳定路径分配。
//
// 搜索采用“分支决策 + 约束传播”：每做一个选择即把对其他节点的确定性
// 影响传播到不动点，冲突分支立即剪枝；传播只依赖已确定的下一跳，
// 完备性由对仍有多种可能的节点按其全部候选（含空选择）分支保证。
func (g *Graph) Solve(opts Options) SolveResult {
	result := SolveResult{}

	var dfs func(partial Assignment, assigned map[int]bool) bool
	dfs = func(partial Assignment, assigned map[int]bool) bool {
		if opts.MaxBranches > 0 && result.Branches > opts.MaxBranches {
			result.Truncated = true
			return true
		}
		if opts.MaxAssignments > 0 && len(result.Assignments) >= opts.MaxAssignments {
			result.Truncated = true
			return true
		}

		work := snapshot(partial).data
		forced, ok := g.propagate(work, assigned)
		if !ok {
			return false
		}
		workAssigned := cloneAssigned(assigned)
		for as := range forced {
			workAssigned[as] = true
		}

		if len(work) == len(g.Nodes) {
			if g.fullyStable(work) {
				found := make(Assignment, len(work))
				for as, p := range work {
					found[as] = copyPath(p)
				}
				result.Assignments = append(result.Assignments, found)
			}
			return false
		}

		as := g.pickUnassigned(work)
		choices := g.possibleChoices(as, work)
		choices = append(choices, nil) // 空选择最后尝试
		for _, choice := range choices {
			result.Branches++
			child := snapshot(work).data
			child[as] = choice
			childAssigned := cloneAssigned(workAssigned)
			childAssigned[as] = true
			stop := dfs(child, childAssigned)
			if stop {
				return true
			}
		}
		return false
	}

	dfs(Assignment{g.Destination: {g.Destination}}, map[int]bool{g.Destination: true})

	// 稳态按字典序排序，保证输出确定。
	sort.Slice(result.Assignments, func(i, j int) bool {
		for _, as := range g.SortedNodes() {
			pi, pj := result.Assignments[i][as], result.Assignments[j][as]
			if pi.Key() != pj.Key() {
				return pathLess(pi, pj)
			}
		}
		return false
	})
	return result
}

// Outcome 是三类收敛结局。
type Outcome string

const (
	// OutcomeUnique 唯一稳态：安全收敛。
	OutcomeUnique Outcome = "unique_stable"
	// OutcomeMultiple 多稳态：依赖消息时序的非确定性分裂。
	OutcomeMultiple Outcome = "multiple_stable"
	// OutcomeNone 无稳态：必然永久震荡。
	OutcomeNone Outcome = "no_stable"
)

// Classify 根据稳态数量给出结局分类。
func Classify(count int) Outcome {
	switch {
	case count == 0:
		return OutcomeNone
	case count == 1:
		return OutcomeUnique
	default:
		return OutcomeMultiple
	}
}
