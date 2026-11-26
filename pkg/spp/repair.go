package spp

import (
	"fmt"
	"sort"
)

// DefaultMaxRepairSize 限制最小修复搜索的动作集合上限。
const DefaultMaxRepairSize = 3

// Action 是一条可执行的最小策略调整。
type Action interface {
	// Kind 返回动作类别标识："preference_edit" 或 "export_edge_disable"。
	Kind() string
	// Describe 返回人类可读的中文描述。
	Describe() string
	// Key 给出去重用的稳定标识。
	Key() string
}

// PreferenceEdit 删除（禁用）AS 的某条本地偏好排名条目：该路径不再合法。
type PreferenceEdit struct {
	AS   int  `json:"as"`
	Path Path `json:"path"`
}

func (e PreferenceEdit) Kind() string { return "preference_edit" }

func (e PreferenceEdit) Describe() string {
	return fmt.Sprintf("在 AS%d 的本地偏好排名中禁用路径 [%s]", e.AS, e.Path)
}

func (e PreferenceEdit) Key() string {
	return fmt.Sprintf("pref:%d:%s", e.AS, e.Path.Key())
}

// ExportEdgeDisable 禁用一条非核心导出边（From 不再向 To 通告依赖该边的路由）。
type ExportEdgeDisable struct {
	From     int    `json:"from"`
	To       int    `json:"to"`
	EdgeKind string `json:"edgeKind"`
}

func (e ExportEdgeDisable) Kind() string { return "export_edge_disable" }

func (e ExportEdgeDisable) Describe() string {
	return fmt.Sprintf("禁用非核心导出边 AS%d -> AS%d (%s)", e.From, e.To, e.EdgeKind)
}

func (e ExportEdgeDisable) Key() string {
	return fmt.Sprintf("edge:%d:%d", e.From, e.To)
}

// RepairPlan 是一组最小修复动作及其修复后验证结果。
type RepairPlan struct {
	Actions            []Action      `json:"-"`
	ActionDescriptions []string      `json:"actionDescriptions"`
	TypedActions       []TypedAction `json:"typedActions"`
	RemainingWheels    int           `json:"remainingDisputeWheels"`
	StableAssignments  []Assignment  `json:"stableAssignments"`
	UniqueStable       bool          `json:"uniqueStable"`
	RestoresValleyFree bool          `json:"restoresValleyFree"`
}

// TypedAction 是修复动作的类型化 JSON 视图，保留类别与结构化参数。
type TypedAction struct {
	Kind     string `json:"kind"`
	AS       int    `json:"as,omitempty"`
	Path     Path   `json:"path,omitempty"`
	From     int    `json:"from,omitempty"`
	To       int    `json:"to,omitempty"`
	EdgeKind string `json:"edgeKind,omitempty"`
}

func toTyped(a Action) TypedAction {
	switch t := a.(type) {
	case PreferenceEdit:
		return TypedAction{Kind: a.Kind(), AS: t.AS, Path: t.Path}
	case ExportEdgeDisable:
		return TypedAction{Kind: a.Kind(), From: t.From, To: t.To, EdgeKind: t.EdgeKind}
	default:
		return TypedAction{Kind: a.Kind()}
	}
}

// RepairOptions 控制最小修复搜索。
type RepairOptions struct {
	MaxSize       int
	MaxWheelLimit int
	// OnlyValleyFree 为真时，只接受同时恢复严格无谷模型的修复方案。
	OnlyValleyFree bool
}

// nonCoreEdges 计算非核心导出边集合：
//   - 全部对等边（p2p，本身不承载客户-供应商核心可达性）；
//   - 无谷违规路径中制造“谷”的那条违规备份上行边（状态机记录的
//     第一条非法跳），而不是路径上的所有 c2p 边——通往供应商的核心
//     c2p 链路永远不允许被当作可禁用边。
func nonCoreEdges(g *Graph, gr *GRReport) map[string]Edge {
	out := make(map[string]Edge)
	for _, as := range g.SortedNodes() {
		for _, e := range g.Out(as) {
			if e.Kind == KindP2P {
				out[edgeKey(e.From, e.To)] = e
			}
		}
	}
	for _, v := range gr.ValleyFreeViolations {
		if i := offendingBackupHop(v.Path, g); i >= 0 {
			if e := g.edge(v.Path[i], v.Path[i+1]); e != nil {
				out[edgeKey(e.From, e.To)] = *e
			}
		}
	}
	return out
}

// offendingBackupHop 复现无谷状态机，定位打破单调性的备份链路：
// 即非法上行（谷顶）之前的首个“转向跳”——路径离开纯上行段的第一条
// p2p/p2c 边。该边才是可禁用的非核心备份链路。
func offendingBackupHop(p Path, g *Graph) int {
	ph := phaseUp
	turn := -1
	for i := 0; i+1 < len(p); i++ {
		e := g.edge(p[i], p[i+1])
		if e == nil {
			return i
		}
		switch e.Kind {
		case KindC2P:
			if ph != phaseUp && turn >= 0 {
				return turn // 谷顶非法上行：根因是此前的转向备份链路
			}
		case KindP2P:
			if ph != phaseUp {
				return turn
			}
			turn = i
			ph = phasePeer
		case KindP2C:
			if turn < 0 {
				turn = i
			}
			ph = phaseDown
		}
	}
	return -1
}

func edgeKey(from, to int) string { return fmt.Sprintf("%d:%d", from, to) }

// candidateActions 生成全部候选动作，按类别与位置确定性排序。
func candidateActions(g *Graph, gr *GRReport) []Action {
	var actions []Action
	for _, as := range g.SortedNodes() {
		if as == g.Destination {
			continue
		}
		for _, p := range g.Preferences(as) {
			actions = append(actions, PreferenceEdit{AS: as, Path: copyPath(p)})
		}
	}
	nc := nonCoreEdges(g, gr)
	keys := make([]string, 0, len(nc))
	for k := range nc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := nc[k]
		actions = append(actions, ExportEdgeDisable{From: e.From, To: e.To, EdgeKind: string(e.Kind)})
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Kind() != actions[j].Kind() {
			return actions[i].Kind() < actions[j].Kind()
		}
		return actions[i].Key() < actions[j].Key()
	})
	return actions
}

// applyActions 在动作集合上构造过滤后的新实例（原图保持不变）。
func (g *Graph) applyActions(cfg *Config, actions []Action) (*Graph, error) {
	disabled := make(map[string]bool)
	dropPrefs := make(map[string]map[string]bool)
	for _, a := range actions {
		switch t := a.(type) {
		case PreferenceEdit:
			if dropPrefs[itoa(t.AS)] == nil {
				dropPrefs[itoa(t.AS)] = make(map[string]bool)
			}
			dropPrefs[itoa(t.AS)][t.Path.Key()] = true
		case ExportEdgeDisable:
			disabled[edgeKey(t.From, t.To)] = true
		default:
			return nil, fmt.Errorf("未知修复动作类型 %T", a)
		}
	}

	filtered := &Config{
		Destination: cfg.Destination,
		Nodes:       append([]int(nil), cfg.Nodes...),
		Preferences: make(map[string][]Path),
	}
	for _, e := range cfg.Edges {
		if disabled[edgeKey(e.From, e.To)] {
			continue
		}
		filtered.Edges = append(filtered.Edges, e)
	}
	for _, as := range g.SortedNodes() {
		var kept []Path
		for _, p := range g.Preferences(as) {
			if dropPrefs[itoa(as)] != nil && dropPrefs[itoa(as)][p.Key()] {
				continue
			}
			bad := false
			for i := 0; i+1 < len(p); i++ {
				if disabled[edgeKey(p[i], p[i+1])] {
					bad = true
					break
				}
			}
			if !bad {
				kept = append(kept, copyPath(p))
			}
		}
		if len(kept) > 0 || as == cfg.Destination {
			filtered.Preferences[itoa(as)] = kept
		}
	}
	return Load(filtered)
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }

func (g *Graph) verify(cfg *Config, actions []Action, opts RepairOptions) (*RepairPlan, error) {
	ng, err := g.applyActions(cfg, actions)
	if err != nil {
		return nil, err
	}
	nr := analyzeGR(ng)
	wheels := ng.FindDisputeWheels(opts.MaxWheelLimit)
	if opts.MaxWheelLimit <= 0 {
		wheels = ng.FindDisputeWheels(DefaultWheelLimit)
	}
	solved := ng.Solve(Options{})
	assignments := solved.Assignments
	if assignments == nil {
		assignments = []Assignment{}
	}
	plan := &RepairPlan{
		Actions:            append([]Action(nil), actions...),
		ActionDescriptions: []string{},
		TypedActions:       []TypedAction{},
		RemainingWheels:    len(wheels),
		StableAssignments:  assignments,
		UniqueStable:       len(solved.Assignments) == 1 && len(wheels) == 0,
		RestoresValleyFree: nr.StrictlyValleyFree(),
	}
	for _, a := range actions {
		plan.ActionDescriptions = append(plan.ActionDescriptions, a.Describe())
		plan.TypedActions = append(plan.TypedActions, toTyped(a))
	}
	return plan, nil
}

// MinimalRepair 自底向上枚举动作子集，返回消除全部争议轮且修复后恰有唯一
// 稳态的最小集合（动作数量最少；同尺寸取字典序最先者）。
// 不存在可行修复时返回 nil。
func (g *Graph) MinimalRepair(cfg *Config, gr *GRReport, opts RepairOptions) (*RepairPlan, error) {
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultMaxRepairSize
	}
	candidates := candidateActions(g, gr)

	// 先确认空动作集合不满足条件。
	baseline, err := g.verify(cfg, nil, opts)
	if err != nil {
		return nil, err
	}
	if baseline.UniqueStable && (!opts.OnlyValleyFree || baseline.RestoresValleyFree) {
		return baseline, nil
	}

	for k := 1; k <= opts.MaxSize; k++ {
		found := searchCombinations(g, cfg, candidates, k, opts)
		if found != nil {
			return found, nil
		}
	}
	return nil, nil
}

// searchCombinations 枚举所有大小恰为 k 的候选组合，命中即返回完整验证报告。
func searchCombinations(g *Graph, cfg *Config, candidates []Action, k int, opts RepairOptions) *RepairPlan {
	combo := make([]Action, 0, k)
	var rec func(start int) *RepairPlan
	rec = func(start int) *RepairPlan {
		if len(combo) == k {
			plan, err := g.verify(cfg, combo, opts)
			if err == nil && plan.UniqueStable && (!opts.OnlyValleyFree || plan.RestoresValleyFree) {
				return plan
			}
			return nil
		}
		for i := start; i < len(candidates); i++ {
			combo = append(combo, candidates[i])
			if plan := rec(i + 1); plan != nil {
				return plan
			}
			combo = combo[:len(combo)-1]
		}
		return nil
	}
	return rec(0)
}
