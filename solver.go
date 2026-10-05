package main

// 全局精确求解：在所有约束（唯一键 / 外键 / 行间冲突）上联合做分支定界，
// 最小化总删除权重。不会按约束类型分别修复再拼接局部解。

const (
	undecided = iota
	kept
	deleted
)

const maxNodes = 20000000 // 超出后退回当前最优解并标记非最优（25 行内几乎不会触发）

type Result struct {
	State         []int
	KeptRows      []int
	DeletedRows   []int
	KeptWeight    int64
	DeletedWeight int64
	Optimal       bool // false 表示搜索被截断，结果为当前最优可行解
}

type solver struct {
	m              *Model
	state          []int
	keptWeight     int64
	bestKeptWeight int64
	bestState      []int
	nodes          int
	truncated      bool
}

func Solve(m *Model) *Result {
	s := &solver{m: m, state: make([]int, len(m.Rows)), bestKeptWeight: -1}
	s.propagate() // 预处理：悬空外键行直接删除
	s.search()
	res := &Result{
		State:         s.bestState,
		KeptWeight:    s.bestKeptWeight,
		DeletedWeight: m.TotalWeight - s.bestKeptWeight,
		Optimal:       !s.truncated,
	}
	for i, st := range s.bestState {
		if st == kept {
			res.KeptRows = append(res.KeptRows, i)
		} else {
			res.DeletedRows = append(res.DeletedRows, i)
		}
	}
	return res
}

func (s *solver) set(i, v int) {
	s.state[i] = v
	if v == kept {
		s.keptWeight += s.m.Rows[i].Weight
	}
}

// propagate 做不动点约束传播，返回 false 表示当前分支矛盾。
func (s *solver) propagate() bool {
	for {
		changed := false
		// 唯一键：同组至多保留一行
		for _, g := range s.m.Groups {
			hasKept := false
			for _, mi := range g.Members {
				if s.state[mi] == kept {
					if hasKept {
						return false
					}
					hasKept = true
				}
			}
			if hasKept {
				for _, mi := range g.Members {
					if s.state[mi] == undecided {
						s.set(mi, deleted)
						changed = true
					}
				}
			}
		}
		// 行间冲突：同集合至多保留一行
		for _, c := range s.m.Conflicts {
			hasKept := false
			for _, mi := range c.Rows {
				if s.state[mi] == kept {
					if hasKept {
						return false
					}
					hasKept = true
				}
			}
			if hasKept {
				for _, mi := range c.Rows {
					if s.state[mi] == undecided {
						s.set(mi, deleted)
						changed = true
					}
				}
			}
		}
		// 外键：子行保留 ⇒ 至少一个候选父行保留；父行全部删除 ⇒ 子行级联删除
		for fi := range s.m.FKs {
			for child, cands := range s.m.fkCandidates[fi] {
				if cands == nil || s.state[child] == deleted {
					continue
				}
				alive := 0
				lastAlive := -1
				anyKept := false
				for _, p := range cands {
					if s.state[p] != deleted {
						alive++
						lastAlive = p
					}
					if s.state[p] == kept {
						anyKept = true
					}
				}
				if s.state[child] == kept {
					if alive == 0 {
						return false
					}
					if !anyKept && alive == 1 {
						s.set(lastAlive, kept)
						changed = true
					}
				} else if alive == 0 {
					s.set(child, deleted) // 级联删除
					changed = true
				}
			}
		}
		if !changed {
			return true
		}
	}
}

// bound 计算当前节点可达成保留权重的上界。
// 对每个唯一键组 / 冲突集合，未定成员中至多保留一个，且每行权重至多被扣减一次，
// 因此是合法上界。
func (s *solver) bound() int64 {
	ub := s.keptWeight
	charged := make([]bool, len(s.m.Rows))
	for i := range s.m.Rows {
		if s.state[i] == undecided {
			ub += s.m.Rows[i].Weight
		}
	}
	for _, g := range s.m.Groups {
		var sum, maxW int64
		for _, mi := range g.Members {
			if s.state[mi] == undecided && !charged[mi] {
				charged[mi] = true
				sum += s.m.Rows[mi].Weight
				if s.m.Rows[mi].Weight > maxW {
					maxW = s.m.Rows[mi].Weight
				}
			}
		}
		ub -= sum - maxW
	}
	for _, c := range s.m.Conflicts {
		var sum, maxW int64
		for _, mi := range c.Rows {
			if s.state[mi] == undecided && !charged[mi] {
				charged[mi] = true
				sum += s.m.Rows[mi].Weight
				if s.m.Rows[mi].Weight > maxW {
					maxW = s.m.Rows[mi].Weight
				}
			}
		}
		ub -= sum - maxW
	}
	return ub
}

// pickRow 选择权重最大的未定行（平局取下标小者），保证确定性。
func (s *solver) pickRow() int {
	best := -1
	for i := range s.m.Rows {
		if s.state[i] != undecided {
			continue
		}
		if best < 0 || s.m.Rows[i].Weight > s.m.Rows[best].Weight {
			best = i
		}
	}
	return best
}

func (s *solver) search() {
	if s.truncated {
		return
	}
	s.nodes++
	if s.nodes > maxNodes {
		s.truncated = true
		return
	}
	if s.bound() <= s.bestKeptWeight {
		return
	}
	idx := s.pickRow()
	if idx < 0 {
		if s.keptWeight > s.bestKeptWeight {
			s.bestKeptWeight = s.keptWeight
			s.bestState = append([]int(nil), s.state...)
		}
		return
	}
	saved := append([]int(nil), s.state...)
	savedKW := s.keptWeight

	// 分支一：保留该行（贪心优先，快速获得优质可行解）
	s.set(idx, kept)
	if s.propagate() {
		s.search()
	}

	// 分支二：删除该行
	s.state = append([]int(nil), saved...)
	s.keptWeight = savedKW
	s.set(idx, deleted)
	if s.propagate() {
		s.search()
	}

	s.state = saved
	s.keptWeight = savedKW
}
