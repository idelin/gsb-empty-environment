package spp

import (
	"fmt"
	"sort"
)

// GRReport 汇总 Gao-Rexford 商业无谷模型核验结果。
type GRReport struct {
	// CommercialCycles 列出检测到的客户-供应商环（每个环为沿 c2p 方向的
	// AS 序列，首尾相连）。空切片表示无商业环。
	CommercialCycles [][]int `json:"commercialCycles"`
	// ValleyFreeViolations 列出被策略允许但违反无谷/导出过滤规则的路径。
	ValleyFreeViolations []ValleyFreeViolation `json:"valleyFreeViolations"`
	// MissingLegalPaths 列出满足无谷模型却未被该 AS 偏好序包含的合法路径。
	MissingLegalPaths []LegalPathGap `json:"missingLegalPaths"`
}

// ValleyFreeViolation 描述一条破坏 Gao-Rexford 导出规则的已配置路径。
type ValleyFreeViolation struct {
	AS     int    `json:"as"`
	Path   Path   `json:"path"`
	Reason string `json:"reason"`
}

// LegalPathGap 描述一条无谷合法但未出现在偏好序中的路径。
type LegalPathGap struct {
	AS   int  `json:"as"`
	Path Path `json:"path"`
}

// StrictlyValleyFree 在无商业环且无任何无谷违规时为真。
func (r *GRReport) StrictlyValleyFree() bool {
	return len(r.CommercialCycles) == 0 && len(r.ValleyFreeViolations) == 0
}

// phase 是无谷路径状态机的阶段。
type phase int

const (
	phaseUp   phase = iota // 仍可继续上行 (c2p) 或跨越一次对等边
	phasePeer              // 已跨越对等边，只能下行
	phaseDown              // 已进入下行段，只能继续 p2c
)

// ValleyFreeLegalPaths 按 Gao-Rexford 无谷规则展开从 as 到源站的全部
// 合法简单路径，模式为 up* (peer)? down*。
//
// 该规则等价于标准导出过滤：客户路由可向任意邻居通告；来自对等体或
// 供应商的路由只能通告给自己的客户——因此路径中既不允许“谷”
// （下行/对等之后再上行），也不允许第二条对等边。
func (g *Graph) ValleyFreeLegalPaths(as int) []Path {
	var found []Path
	visited := map[int]bool{as: true}

	var dfs func(cur int, ph phase, prefix Path)
	dfs = func(cur int, ph phase, prefix Path) {
		if cur == g.Destination {
			found = append(found, copyPath(prefix))
			return
		}
		for _, e := range g.edges[cur] {
			if visited[e.To] {
				continue
			}
			next := ph
			switch e.Kind {
			case KindC2P:
				if ph != phaseUp {
					continue // 下行或对等之后再上行形成“谷”
				}
			case KindP2P:
				if ph != phaseUp {
					continue // 至多一条对等边，且必须位于山顶
				}
				next = phasePeer
			case KindP2C:
				next = phaseDown
			}
			visited[e.To] = true
			dfs(e.To, next, append(prefix, e.To))
			delete(visited, e.To)
		}
	}

	dfs(as, phaseUp, Path{as})
	sort.Slice(found, func(i, j int) bool { return pathLess(found[i], found[j]) })
	return found
}

func pathLess(a, b Path) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// valleyFreeReason 沿状态机走查路径，返回第一条违规边的中文解释。
func (g *Graph) valleyFreeReason(p Path) string {
	ph := phaseUp
	for i := 0; i+1 < len(p); i++ {
		e := g.edge(p[i], p[i+1])
		if e == nil {
			return fmt.Sprintf("跳 %d->%d 不存在", p[i], p[i+1])
		}
		switch e.Kind {
		case KindC2P:
			if ph == phaseDown {
				return fmt.Sprintf("跳 %d->%d 为客户->供应商上行，但路径此前已下行，形成无谷模型禁止的“谷”（供应商路由被转运给上游）", p[i], p[i+1])
			}
			if ph == phasePeer {
				return fmt.Sprintf("跳 %d->%d 为客户->供应商上行，但路径此前已跨越对等边，违反“对等路由不向非客户转运”的导出过滤", p[i], p[i+1])
			}
		case KindP2P:
			if ph == phasePeer {
				return fmt.Sprintf("跳 %d->%d 是第二条对等边，无谷路径至多允许山顶处一条对等边", p[i], p[i+1])
			}
			if ph == phaseDown {
				return fmt.Sprintf("跳 %d->%d 为对等边，但路径此前已下行，违反对等路由不向非客户转运的规则", p[i], p[i+1])
			}
			ph = phasePeer
		case KindP2C:
			ph = phaseDown
		}
	}
	return "路径违反 Gao-Rexford 无谷模型"
}

// FindCommercialCycles 在客户->供应商 (c2p) 有向子图上检测商业环。
// 返回找到的环列表，每个环为沿 c2p 方向首尾相接的 AS 序列。
func (g *Graph) FindCommercialCycles() [][]int {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int]int, len(g.Nodes))
	var stack []int
	var cycles [][]int

	var dfs func(u int)
	dfs = func(u int) {
		color[u] = gray
		stack = append(stack, u)
		for _, e := range g.edges[u] {
			if e.Kind != KindC2P {
				continue
			}
			switch color[e.To] {
			case white:
				dfs(e.To)
			case gray:
				start := 0
				for stack[start] != e.To {
					start++
				}
				cycle := append([]int(nil), stack[start:]...)
				cycle = append(cycle, e.To)
				cycles = append(cycles, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
	}

	for _, as := range g.SortedNodes() {
		if color[as] == white {
			dfs(as)
		}
	}
	return cycles
}

// analyzeGR 对整个实例执行无谷展开核验。
func analyzeGR(g *Graph) *GRReport {
	report := &GRReport{
		CommercialCycles:     [][]int{},
		ValleyFreeViolations: []ValleyFreeViolation{},
		MissingLegalPaths:    []LegalPathGap{},
	}
	for _, c := range g.FindCommercialCycles() {
		report.CommercialCycles = append(report.CommercialCycles, c)
	}
	for _, as := range g.SortedNodes() {
		if as == g.Destination {
			continue
		}
		legal := g.ValleyFreeLegalPaths(as)
		legalSet := make(map[string]Path, len(legal))
		for _, p := range legal {
			legalSet[p.Key()] = p
		}
		configured := g.Preferences(as)
		configuredSet := make(map[string]bool, len(configured))
		for _, p := range configured {
			configuredSet[p.Key()] = true
			if _, ok := legalSet[p.Key()]; !ok {
				report.ValleyFreeViolations = append(report.ValleyFreeViolations, ValleyFreeViolation{
					AS:     as,
					Path:   copyPath(p),
					Reason: g.valleyFreeReason(p),
				})
			}
		}
		for _, p := range legal {
			if !configuredSet[p.Key()] {
				report.MissingLegalPaths = append(report.MissingLegalPaths, LegalPathGap{AS: as, Path: copyPath(p)})
			}
		}
	}
	sort.Slice(report.ValleyFreeViolations, func(i, j int) bool {
		if report.ValleyFreeViolations[i].AS != report.ValleyFreeViolations[j].AS {
			return report.ValleyFreeViolations[i].AS < report.ValleyFreeViolations[j].AS
		}
		return pathLess(report.ValleyFreeViolations[i].Path, report.ValleyFreeViolations[j].Path)
	})
	sort.Slice(report.MissingLegalPaths, func(i, j int) bool {
		if report.MissingLegalPaths[i].AS != report.MissingLegalPaths[j].AS {
			return report.MissingLegalPaths[i].AS < report.MissingLegalPaths[j].AS
		}
		return pathLess(report.MissingLegalPaths[i].Path, report.MissingLegalPaths[j].Path)
	})
	return report
}
