package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Evidence 描述一项可采集的证据。
type Evidence struct {
	ID         string `json:"id"`
	Cost       int64  `json:"cost"`
	ValidUntil string `json:"validUntil"` // YYYY-MM-DD，含当日；空表示长期有效
	MutexGroup string `json:"mutexGroup,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Criterion 描述一条验收条目及其布尔表达式。
type Criterion struct {
	ID   string `json:"id"`
	Expr string `json:"expr"`
}

// Input 是规划器的完整输入。
type Input struct {
	AsOf     string      `json:"asOf"` // 取证基准日 YYYY-MM-DD，空表示今天
	MaxItems int         `json:"maxItems"`
	Evidence []Evidence  `json:"evidence"`
	Criteria []Criterion `json:"criteria"`
}

// CriterionResult 是单条验收的结论。
type CriterionResult struct {
	ID        string `json:"id"`
	Expr      string `json:"expr"`
	Satisfied bool   `json:"satisfied"`
	Path      string `json:"path,omitempty"`   // 满足路径
	Reason    string `json:"reason,omitempty"` // 未满足原因
}

// Plan 是规划输出。
type Plan struct {
	Feasible  bool              `json:"feasible"`
	TotalCost int64             `json:"totalCost"`
	Selected  []Evidence        `json:"selected"`
	Expired   []Evidence        `json:"expired"` // 已过期、不计入的证据
	Results   []CriterionResult `json:"results"`
	Note      string            `json:"note,omitempty"`
}

const defaultMaxItems = 25
const maxCandidates = 60 // 位掩码求解的候选证据上限

type compiledCriterion struct {
	id   string
	expr string
	root *Node
}

// Solve 求最低成本取证计划。
func Solve(in *Input) (*Plan, error) {
	if len(in.Criteria) == 0 {
		return nil, fmt.Errorf("至少需要一条验收条目")
	}
	maxItems := in.MaxItems
	if maxItems <= 0 {
		maxItems = defaultMaxItems
	}

	asOf := time.Now()
	if in.AsOf != "" {
		t, err := time.Parse("2006-01-02", in.AsOf)
		if err != nil {
			return nil, fmt.Errorf("asOf 日期格式错误: %v", err)
		}
		asOf = t
	}
	asOfDay := asOf.Truncate(24 * time.Hour)

	// 1. 剔除过期证据（过期证据不计入、不可选）。
	var valid, expired []Evidence
	seen := map[string]bool{}
	for _, ev := range in.Evidence {
		if ev.ID == "" {
			return nil, fmt.Errorf("存在缺少 id 的证据")
		}
		if seen[ev.ID] {
			return nil, fmt.Errorf("证据 id 重复: %s", ev.ID)
		}
		seen[ev.ID] = true
		if ev.Cost < 0 {
			return nil, fmt.Errorf("证据 %s 成本为负", ev.ID)
		}
		if ev.ValidUntil != "" {
			vu, err := time.Parse("2006-01-02", ev.ValidUntil)
			if err != nil {
				return nil, fmt.Errorf("证据 %s 的 validUntil 格式错误: %v", ev.ID, err)
			}
			if asOfDay.After(vu) {
				expired = append(expired, ev)
				continue
			}
		}
		valid = append(valid, ev)
	}
	if len(valid) > maxCandidates {
		return nil, fmt.Errorf("有效证据 %d 项，超过求解上限 %d", len(valid), maxCandidates)
	}

	// 2. 编译验收表达式。
	var crits []compiledCriterion
	for _, c := range in.Criteria {
		root, err := ParseExpr(c.Expr)
		if err != nil {
			return nil, fmt.Errorf("验收 %s 表达式错误: %v", c.ID, err)
		}
		crits = append(crits, compiledCriterion{id: c.ID, expr: c.Expr, root: root})
	}

	// 3. 候选证据按成本升序编号（利于剪枝）。
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Cost < valid[j].Cost })
	bitOf := map[string]int{}
	for i, ev := range valid {
		bitOf[ev.ID] = i
	}
	for _, c := range crits {
		for _, ref := range leafRefs(c.root) {
			if _, ok := bitOf[ref]; !ok {
				if !seen[ref] {
					return nil, fmt.Errorf("验收 %s 引用了不存在的证据 %s", c.id, ref)
				}
				// 引用的是已过期证据：合法，但求值恒为 false。
			}
		}
	}

	// 互斥组 -> 组位。
	groupBit := map[string]int{}
	groups := make([]int, len(valid))
	for i, ev := range valid {
		groups[i] = -1
		if ev.MutexGroup != "" {
			b, ok := groupBit[ev.MutexGroup]
			if !ok {
				b = len(groupBit)
				groupBit[ev.MutexGroup] = b
			}
			groups[i] = b
		}
	}

	// 4. 分支定界搜索最低成本子集。
	s := &solver{
		costs:    make([]int64, len(valid)),
		groups:   groups,
		roots:    make([]*Node, len(crits)),
		bitOf:    bitOf,
		maxItems: maxItems,
	}
	for i, ev := range valid {
		s.costs[i] = ev.Cost
	}
	for i, c := range crits {
		s.roots[i] = c.root
	}
	rem := make([]uint64, len(valid)+1)
	for i := len(valid) - 1; i >= 0; i-- {
		rem[i] = rem[i+1] | uint64(1)<<uint(i)
	}
	s.search(0, 0, 0, 0, 0, rem)

	plan := &Plan{Expired: expired}

	// 5. 汇总每条验收的结论。
	if s.found {
		plan.Feasible = true
		plan.TotalCost = s.bestCost
		for i, ev := range valid {
			if s.best&(uint64(1)<<uint(i)) != 0 {
				plan.Selected = append(plan.Selected, ev)
			}
		}
		sort.SliceStable(plan.Selected, func(i, j int) bool { return plan.Selected[i].ID < plan.Selected[j].ID })
		for _, c := range crits {
			plan.Results = append(plan.Results, explainCriterion(c, s.best, valid, bitOf, expired))
		}
	} else {
		hard := hardInfeasible(crits, bitOf, rem[0])
		plan.Note = infeasibleNote(hard, maxItems)
		for _, c := range crits {
			plan.Results = append(plan.Results, explainInfeasible(c, valid, bitOf, expired, rem[0], hard))
		}
	}
	return plan, nil
}

type solver struct {
	costs    []int64
	groups   []int
	roots    []*Node
	bitOf    map[string]int
	maxItems int
	found    bool
	best     uint64
	bestCost int64
}

// allSat 检查全部验收是否满足（严格按 AND/OR 语义逐条求值）。
func (s *solver) allSat(mask uint64) bool {
	for _, r := range s.roots {
		if !EvalMask(r, mask, s.bitOf) {
			return false
		}
	}
	return true
}

// search 在候选 i 处分支。mask 为已选证据位，gbits 为已占用的互斥组位。
func (s *solver) search(i int, mask, gbits uint64, cost int64, count int, rem []uint64) {
	if s.found && cost >= s.bestCost {
		return
	}
	// 乐观上界：剩余证据全选也无法满足全部验收，则剪枝。
	if !s.allSat(mask | rem[i]) {
		return
	}
	if s.allSat(mask) {
		if !s.found || cost < s.bestCost {
			s.found, s.best, s.bestCost = true, mask, cost
		}
		return
	}
	if i == len(s.costs) || count >= s.maxItems {
		return
	}
	// 分支一：选入证据 i（成本升序，优先尝试低成本）。
	if s.groups[i] < 0 || gbits&(uint64(1)<<uint(s.groups[i])) == 0 {
		g := gbits
		if s.groups[i] >= 0 {
			g |= uint64(1) << uint(s.groups[i])
		}
		s.search(i+1, mask|(uint64(1)<<uint(i)), g, cost+s.costs[i], count+1, rem)
	}
	// 分支二：不选证据 i。
	s.search(i+1, mask, gbits, cost, count, rem)
}

func leafRefs(n *Node) []string {
	if n.Op == OpLeaf {
		return []string{n.Ref}
	}
	var out []string
	for _, k := range n.Kids {
		out = append(out, leafRefs(k)...)
	}
	return out
}

// explainCriterion 对单条验收生成满足路径或未满足原因。
func explainCriterion(c compiledCriterion, mask uint64, valid []Evidence, bitOf map[string]int, expired []Evidence) CriterionResult {
	res := CriterionResult{ID: c.id, Expr: c.expr}
	leaf := leafStatusFn(mask, valid, bitOf, expired)
	e := explain(c.root, leaf)
	res.Satisfied = e.sat
	if e.sat {
		res.Path = satisfactionPath(e)
	} else {
		res.Reason = unsatReason(e)
	}
	return res
}

// leafStatusFn 返回叶子状态函数：已采集 / 已过期 / 互斥排除 / 未采集。
func leafStatusFn(mask uint64, valid []Evidence, bitOf map[string]int, expired []Evidence) func(string) (bool, string) {
	expiredUntil := map[string]string{}
	for _, ev := range expired {
		expiredUntil[ev.ID] = ev.ValidUntil
	}
	// 已选证据占用的互斥组。
	usedGroup := map[string]string{}
	for i, ev := range valid {
		if mask&(uint64(1)<<uint(i)) != 0 && ev.MutexGroup != "" {
			usedGroup[ev.MutexGroup] = ev.ID
		}
	}
	byID := map[string]Evidence{}
	for _, ev := range valid {
		byID[ev.ID] = ev
	}
	return func(id string) (bool, string) {
		if i, ok := bitOf[id]; ok {
			if mask&(uint64(1)<<uint(i)) != 0 {
				return true, fmt.Sprintf("证据 %s 已采集", id)
			}
			ev := byID[id]
			if ev.MutexGroup != "" {
				if other, clash := usedGroup[ev.MutexGroup]; clash {
					return false, fmt.Sprintf("证据 %s 未采集（与已采集的 %s 互斥，组 %q）", id, other, ev.MutexGroup)
				}
			}
			return false, fmt.Sprintf("证据 %s 未采集", id)
		}
		if until, wasValid := expiredUntil[id]; wasValid {
			return false, fmt.Sprintf("证据 %s 已过期（有效期至 %s），不计入", id, until)
		}
		return false, fmt.Sprintf("证据 %s 不存在", id)
	}
}

// hardInfeasible 返回即使采集全部有效证据也无法满足的验收条目。
func hardInfeasible(crits []compiledCriterion, bitOf map[string]int, allValid uint64) map[string]bool {
	hard := map[string]bool{}
	for _, c := range crits {
		if !EvalMask(c.root, allValid, bitOf) {
			hard[c.id] = true
		}
	}
	return hard
}

// explainInfeasible 在整体不可行时，区分“结构性不可满足”与“被其它条目拖累”。
func explainInfeasible(c compiledCriterion, valid []Evidence, bitOf map[string]int, expired []Evidence, allValid uint64, hard map[string]bool) CriterionResult {
	res := CriterionResult{ID: c.id, Expr: c.expr}
	if !hard[c.id] {
		if len(hard) > 0 {
			ids := make([]string, 0, len(hard))
			for id := range hard {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			res.Reason = fmt.Sprintf("本身可满足，但验收 %s 结构性不可满足，整体计划不可行", strings.Join(ids, ", "))
		} else {
			res.Reason = "单独可满足，但与其它验收条目组合时受互斥条件或数量上限约束，无法同时满足"
		}
		return res
	}
	// 用“全部有效证据”掩码解释，暴露结构性缺口（过期/缺失证据）。
	leaf := leafStatusFn(allValid, valid, bitOf, expired)
	e := explain(c.root, leaf)
	res.Reason = unsatReason(e)
	return res
}

func infeasibleNote(hard map[string]bool, maxItems int) string {
	if len(hard) > 0 {
		ids := make([]string, 0, len(hard))
		for id := range hard {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return fmt.Sprintf("无可行计划：验收 %s 即使采集全部有效证据也无法满足（证据过期或缺失）", strings.Join(ids, ", "))
	}
	return fmt.Sprintf("无可行计划：互斥执行条件或数量上限（%d 项）导致全部验收无法同时满足", maxItems)
}
