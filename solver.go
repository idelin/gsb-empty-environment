package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	feasTol = 1e-4 // 可行性金额容差
	flowEps = 1e-4 // 输出资金流的最小阈值
	optTol  = 1e-6 // 目标比较容差
)

// Flow 一笔资金流向。
type Flow struct {
	From     string  `json:"from"`
	FromName string  `json:"from_name"`
	To       string  `json:"to"`
	ToName   string  `json:"to_name"`
	Amount   float64 `json:"amount"`
	Type     string  `json:"type"` // "direct" 或 "match"
	Basis    string  `json:"basis"`
}

// IdleFund 一笔未投出的闲置资金。
type IdleFund struct {
	Source     string  `json:"source"`
	SourceName string  `json:"source_name"`
	Amount     float64 `json:"amount"`
	Expired    bool    `json:"expired"` // true 表示计入过期闲置
}

// FundedProject 一个达标项目的资助结果。
type FundedProject struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Goal     float64 `json:"goal"`
	Benefit  float64 `json:"benefit"`
	Funded   float64 `json:"funded"`
	BaseFund float64 `json:"base_fund"` // 其中非配捐基础资金
}

// Result 求解结果。
type Result struct {
	Benefit     float64         `json:"total_benefit"`
	ExpiredIdle float64         `json:"expired_idle"`
	MaxProjects int             `json:"max_projects"`
	Funded      []FundedProject `json:"funded_projects"`
	Unfunded    []Project       `json:"unfunded_projects"`
	Flows       []Flow          `json:"flows"`
	Idle        []IdleFund      `json:"idle_funds"`

	set []int // 入选项目在 in.Projects 中的下标
}

// setLP 针对固定项目集合构造的 LP 结果。
type setLP struct {
	value float64
	x     []float64
	xVar  map[[2]int]int // (来源下标, 项目下标) -> 变量下标，非配捐来源
	yVar  map[[2]int]int // (配捐池下标, 项目下标) -> 变量下标
	err   error
}

// buildSetLP 为项目集合 set 构造线性规划。
// secondary=false：最大化总拨付额（用于可行性判定，最优值 >= 目标总额即可行）。
// secondary=true ：在全部项目整笔达标的前提下，最大化临期（有有效期）资金的使用量，
// 等价于最小化过期闲置金额。
func buildSetLP(in *Input, set []int, secondary bool) *setLP {
	var nonMatch, pools []int
	for i := range in.Sources {
		if in.Sources[i].Match == nil {
			nonMatch = append(nonMatch, i)
		} else {
			pools = append(pools, i)
		}
	}
	res := &setLP{xVar: map[[2]int]int{}, yVar: map[[2]int]int{}}
	vars := 0
	for _, s := range nonMatch {
		for _, p := range set {
			if eligible(&in.Sources[s], &in.Projects[p]) {
				res.xVar[[2]int{s, p}] = vars
				vars++
			}
		}
	}
	for _, m := range pools {
		for _, p := range set {
			if eligible(&in.Sources[m], &in.Projects[p]) {
				res.yVar[[2]int{m, p}] = vars
				vars++
			}
		}
	}

	var coefs []map[int]float64
	var rhs []float64
	addRow := func(coef map[int]float64, b float64) {
		coefs = append(coefs, coef)
		rhs = append(rhs, b)
	}

	// 来源额度约束
	for _, s := range nonMatch {
		coef := map[int]float64{}
		for _, p := range set {
			if v, ok := res.xVar[[2]int{s, p}]; ok {
				coef[v] = 1
			}
		}
		if len(coef) > 0 {
			addRow(coef, in.Sources[s].Amount)
		}
	}
	// 配捐池额度约束
	for _, m := range pools {
		coef := map[int]float64{}
		for _, p := range set {
			if v, ok := res.yVar[[2]int{m, p}]; ok {
				coef[v] = 1
			}
		}
		if len(coef) > 0 {
			addRow(coef, in.Sources[m].Amount)
		}
	}
	// 配捐触发约束：y_{m,p} <= ratio * (项目 p 的非配捐基础资金)，以及单项目上限。
	// 基础资金只统计非配捐来源，因此配捐不会再次触发配捐。
	for _, m := range pools {
		rule := in.Sources[m].Match
		for _, p := range set {
			yv, ok := res.yVar[[2]int{m, p}]
			if !ok {
				continue
			}
			coef := map[int]float64{yv: 1}
			for _, s := range nonMatch {
				if xv, ok := res.xVar[[2]int{s, p}]; ok {
					coef[xv] = -rule.Ratio
				}
			}
			addRow(coef, 0)
			if rule.PerProjectCap > 0 {
				addRow(map[int]float64{yv: 1}, rule.PerProjectCap)
			}
		}
	}
	// 项目整笔目标约束
	for _, p := range set {
		coef := map[int]float64{}
		for _, s := range nonMatch {
			if v, ok := res.xVar[[2]int{s, p}]; ok {
				coef[v] = 1
			}
		}
		for _, m := range pools {
			if v, ok := res.yVar[[2]int{m, p}]; ok {
				coef[v] = 1
			}
		}
		goal := in.Projects[p].Goal
		addRow(coef, goal)
		if secondary {
			neg := map[int]float64{}
			for k, v := range coef {
				neg[k] = -v
			}
			addRow(neg, -goal)
		}
	}

	c := make([]float64, vars)
	if !secondary {
		for j := range c {
			c[j] = 1
		}
	} else {
		for key, v := range res.xVar {
			if in.Sources[key[0]].Expires != "" {
				c[v] = 1
			}
		}
		for key, v := range res.yVar {
			if in.Sources[key[0]].Expires != "" {
				c[v] = 1
			}
		}
	}

	a := make([][]float64, len(coefs))
	b := make([]float64, len(coefs))
	for i, coef := range coefs {
		a[i] = make([]float64, vars)
		for k, v := range coef {
			a[i][k] = v
		}
		b[i] = rhs[i]
	}
	sv := newLPSolver(a, b, c)
	res.value, res.x, res.err = sv.solve()
	return res
}

func setGoal(in *Input, set []int) float64 {
	total := 0.0
	for _, p := range set {
		total += in.Projects[p].Goal
	}
	return total
}

// feasibleSet 判定项目集合是否可全部整笔达标。
func feasibleSet(in *Input, set []int) bool {
	if len(set) == 0 {
		return true
	}
	lp := buildSetLP(in, set, false)
	if lp.err != nil {
		return false
	}
	return lp.value >= setGoal(in, set)-feasTol
}

// expiredIdle 计算可行集合在次目标下的最小过期闲置金额。
func expiredIdle(in *Input, set []int) float64 {
	total := in.totalExpiring()
	if total <= 0 {
		return 0
	}
	lp := buildSetLP(in, set, true)
	if lp.err != nil {
		return total
	}
	idle := total - lp.value
	if idle < 0 {
		idle = 0
	}
	return idle
}

// search 分支定界搜索状态。
type search struct {
	in      *Input
	order   []int     // 项目下标，按收益降序
	suffix  []float64 // suffix[i] = order[i:] 的收益和
	goalTop []float64 // goalTop[k] = 全部项目中 k 个最大目标的和（需求上界）
	expTot  float64   // 临期资金总额
	cache   map[string]bool
	best    *Result
	bestSet []int // order 空间中的位置
}

func (s *search) key(chosen []int) string {
	b := make([]byte, len(s.order))
	for _, p := range chosen {
		b[p] = 1
	}
	return string(b)
}

func (s *search) toProjects(chosen []int) []int {
	set := make([]int, len(chosen))
	for i, p := range chosen {
		set[i] = s.order[p]
	}
	return set
}

func (s *search) feasible(chosen []int) bool {
	k := s.key(chosen)
	if v, ok := s.cache[k]; ok {
		return v
	}
	v := feasibleSet(s.in, s.toProjects(chosen))
	s.cache[k] = v
	return v
}

func (s *search) consider(chosen []int, benefit float64) {
	if s.best != nil && benefit < s.best.Benefit-optTol {
		return
	}
	idle := expiredIdle(s.in, s.toProjects(chosen))
	if s.best == nil ||
		benefit > s.best.Benefit+optTol ||
		(math.Abs(benefit-s.best.Benefit) <= optTol && idle < s.best.ExpiredIdle-optTol) {
		s.best = &Result{Benefit: benefit, ExpiredIdle: idle}
		s.bestSet = append([]int(nil), chosen...)
	}
}

// dfs 搜索。demand 为已选项目的目标总额。
func (s *search) dfs(pos int, chosen []int, benefit, demand float64) {
	s.consider(chosen, benefit)
	maxP := s.in.MaxProjects
	if len(chosen) >= maxP {
		return
	}
	n := len(s.order)
	for i := pos; i < n; i++ {
		rem := maxP - len(chosen)
		end := i + rem
		if end > n {
			end = n
		}
		// 收益上界剪枝：剩余名额全取收益最高的项目也无法超越当前最优
		bound := benefit + s.suffix[i] - s.suffix[end]
		if bound < s.best.Benefit-optTol {
			break
		}
		// 平局剪枝：主目标无法更优时，若过期闲置也不可能更低则剪枝。
		// 闲置下界 = 临期总额 - 尚可使用的临期资金上界（受剩余需求约束）。
		if bound < s.best.Benefit+optTol {
			k := rem
			if k > n {
				k = n
			}
			usable := demand + s.goalTop[k]
			if usable > s.expTot {
				usable = s.expTot
			}
			if s.expTot-usable >= s.best.ExpiredIdle-optTol {
				break
			}
		}
		next := append(append([]int(nil), chosen...), i)
		if s.feasible(next) {
			p := s.order[i]
			s.dfs(i+1, next, benefit+s.in.Projects[p].Benefit, demand+s.in.Projects[p].Goal)
		}
	}
}

// Solve 求解：主目标总收益最大，次目标过期闲置最小，入选项目数不超过上限。
func Solve(in *Input) (*Result, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	n := len(in.Projects)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return in.Projects[order[a]].Benefit > in.Projects[order[b]].Benefit
	})
	suffix := make([]float64, n+1)
	for i := n - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + in.Projects[order[i]].Benefit
	}
	goals := make([]float64, n)
	for i, p := range in.Projects {
		goals[i] = p.Goal
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(goals)))
	goalTop := make([]float64, n+1)
	for i := 0; i < n; i++ {
		goalTop[i+1] = goalTop[i] + goals[i]
	}
	s := &search{
		in: in, order: order, suffix: suffix, goalTop: goalTop,
		expTot: in.totalExpiring(), cache: map[string]bool{},
	}
	s.dfs(0, nil, 0, 0)

	res := s.best
	res.MaxProjects = in.MaxProjects
	res.set = s.toProjects(s.bestSet)
	if err := res.buildDetails(in); err != nil {
		return nil, err
	}
	return res, nil
}

// buildDetails 对最优集合重解次目标 LP，生成每笔资金流向与触发依据。
func (r *Result) buildDetails(in *Input) error {
	inSet := make([]bool, len(in.Projects))
	for _, p := range r.set {
		inSet[p] = true
	}
	lp := buildSetLP(in, r.set, true)
	if lp.err != nil {
		return fmt.Errorf("内部错误：最优集合次目标 LP 不可行: %v", lp.err)
	}

	// 每个项目的非配捐基础资金与来源
	baseOf := map[int]float64{}
	var baseSrcs map[int][]string
	baseSrcs = map[int][]string{}
	for key, v := range lp.xVar {
		amt := lp.x[v]
		if amt <= flowEps {
			continue
		}
		si, pi := key[0], key[1]
		baseOf[pi] += amt
		src := &in.Sources[si]
		baseSrcs[pi] = append(baseSrcs[pi], fmt.Sprintf("%s(%.2f)", src.ID, amt))
		r.Flows = append(r.Flows, Flow{
			From: src.ID, FromName: src.Name,
			To: in.Projects[pi].ID, ToName: in.Projects[pi].Name,
			Amount: round2(amt),
			Type:   "direct",
			Basis:  directBasis(src, &in.Projects[pi]),
		})
	}
	for key, v := range lp.yVar {
		amt := lp.x[v]
		if amt <= flowEps {
			continue
		}
		mi, pi := key[0], key[1]
		pool := &in.Sources[mi]
		r.Flows = append(r.Flows, Flow{
			From: pool.ID, FromName: pool.Name,
			To: in.Projects[pi].ID, ToName: in.Projects[pi].Name,
			Amount: round2(amt),
			Type:   "match",
			Basis:  matchBasis(pool, &in.Projects[pi], baseOf[pi], baseSrcs[pi]),
		})
	}

	// 项目汇总
	fundedBy := map[int]float64{}
	for _, f := range r.Flows {
		for _, p := range r.set {
			if in.Projects[p].ID == f.To {
				fundedBy[p] += f.Amount
			}
		}
	}
	for _, p := range r.set {
		proj := in.Projects[p]
		r.Funded = append(r.Funded, FundedProject{
			ID: proj.ID, Name: proj.Name, Goal: proj.Goal,
			Benefit: proj.Benefit, Funded: round2(fundedBy[p]),
			BaseFund: round2(baseOf[p]),
		})
	}
	for i, proj := range in.Projects {
		if !inSet[i] {
			r.Unfunded = append(r.Unfunded, proj)
		}
	}

	// 闲置资金
	used := map[int]float64{}
	for key, v := range lp.xVar {
		used[key[0]] += lp.x[v]
	}
	for key, v := range lp.yVar {
		used[key[0]] += lp.x[v]
	}
	for i := range in.Sources {
		left := in.Sources[i].Amount - used[i]
		if left > flowEps {
			r.Idle = append(r.Idle, IdleFund{
				Source: in.Sources[i].ID, SourceName: in.Sources[i].Name,
				Amount: round2(left), Expired: in.Sources[i].Expires != "",
			})
		}
	}
	return nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func directBasis(s *Source, p *Project) string {
	var parts []string
	if len(s.Categories) > 0 {
		parts = append(parts, fmt.Sprintf("来源限定用途「%s」与项目类别「%s」匹配",
			strings.Join(s.Categories, "/"), p.Category))
	} else {
		parts = append(parts, "来源不限定用途")
	}
	if s.Expires == "" {
		parts = append(parts, "来源无有效期限制")
	} else if p.Deadline == "" {
		parts = append(parts, fmt.Sprintf("来源有效期 %s，项目无截止日，可即时拨付", s.Expires))
	} else {
		parts = append(parts, fmt.Sprintf("来源有效期 %s 不早于项目截止日 %s", s.Expires, p.Deadline))
	}
	return "直接拨付：" + strings.Join(parts, "；")
}

func matchBasis(pool *Source, p *Project, base float64, baseSrcs []string) string {
	rule := pool.Match
	srcs := strings.Join(baseSrcs, "、")
	if srcs == "" {
		srcs = "无"
	}
	capNote := "无单项目上限"
	if rule.PerProjectCap > 0 {
		capNote = fmt.Sprintf("单项目上限 %.2f", rule.PerProjectCap)
	}
	return fmt.Sprintf("配捐触发：项目获得非配捐基础资金 %.2f（来自 %s），按配捐规则 %.4g:1 触发（%s，配捐池总额 %.2f）；依据规则，配捐资金本身不再触发新的配捐",
		base, srcs, rule.Ratio, capNote, pool.Amount)
}
