package main

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// MaxProjects 为求解器支持的最大项目数。
const MaxProjects = 26

// 输入数值范围限制，保证上界计算中的整数运算不溢出。
const (
	maxAbsMoney = int64(1) << 40 // 约 1.1e12
)

// PairContribution 为最终方案中实际实现的一条协同收益。
type PairContribution struct {
	A     string
	B     string
	Value int64
}

// ProjectLine 为最终方案中一个选中项目的分解行。
type ProjectLine struct {
	ID    string
	Name  string
	Cost  int64
	Value int64
}

// Result 为求解结果：成本与收益分解 + 最优性上界证据。
type Result struct {
	Selected     []ProjectLine
	TotalCost    int64
	BaseValue    int64 // 项目固定收益合计
	Synergies    []PairContribution
	SynergyValue int64 // 成对协同收益合计（含负协同）
	TotalValue   int64 // BaseValue + SynergyValue
	Budget       int64

	// 上界证据
	RootUpperBound int64 // 根节点上界：任何可行解的收益都不超过它
	NodesExplored  int64
	NodesPruned    int64
}

// solver 为分支定界求解器。协同收益始终作为成对项保存在 syn 矩阵中，
// 既不在建模时、也不在搜索过程中折算进项目固定收益。
type solver struct {
	n        int
	ids      []string
	names    []string
	cost     []int64
	value    []int64
	syn      [][]int64 // 对称矩阵，syn[i][j] 为 i、j 同时选中的协同收益（可为负）
	reqMask  []uint32  // reqMask[i]：选中 i 时必须同时选中的项目闭包（含 i 自身）
	exclMask []uint32  // exclMask[i]：与 i 互斥的项目集合（对称化后）
	banned   []bool    // 因前置闭包内部互斥或闭包成本超预算而永远不可选的项目
	budget   int64
	allMask  uint32
	order    []int // 分支顺序（按乐观密度降序，不可选项排最后）

	bestVal  int64
	bestMask uint32
	nodes    int64
	pruned   int64
	rootUB   int64
}

// Solve 求解实例，返回收益最高的可行组合及最优性证据。
func Solve(inst Instance) (Result, error) {
	s, err := buildSolver(inst)
	if err != nil {
		return Result{}, err
	}
	s.search(0, 0, 0, 0)
	return s.result(), nil
}

func buildSolver(inst Instance) (*solver, error) {
	n := len(inst.Projects)
	if n == 0 {
		return nil, errors.New("instance has no projects")
	}
	if n > MaxProjects {
		return nil, fmt.Errorf("too many projects: %d (max %d)", n, MaxProjects)
	}
	if inst.Budget < 0 || inst.Budget > maxAbsMoney {
		return nil, fmt.Errorf("budget %d out of supported range [0, %d]", inst.Budget, maxAbsMoney)
	}

	s := &solver{
		n:        n,
		budget:   inst.Budget,
		syn:      make([][]int64, n),
		reqMask:  make([]uint32, n),
		exclMask: make([]uint32, n),
		banned:   make([]bool, n),
		allMask:  uint32(1)<<uint(n) - 1,
	}
	for i := range s.syn {
		s.syn[i] = make([]int64, n)
	}

	index := make(map[string]int, n)
	for i, p := range inst.Projects {
		if p.ID == "" {
			return nil, fmt.Errorf("project #%d has empty id", i)
		}
		if _, dup := index[p.ID]; dup {
			return nil, fmt.Errorf("duplicate project id %q", p.ID)
		}
		index[p.ID] = i
		if p.Cost < 0 || p.Cost > maxAbsMoney {
			return nil, fmt.Errorf("project %q: cost %d out of supported range", p.ID, p.Cost)
		}
		if abs64(p.Value) > maxAbsMoney {
			return nil, fmt.Errorf("project %q: value %d out of supported range", p.ID, p.Value)
		}
		s.ids = append(s.ids, p.ID)
		name := p.Name
		if name == "" {
			name = p.ID
		}
		s.names = append(s.names, name)
		s.cost = append(s.cost, p.Cost)
		s.value = append(s.value, p.Value)
	}

	lookup := func(id string) (int, error) {
		j, ok := index[id]
		if !ok {
			return 0, fmt.Errorf("unknown project id %q", id)
		}
		return j, nil
	}

	// 前置关系（先记直接边，随后求传递闭包）
	for i, p := range inst.Projects {
		for _, r := range p.Requires {
			j, err := lookup(r)
			if err != nil {
				return nil, fmt.Errorf("project %q requires %v", p.ID, err)
			}
			s.reqMask[i] |= uint32(1) << uint(j)
		}
	}
	for i := 0; i < n; i++ {
		mask := s.reqMask[i] | uint32(1)<<uint(i)
		for {
			expanded := mask
			for m := mask; m != 0; m &= m - 1 {
				j := bitIndex(m)
				expanded |= s.reqMask[j]
			}
			if expanded == mask {
				break
			}
			mask = expanded
		}
		s.reqMask[i] = mask
	}

	// 互斥关系（对称化）
	for i, p := range inst.Projects {
		for _, e := range p.Excludes {
			j, err := lookup(e)
			if err != nil {
				return nil, fmt.Errorf("project %q excludes %v", p.ID, err)
			}
			if j == i {
				return nil, fmt.Errorf("project %q excludes itself", p.ID)
			}
			s.exclMask[i] |= uint32(1) << uint(j)
			s.exclMask[j] |= uint32(1) << uint(i)
		}
	}

	// 成对协同收益（可为负；重复出现的同一对累加）
	for _, sy := range inst.Synergies {
		a, err := lookup(sy.A)
		if err != nil {
			return nil, fmt.Errorf("synergy: %v", err)
		}
		b, err := lookup(sy.B)
		if err != nil {
			return nil, fmt.Errorf("synergy: %v", err)
		}
		if a == b {
			return nil, fmt.Errorf("synergy on self-pair %q", sy.A)
		}
		if abs64(sy.Value) > maxAbsMoney {
			return nil, fmt.Errorf("synergy %q-%q: value %d out of supported range", sy.A, sy.B, sy.Value)
		}
		s.syn[a][b] += sy.Value
		s.syn[b][a] += sy.Value
	}

	// 永不可选项目：前置闭包成本超预算，或闭包内部存在互斥。
	for i := 0; i < n; i++ {
		closure := s.reqMask[i]
		var csum int64
		for m := closure; m != 0; m &= m - 1 {
			csum += s.cost[bitIndex(m)]
		}
		if csum > s.budget {
			s.banned[i] = true
			continue
		}
		for m := closure; m != 0; m &= m - 1 {
			if s.exclMask[bitIndex(m)]&closure != 0 {
				s.banned[i] = true
				break
			}
		}
	}

	// 分支顺序：乐观密度 =（固定收益 + 正协同之和）/ 成本，降序；不可选项排最后。
	type cand struct {
		idx      int
		num, den int64 // 密度 = num/den
		score    int64
	}
	cands := make([]cand, 0, n)
	for i := 0; i < n; i++ {
		score := s.value[i]
		for j := 0; j < n; j++ {
			if s.syn[i][j] > 0 {
				score += s.syn[i][j]
			}
		}
		den := s.cost[i]
		if den < 1 {
			den = 1
		}
		cands = append(cands, cand{idx: i, num: score, den: den, score: score})
	}
	sort.SliceStable(cands, func(a, b int) bool {
		ca, cb := cands[a], cands[b]
		if s.banned[ca.idx] != s.banned[cb.idx] {
			return !s.banned[ca.idx]
		}
		if ca.num*cb.den != cb.num*ca.den {
			return ca.num*cb.den > cb.num*ca.den
		}
		return ca.score > cb.score
	})
	for _, c := range cands {
		s.order = append(s.order, c.idx)
	}
	return s, nil
}

// search 为分支定界主递归。
// decided：已决定（选或不选）的项目位掩码；included：已选中的项目位掩码。
// curVal / curCost：已选中集合当前的精确总收益（含协同）与总成本。
func (s *solver) search(decided, included uint32, curVal, curCost int64) {
	s.nodes++
	// 当前 included 集合本身即一个可行解，随时可更新最优。
	if curVal > s.bestVal {
		s.bestVal = curVal
		s.bestMask = included
	}
	ub := s.upperBound(decided, included, curVal, curCost)
	if s.nodes == 1 {
		s.rootUB = ub
	}
	if ub <= s.bestVal {
		s.pruned++
		return
	}
	i := s.nextUndecided(decided)
	if i < 0 {
		return
	}
	bit := uint32(1) << uint(i)

	// 分支一：选中 i（连带其前置闭包）。
	if !s.banned[i] && s.exclMask[i]&included == 0 {
		out := decided &^ included
		add := s.reqMask[i] &^ included
		if add&out == 0 { // 前置闭包中没有已被排除的项目
			feasible := true
			newCost := curCost
			for m := add; m != 0; m &= m - 1 {
				j := bitIndex(m)
				if s.exclMask[j]&(included|add) != 0 {
					feasible = false
					break
				}
				newCost += s.cost[j]
			}
			if feasible && newCost <= s.budget {
				delta := int64(0)
				for m := add; m != 0; m &= m - 1 {
					j := bitIndex(m)
					delta += s.value[j]
					for k := included; k != 0; k &= k - 1 {
						delta += s.syn[j][bitIndex(k)]
					}
					// add 集合内部的成对协同（每对计一次）
					for k := m & (m - 1); k != 0; k &= k - 1 {
						delta += s.syn[j][bitIndex(k)]
					}
				}
				s.search(decided|add, included|add, curVal+delta, newCost)
			}
		}
	}

	// 分支二：不选 i。
	s.search(decided|bit, included, curVal, curCost)
}

// upperBound 计算当前节点子树中任意可行解的收益上界。
// 这是对原问题的松弛：忽略剩余预算之外的所有耦合约束，
// 项目边际收益取正部后做背包 LP 松弛（受剩余预算约束），
// 未决定项目之间的协同只累加正部——因此负协同不会使上界失效。
// 协同始终以成对形式计入，绝不折算为项目固定价值。
func (s *solver) upperBound(decided, included uint32, curVal, curCost int64) int64 {
	out := decided &^ included
	undecided := s.allMask &^ decided

	// avail：仍可被追加选中的未决定项目。
	var avail []int
	for m := undecided; m != 0; m &= m - 1 {
		i := bitIndex(m)
		if s.banned[i] {
			continue
		}
		if s.exclMask[i]&included != 0 {
			continue
		}
		if s.reqMask[i]&out != 0 {
			continue
		}
		avail = append(avail, i)
	}

	bound := curVal

	// 项目部分：边际收益正部在剩余预算下的背包 LP 松弛上界。
	rem := s.budget - curCost
	if len(avail) > 0 {
		type frac struct {
			w, c int64
		}
		items := make([]frac, 0, len(avail))
		for _, i := range avail {
			marg := s.value[i]
			for k := included; k != 0; k &= k - 1 {
				marg += s.syn[i][bitIndex(k)]
			}
			if marg > 0 {
				items = append(items, frac{w: marg, c: s.cost[i]})
			}
		}
		sort.Slice(items, func(a, b int) bool {
			// 128 位交叉相乘比较 w1/c1 > w2/c2，避免溢出。
			hi1, lo1 := bits.Mul64(uint64(items[a].w), uint64(items[b].c))
			hi2, lo2 := bits.Mul64(uint64(items[b].w), uint64(items[a].c))
			if hi1 != hi2 {
				return hi1 > hi2
			}
			return lo1 > lo2
		})
		capLeft := rem
		for _, it := range items {
			if it.c == 0 {
				// 零成本项目不占用预算，即使剩余预算为零也可计入。
				bound += it.w
				continue
			}
			if capLeft == 0 {
				break
			}
			if it.c <= capLeft {
				bound += it.w
				capLeft -= it.c
			} else {
				// 分数件：向上取整保持上界有效。
				bound += ceilDivMul(it.w, capLeft, it.c)
				capLeft = 0
			}
		}
	}

	// 成对协同部分：未决定且仍可用项目之间的正协同全部累加（松弛）。
	for x := 0; x < len(avail); x++ {
		for y := x + 1; y < len(avail); y++ {
			if v := s.syn[avail[x]][avail[y]]; v > 0 {
				bound += v
			}
		}
	}
	return bound
}

func (s *solver) nextUndecided(decided uint32) int {
	for _, i := range s.order {
		if decided&(uint32(1)<<uint(i)) == 0 {
			return i
		}
	}
	return -1
}

// result 汇总最优解的成本与收益分解，并附上界证据。
func (s *solver) result() Result {
	r := Result{
		Budget:         s.budget,
		RootUpperBound: s.rootUB,
		NodesExplored:  s.nodes,
		NodesPruned:    s.pruned,
	}
	for m := s.bestMask; m != 0; m &= m - 1 {
		i := bitIndex(m)
		r.Selected = append(r.Selected, ProjectLine{
			ID:    s.ids[i],
			Name:  s.names[i],
			Cost:  s.cost[i],
			Value: s.value[i],
		})
		r.TotalCost += s.cost[i]
		r.BaseValue += s.value[i]
	}
	for x := 0; x < s.n; x++ {
		if s.bestMask&(uint32(1)<<uint(x)) == 0 {
			continue
		}
		for y := x + 1; y < s.n; y++ {
			if s.bestMask&(uint32(1)<<uint(y)) == 0 {
				continue
			}
			if v := s.syn[x][y]; v != 0 {
				r.Synergies = append(r.Synergies, PairContribution{
					A:     s.ids[x],
					B:     s.ids[y],
					Value: v,
				})
				r.SynergyValue += v
			}
		}
	}
	r.TotalValue = r.BaseValue + r.SynergyValue
	return r
}

func bitIndex(m uint32) int {
	// m 非零；返回其最低有效位的下标。
	idx := 0
	for m&1 == 0 {
		m >>= 1
		idx++
	}
	return idx
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ceilDivMul 返回 ceil(a*b/c)，其中 a >= 0, b >= 0, c > b（分数件分支保证），
// 用 128 位中间结果避免 a*b 溢出；此时商必小于 a，可用 int64 表示。
func ceilDivMul(a, b, c int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	q, r := bits.Div64(hi, lo, uint64(c))
	if r != 0 {
		q++
	}
	return int64(q)
}
