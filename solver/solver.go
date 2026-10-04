package solver

import (
	"fmt"
	"math"
	"sort"
)

// Flow 一笔资金流向。
type Flow struct {
	Kind   string `json:"kind"` // direct=直接拨款, match=配捐
	From   string `json:"from"`
	To     string `json:"to"`
	Amount Cents  `json:"amount"`
	Reason string `json:"reason"` // 触发依据
}

// matchPart 一次来源拨款中由某规则配捐的部分。
type matchPart struct {
	rule   int
	amount Cents
}

// allocPlan 单个项目的一笔来源出资计划（本金 + 触发的配捐）。
type allocPlan struct {
	src    int
	amount Cents
	parts  []matchPart
}

// Solver 分支定界求解器。
//
// 主目标：已整笔达标项目的收益之和最大；
// 次目标：过期闲置金额最小（等价于“有有效期的来源+配捐额度”被动用最多）。
// 配捐只由来源拨款触发，配捐资金本身不会再触发配捐（结构上保证）。
type Solver struct {
	in          *Input
	order       []int // 项目搜索顺序
	suffix      []Cents
	maxProjects int

	srcBal     []Cents
	srcUsable  []bool
	ruleCap    []Cents
	ruleActive []bool
	rulesFor   [][]int // 每个来源可触发的规则（按 ratio 降序）

	baseExpiring Cents // 有有效期的来源与规则额度之和（常量）

	// 当前搜索状态
	flows   []Flow
	benefit Cents
	funded  []bool

	// 最优解快照
	bestBenefit Cents
	bestUsed    Cents
	bestFlows   []Flow
	bestFunded  []bool
	bestSrcBal  []Cents
	bestRuleCap []Cents
}

// Solve 求解并生成报告。
func Solve(in *Input) *Report {
	s := newSolver(in)
	s.greedyIncumbent()
	s.search(0, 0)
	return s.buildReport()
}

func newSolver(in *Input) *Solver {
	s := &Solver{
		in:          in,
		maxProjects: in.MaxProjects,
		srcBal:      make([]Cents, len(in.Sources)),
		srcUsable:   make([]bool, len(in.Sources)),
		ruleCap:     make([]Cents, len(in.MatchRules)),
		ruleActive:  make([]bool, len(in.MatchRules)),
		rulesFor:    make([][]int, len(in.Sources)),
		funded:      make([]bool, len(in.Projects)),
		bestUsed:    -1,
	}
	for i, src := range in.Sources {
		s.srcBal[i] = src.Amount
		s.srcUsable[i] = src.Expires == "" || src.Expires >= in.AsOf
		if src.Expires != "" {
			s.baseExpiring += src.Amount
		}
	}
	for i, r := range in.MatchRules {
		s.ruleCap[i] = r.Cap
		s.ruleActive[i] = r.Expires == "" || r.Expires >= in.AsOf
		if r.Expires != "" {
			s.baseExpiring += r.Cap
		}
	}
	for si, src := range in.Sources {
		for ri, r := range in.MatchRules {
			if !s.ruleActive[ri] || r.Ratio <= 0 {
				continue
			}
			if r.SourceID != "" && r.SourceID != src.ID {
				continue
			}
			s.rulesFor[si] = append(s.rulesFor[si], ri)
		}
		sort.Slice(s.rulesFor[si], func(a, b int) bool {
			return in.MatchRules[s.rulesFor[si][a]].Ratio > in.MatchRules[s.rulesFor[si][b]].Ratio
		})
	}
	// 搜索顺序：收益密度（收益/目标额）降序，其次收益降序。
	s.order = make([]int, len(in.Projects))
	for i := range s.order {
		s.order[i] = i
	}
	sort.Slice(s.order, func(a, b int) bool {
		pa, pb := in.Projects[s.order[a]], in.Projects[s.order[b]]
		da := float64(pa.Benefit) / float64(pa.Target)
		db := float64(pb.Benefit) / float64(pb.Target)
		if da != db {
			return da > db
		}
		if pa.Benefit != pb.Benefit {
			return pa.Benefit > pb.Benefit
		}
		return pa.ID < pb.ID
	})
	s.suffix = make([]Cents, len(s.order)+1)
	for i := len(s.order) - 1; i >= 0; i-- {
		s.suffix[i] = s.suffix[i+1] + in.Projects[s.order[i]].Benefit
	}
	return s
}

// greedyIncumbent 先求一个贪心可行解作为初始 incumbent。
func (s *Solver) greedyIncumbent() {
	type rec struct {
		pIdx  int
		plans []allocPlan
		mark  int
	}
	var recs []rec
	sel := 0
	for _, pIdx := range s.order {
		if sel >= s.maxProjects {
			break
		}
		if plans, ok := s.planProject(pIdx); ok {
			mark := len(s.flows)
			s.apply(pIdx, plans)
			recs = append(recs, rec{pIdx, plans, mark})
			sel++
		}
	}
	s.evaluate()
	for i := len(recs) - 1; i >= 0; i-- {
		s.undo(recs[i].pIdx, recs[i].plans, recs[i].mark)
	}
}

func (s *Solver) search(idx, selected int) {
	if idx == len(s.order) {
		s.evaluate()
		return
	}
	// 收益上界剪枝（严格小于才剪，保留同收益但更优次目标的叶子）。
	if s.benefit+s.suffix[idx] < s.bestBenefit {
		return
	}
	pIdx := s.order[idx]
	if selected < s.maxProjects {
		if plans, ok := s.planProject(pIdx); ok {
			mark := len(s.flows)
			s.apply(pIdx, plans)
			s.search(idx+1, selected+1)
			s.undo(pIdx, plans, mark)
		}
	}
	s.search(idx+1, selected)
}

// evaluate 以（收益, 动用有有效期资金）字典序比较并更新最优解。
func (s *Solver) evaluate() {
	var remainingExpiring Cents
	for i, src := range s.in.Sources {
		if src.Expires != "" {
			remainingExpiring += s.srcBal[i]
		}
	}
	for i, r := range s.in.MatchRules {
		if r.Expires != "" {
			remainingExpiring += s.ruleCap[i]
		}
	}
	used := s.baseExpiring - remainingExpiring
	if s.benefit > s.bestBenefit || (s.benefit == s.bestBenefit && used > s.bestUsed) {
		s.bestBenefit = s.benefit
		s.bestUsed = used
		s.bestFlows = append([]Flow(nil), s.flows...)
		s.bestFunded = append([]bool(nil), s.funded...)
		s.bestSrcBal = append([]Cents(nil), s.srcBal...)
		s.bestRuleCap = append([]Cents(nil), s.ruleCap...)
	}
}

// planProject 为项目制定整笔达标计划：按优先级依次从各来源出资，
// 能凑满 Target（直接+配捐）才返回计划，否则返回 false。
func (s *Solver) planProject(pIdx int) ([]allocPlan, bool) {
	proj := &s.in.Projects[pIdx]
	need := proj.Target
	var plans []allocPlan
	for _, si := range s.srcPriority(proj) {
		if need <= 0 {
			break
		}
		x, parts := s.allocateFromSource(si, proj, need)
		if x <= 0 {
			continue
		}
		got := x
		for _, mp := range parts {
			got += mp.amount
		}
		need -= got
		plans = append(plans, allocPlan{src: si, amount: x, parts: parts})
	}
	if need > 0 {
		return nil, false
	}
	return plans, true
}

// srcPriority 来源出资优先级：配捐杠杆高者优先（省本金，服务主目标）；
// 其次有有效期且到期更早者优先（服务次目标）；再按余额、ID。
func (s *Solver) srcPriority(proj *Project) []int {
	var idxs []int
	leverage := map[int]float64{}
	for si, src := range s.in.Sources {
		if !s.srcUsable[si] || s.srcBal[si] <= 0 {
			continue
		}
		if !purposeMatch(src.Purpose, proj.Purpose) {
			continue
		}
		lev := 1.0
		for _, ri := range s.rulesFor[si] {
			r := s.in.MatchRules[ri]
			if s.ruleCap[ri] > 0 && purposeMatch(r.Purpose, proj.Purpose) && r.Ratio+1 > lev {
				lev = r.Ratio + 1
			}
		}
		leverage[si] = lev
		idxs = append(idxs, si)
	}
	sort.Slice(idxs, func(a, b int) bool {
		sa, sb := s.in.Sources[idxs[a]], s.in.Sources[idxs[b]]
		la, lb := leverage[idxs[a]], leverage[idxs[b]]
		if la != lb {
			return la > lb
		}
		ea, eb := sa.Expires != "", sb.Expires != ""
		if ea != eb {
			return ea
		}
		if sa.Expires != sb.Expires {
			return sa.Expires < sb.Expires
		}
		if s.srcBal[idxs[a]] != s.srcBal[idxs[b]] {
			return s.srcBal[idxs[a]] > s.srcBal[idxs[b]]
		}
		return sa.ID < sb.ID
	})
	return idxs
}

// allocateFromSource 从来源 srcIdx 出资，使“本金+配捐”不超过 need。
// 返回出资本金与触发的各规则配捐明细。
func (s *Solver) allocateFromSource(srcIdx int, proj *Project, need Cents) (Cents, []matchPart) {
	bal := s.srcBal[srcIdx]
	if bal <= 0 || need <= 0 {
		return 0, nil
	}
	var x, progress Cents
	var parts []matchPart
	for _, ri := range s.rulesFor[srcIdx] {
		r := s.in.MatchRules[ri]
		if !purposeMatch(r.Purpose, proj.Purpose) {
			continue
		}
		capRem := s.ruleCap[ri]
		if capRem <= 0 {
			continue
		}
		maxPortion := Cents(math.Floor(float64(capRem)/r.Ratio + 1e-9))
		portion := minC(maxPortion, bal-x)
		if portion <= 0 {
			continue
		}
		gap := need - progress
		if portion+matchOf(portion, r.Ratio) > gap {
			// 二分求最大 p，使 p + floor(p*ratio) <= gap，避免超额拨付。
			lo, hi := Cents(0), portion
			for lo < hi {
				mid := (lo + hi + 1) / 2
				if mid+matchOf(mid, r.Ratio) <= gap {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			portion = lo
		}
		if portion <= 0 {
			continue
		}
		m := matchOf(portion, r.Ratio)
		if m > capRem {
			m = capRem
		}
		x += portion
		progress += portion + m
		if m > 0 {
			parts = append(parts, matchPart{rule: ri, amount: m})
		}
		if progress >= need {
			return x, parts
		}
	}
	// 规则额度用尽或取整零头：用无配捐本金补足。
	if progress < need && x < bal {
		top := minC(bal-x, need-progress)
		x += top
	}
	return x, parts
}

func matchOf(portion Cents, ratio float64) Cents {
	if ratio <= 0 {
		return 0
	}
	return Cents(math.Floor(float64(portion)*ratio + 1e-9))
}

func minC(a, b Cents) Cents {
	if a < b {
		return a
	}
	return b
}

func (s *Solver) apply(pIdx int, plans []allocPlan) {
	proj := &s.in.Projects[pIdx]
	for _, pl := range plans {
		src := &s.in.Sources[pl.src]
		s.srcBal[pl.src] -= pl.amount
		s.flows = append(s.flows, Flow{
			Kind: "direct", From: src.ID, To: proj.ID, Amount: pl.amount,
			Reason: directReason(src, proj),
		})
		for _, mp := range pl.parts {
			r := &s.in.MatchRules[mp.rule]
			s.ruleCap[mp.rule] -= mp.amount
			s.flows = append(s.flows, Flow{
				Kind: "match", From: r.Funder, To: proj.ID, Amount: mp.amount,
				Reason: matchReason(r, src, proj),
			})
		}
	}
	s.funded[pIdx] = true
	s.benefit += proj.Benefit
}

func (s *Solver) undo(pIdx int, plans []allocPlan, mark int) {
	proj := &s.in.Projects[pIdx]
	for _, pl := range plans {
		s.srcBal[pl.src] += pl.amount
		for _, mp := range pl.parts {
			s.ruleCap[mp.rule] += mp.amount
		}
	}
	s.flows = s.flows[:mark]
	s.funded[pIdx] = false
	s.benefit -= proj.Benefit
}

func directReason(src *Source, proj *Project) string {
	purpose := "来源不限用途"
	if src.Purpose != "" && src.Purpose != "*" {
		purpose = fmt.Sprintf("来源限定用途「%s」与项目用途一致", src.Purpose)
	}
	expiry := "无有效期限制"
	if src.Expires != "" {
		expiry = fmt.Sprintf("在有效期 %s 内拨付", src.Expires)
	}
	return fmt.Sprintf("%s；%s", purpose, expiry)
}

func matchReason(r *MatchRule, src *Source, proj *Project) string {
	return fmt.Sprintf("来源 %s 向项目 %s 拨款，触发配捐规则 %s（出资方 %s，比例 %.2f:1）；配捐资金本身不再触发新配捐",
		src.ID, proj.ID, r.ID, r.Funder, r.Ratio)
}
