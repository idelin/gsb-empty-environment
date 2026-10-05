package main

// Solver 用精确分支定界枚举所有匹配,求最大规模弱稳定匹配。
//
// 不打破任何并列:弱稳定性直接按"双方严格更优才算阻挡"判定,
// 因此所得最大规模是精确最优,而非某种并列打破方式下的最优。
//
// 剪枝的安全性依据(沿任一搜索分支):
//   - 申请者按固定顺序决策,已决策申请者的指派不再改变;
//   - 医院的在院者集合只增不减,满员后不再变化;
//   - 因此"医院已满"的阻挡对一旦出现便永久存在,可安全剪枝;
//   - 若医院有空位且剩余未决策申请者对它均不可接受,该空位永久存在,
//     由它构成的阻挡对同样永久,可安全剪枝;
//   - 已匹配数 + 剩余未决策申请者数 <= 当前最优值时,分支不可能更优。
type Solver struct {
	p     *Problem
	order []int // 申请者决策顺序
	pos   []int // pos[a] = a 在决策顺序中的位置

	cur     []int // 当前部分匹配
	occ     [][]int
	matched int // 当前已匹配人数

	Best      *Matching
	BestCount int // 达到最优规模的弱稳定匹配个数

	Nodes  int // 搜索节点数
	Leaves int // 完整匹配数(叶子)
	Pruned int // 被剪枝的节点数
}

// NewSolver 构造求解器。
func NewSolver(p *Problem) *Solver {
	s := &Solver{p: p, cur: make([]int, p.NumA), occ: make([][]int, p.NumH)}
	s.order = p.orderApplicants()
	s.pos = make([]int, p.NumA)
	for i, a := range s.order {
		s.pos[a] = i
		s.cur[a] = -1
	}
	return s
}

// Solve 运行搜索并返回一个最大规模弱稳定匹配。
func (s *Solver) Solve() *Matching {
	s.search(0)
	return s.Best
}

func (s *Solver) search(depth int) {
	s.Nodes++
	p := s.p
	if depth == p.NumA {
		s.Leaves++
		m := &Matching{Cur: append([]int(nil), s.cur...)}
		// 安全网:叶子处做完整弱稳定验证(剪枝不变式应已保证通过)。
		if len(BlockingPairs(VerifyWeakStability(p, m))) > 0 {
			return
		}
		if s.Best == nil || m.Size() > s.Best.Size() {
			s.Best = m
			s.BestCount = 1
		} else if m.Size() == s.Best.Size() {
			s.BestCount++
		}
		return
	}
	// 上界剪枝:最大可达规模严格小于当前最优时才剪;
	// 持平最优的分支仍需展开,以保证最优匹配计数精确。
	if s.Best != nil && s.matched+(p.NumA-depth) < s.Best.Size() {
		s.Pruned++
		return
	}
	a := s.order[depth]
	for _, h := range p.AHosps[a] {
		if !p.Acceptable(a, h) || len(s.occ[h]) >= p.Cap[h] {
			continue
		}
		s.cur[a] = h
		s.occ[h] = append(s.occ[h], a)
		s.matched++
		if !s.hasPermanentBlock(depth) {
			s.search(depth + 1)
		} else {
			s.Pruned++
		}
		s.matched--
		s.occ[h] = s.occ[h][:len(s.occ[h])-1]
		s.cur[a] = -1
	}
	// 选择不匹配
	s.cur[a] = -1
	if !s.hasPermanentBlock(depth) {
		s.search(depth + 1)
	} else {
		s.Pruned++
	}
}

// hasPermanentBlock 检查已决策申请者中是否存在沿本分支必然保留的阻挡对。
func (s *Solver) hasPermanentBlock(depth int) bool {
	p := s.p
	for d := 0; d <= depth; d++ {
		a := s.order[d]
		cur := s.cur[a]
		for _, h := range p.AHosps[a] {
			if h == cur {
				continue
			}
			ha, ok := p.HRank[h][a]
			if !ok {
				continue // 非互可接受
			}
			if !p.APrefersStrict(a, h, cur) {
				continue // 申请者一侧不严格偏好
			}
			if len(s.occ[h]) >= p.Cap[h] {
				// 医院已满:在院者沿分支不再变化,若 h 严格偏好 a 则永久阻挡。
				worstRank := -1
				for _, b := range s.occ[h] {
					if r := p.HRank[h][b]; r > worstRank {
						worstRank = r
					}
				}
				if ha < worstRank {
					return true
				}
			} else if s.vacancyPermanent(h, depth) {
				// 空位永久存在且 h 愿意接收 a:永久阻挡。
				return true
			}
		}
	}
	return false
}

// vacancyPermanent 报告 h 的空位是否必然保留到叶子:
// 即剩余未决策申请者中没有任何 h 可接受的人。
func (s *Solver) vacancyPermanent(h, depth int) bool {
	for _, a := range s.p.HApps[h] {
		if s.pos[a] > depth {
			return false
		}
	}
	return true
}
