package main

import (
	"errors"
	"fmt"
	"sort"
)

// Edge 描述一条依赖边。Cuttable 为 true 时表示该边被授权可用
// 指定引导产物切断，切断需支付 Cost。
type Edge struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Cuttable bool    `json:"cuttable"`
	Artifact string  `json:"artifact,omitempty"`
	Cost     float64 `json:"cost,omitempty"`
}

// GraphSpec 是工具的输入：依赖图与可切边授权信息。
type GraphSpec struct {
	Nodes []string `json:"nodes,omitempty"`
	Edges []Edge   `json:"edges"`
}

// CutEdge 是最终方案里一条被切断的边及其所用引导产物。
type CutEdge struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Artifact string  `json:"artifact"`
	Cost     float64 `json:"cost"`
}

// Plan 是输出：总成本、切边清单（含产物）与拓扑构建计划。
// Stages 中同一阶段的节点可并行构建，阶段之间必须按序进行。
type Plan struct {
	TotalCost float64    `json:"totalCost"`
	Cuts      []CutEdge  `json:"cuts"`
	Stages    [][]string `json:"stages"`
}

const maxCuttable = 25

var errInfeasible = errors.New("图中存在不可切断的循环，无法拆解")

type solver struct {
	nodes []string
	index map[string]int
	edges []Edge
	adj   [][]int // 节点 -> 出边（edges 下标）

	cutIdx []int // 可切边在 edges 中的下标，位 i 对应 cutIdx[i]
	bitOf  []int // edges 下标 -> 可切边位号，不可切边为 -1

	memo   map[uint32]float64
	choice map[uint32]int // 在状态 mask 下选择切断的可切边位号
}

// Solve 求最便宜的引导集合，使剩余依赖图无环。
func Solve(spec GraphSpec) (*Plan, error) {
	s, err := newSolver(spec)
	if err != nil {
		return nil, err
	}

	// 不可切边自身若成环，则任何引导集合都无解。
	if cyc := s.findCycleExcludingCuttable(); len(cyc) > 0 {
		return nil, fmt.Errorf("%v：循环 %s 中没有已授权的可切边", errInfeasible, s.cycleString(cyc))
	}

	full := s.fullMask()
	cost := s.dp(full)
	if cost < 0 {
		return nil, errInfeasible
	}

	cutBits := s.reconstruct(full)
	plan := &Plan{TotalCost: cost}
	keep := make([]bool, len(s.edges))
	for i := range s.edges {
		keep[i] = true
	}
	for i, ei := range s.cutIdx {
		if cutBits&(1<<uint(i)) != 0 {
			keep[ei] = false
			e := s.edges[ei]
			plan.Cuts = append(plan.Cuts, CutEdge{From: e.From, To: e.To, Artifact: e.Artifact, Cost: e.Cost})
		}
	}
	sort.Slice(plan.Cuts, func(i, j int) bool {
		if plan.Cuts[i].From != plan.Cuts[j].From {
			return plan.Cuts[i].From < plan.Cuts[j].From
		}
		return plan.Cuts[i].To < plan.Cuts[j].To
	})
	plan.Stages = s.topoStages(keep)
	return plan, nil
}

func newSolver(spec GraphSpec) (*solver, error) {
	s := &solver{index: make(map[string]int), memo: make(map[uint32]float64), choice: make(map[uint32]int)}
	nodeID := func(name string) int {
		if id, ok := s.index[name]; ok {
			return id
		}
		id := len(s.nodes)
		s.index[name] = id
		s.nodes = append(s.nodes, name)
		s.adj = append(s.adj, nil)
		return id
	}
	for _, n := range spec.Nodes {
		nodeID(n)
	}
	for i, e := range spec.Edges {
		if e.From == "" || e.To == "" {
			return nil, fmt.Errorf("第 %d 条边缺少 from/to", i)
		}
		if e.Cuttable {
			if e.Artifact == "" {
				return nil, fmt.Errorf("可切边 %s -> %s 未指定引导产物", e.From, e.To)
			}
			if e.Cost < 0 {
				return nil, fmt.Errorf("可切边 %s -> %s 成本不能为负", e.From, e.To)
			}
		}
		nodeID(e.From)
		nodeID(e.To)
		s.edges = append(s.edges, e)
		ei := len(s.edges) - 1
		from := s.index[e.From]
		s.adj[from] = append(s.adj[from], ei)
		if e.Cuttable {
			s.cutIdx = append(s.cutIdx, ei)
		}
	}
	if len(s.cutIdx) > maxCuttable {
		return nil, fmt.Errorf("可切边数量 %d 超过上限 %d", len(s.cutIdx), maxCuttable)
	}
	s.bitOf = make([]int, len(s.edges))
	for i := range s.bitOf {
		s.bitOf[i] = -1
	}
	for i, ei := range s.cutIdx {
		s.bitOf[ei] = i
	}
	return s, nil
}

func (s *solver) fullMask() uint32 {
	if len(s.cutIdx) == 0 {
		return 0
	}
	return uint32(1)<<uint(len(s.cutIdx)) - 1
}

// edgeActive 判断边在状态 mask 下是否仍然存在（未被切断）。
func (s *solver) edgeActive(ei int, mask uint32) bool {
	bit := s.bitOf[ei]
	if bit < 0 {
		return true
	}
	return mask&(1<<uint(bit)) != 0
}

// findCycle 在状态 mask 对应的图中找一条环，返回组成环的边下标；无环返回 nil。
func (s *solver) findCycle(mask uint32) []int {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(s.nodes))
	parentEdge := make([]int, len(s.nodes))
	parentNode := make([]int, len(s.nodes))
	for i := range parentEdge {
		parentEdge[i] = -1
		parentNode[i] = -1
	}

	var cycle []int
	var visit func(u int) bool
	visit = func(u int) bool {
		color[u] = gray
		for _, ei := range s.adj[u] {
			if !s.edgeActive(ei, mask) {
				continue
			}
			v := s.index[s.edges[ei].To]
			if v == u { // 自环
				cycle = []int{ei}
				return true
			}
			switch color[v] {
			case gray:
				cycle = []int{ei}
				for x := u; x != v; x = parentNode[x] {
					cycle = append(cycle, parentEdge[x])
				}
				// 反转得到沿环方向的边序列
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return true
			case white:
				parentEdge[v] = ei
				parentNode[v] = u
				if visit(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}
	for u := range s.nodes {
		if color[u] == white && visit(u) {
			return cycle
		}
	}
	return nil
}

// findCycleExcludingCuttable 只由不可切边构成的图中找环。
func (s *solver) findCycleExcludingCuttable() []int {
	return s.findCycle(0)
}

func (s *solver) cycleString(cyc []int) string {
	out := ""
	for i, ei := range cyc {
		if i > 0 {
			out += " -> "
		}
		out += s.edges[ei].From
	}
	if len(cyc) > 0 {
		out += " -> " + s.edges[cyc[len(cyc)-1]].To
	}
	return out
}

// dp 返回在状态 mask（尚存的可切边集合）下，使图无环还需支付的最小成本。
// 无解返回 -1。
func (s *solver) dp(mask uint32) float64 {
	if v, ok := s.memo[mask]; ok {
		return v
	}
	cyc := s.findCycle(mask)
	if len(cyc) == 0 {
		s.memo[mask] = 0
		return 0
	}
	best := -1.0
	bestBit := -1
	seen := make(map[int]bool)
	for _, ei := range cyc {
		bit := s.bitOf[ei]
		if bit < 0 || seen[bit] {
			continue // 不可切边不能选；同一可切边只分支一次
		}
		seen[bit] = true
		sub := s.dp(mask &^ (1 << uint(bit)))
		if sub < 0 {
			continue
		}
		cand := s.edges[ei].Cost + sub
		if best < 0 || cand < best {
			best = cand
			bestBit = bit
		}
	}
	s.memo[mask] = best
	s.choice[mask] = bestBit
	return best
}

// reconstruct 根据 choice 表还原被切断的可切边位集合。
func (s *solver) reconstruct(mask uint32) uint32 {
	cut := uint32(0)
	for {
		cyc := s.findCycle(mask)
		if len(cyc) == 0 {
			return cut
		}
		bit := s.choice[mask]
		if bit < 0 {
			return cut
		}
		cut |= 1 << uint(bit)
		mask &^= 1 << uint(bit)
	}
}

// topoStages 对保留的边做分层拓扑排序，生成构建计划。
func (s *solver) topoStages(keep []bool) [][]string {
	indeg := make([]int, len(s.nodes))
	for ei, e := range s.edges {
		if !keep[ei] {
			continue
		}
		indeg[s.index[e.To]]++
	}
	var cur []int
	for u := range s.nodes {
		if indeg[u] == 0 {
			cur = append(cur, u)
		}
	}
	sort.Ints(cur)
	var stages [][]string
	done := 0
	for len(cur) > 0 {
		stage := make([]string, len(cur))
		var next []int
		for i, u := range cur {
			stage[i] = s.nodes[u]
			done++
			for _, ei := range s.adj[u] {
				if !keep[ei] {
					continue
				}
				v := s.index[s.edges[ei].To]
				indeg[v]--
				if indeg[v] == 0 {
					next = append(next, v)
				}
			}
		}
		sort.Ints(next)
		stages = append(stages, stage)
		cur = next
	}
	return stages
}
