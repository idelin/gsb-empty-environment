package loadshed

import (
	"math"
	"sort"
)

// Solution 是一次静态求解的完整结果。
type Solution struct {
	// Admission 是各入口各优先级的放行比例。
	Admission Admission
	// CutEdges 是建议切断的 SOFT 边编号集合（升序）。
	CutEdges []int
	// EdgeScale 是与 CutEdges 对应的生效比例（静态求解时被切边为 0，其余为 1）。
	EdgeScale []EdgeScale
	// Routing 是应用切断方案后的路由系数。
	Routing *Routing
	// LoadPerPriority[v][p] 是节点 v 上优先级 p 的实际负载。
	LoadPerPriority [][NumPriorities]float64
	// Load[v] 是节点 v 的实际总负载。
	Load []float64
	// Retained[p] 是优先级 p 全网被保留（放行）的入口流量总量。
	Retained [NumPriorities]float64
	// Rejected[p] 是优先级 p 全网被丢弃的入口流量总量。
	Rejected [NumPriorities]float64
	// DegradeCost 是切断 SOFT 边的降级代价之和。
	DegradeCost float64
	// FullyAdmitted 为 true 表示四档入口流量全部零拒绝（仅可能存在 SOFT 降级）。
	// 为 false 时说明即便切断全部可切 SOFT 边，仍必须脱落部分用户请求才能满足容量。
	FullyAdmitted bool
}

// Solve 执行“先弱依赖降级、再严格优先级字典序脱落”的静态求解。
// 图必须由 NewGraph 成功构建（含环图在构建阶段即返回 *CycleError，无法进入求解）。
func Solve(g *Graph) (*Solution, error) {
	if g == nil {
		return nil, errNilGraph
	}
	cut := map[int]bool{}
	// locked[entry][p] 记录更高/当前优先级已锁定的最优放行速率，低档不得挤占。
	locked := make([][NumPriorities]float64, len(g.Nodes))

	// 逐档求解：每档先在当前切断集合下做 Max-Min 水填；不足则按“代价/缓解”最优切一条 SOFT 边后重试。
	for p := Priority(0); p < NumPriorities; p++ {
		for {
			routing, err := g.BuildRouting(cutScales(cut))
			if err != nil {
				return nil, err
			}
			adm, retained := runTier(g, routing, p, locked)
			want := g.TotalArrival(p)
			for v := range g.Nodes {
				locked[v][p] = g.Nodes[v].Arrival[p] * adm[v][p]
			}
			edgeIdx, ok := pickSoftEdge(g, cut, p, locked, adm)
			// 本档已全量保留，或已没有可缓解瓶颈的 SOFT 边时，才结束本档进入下一档。
			if retained >= want-Eps || want == 0 || !ok {
				// 没有可缓解瓶颈的 SOFT 边：接受容量内的最优 Max-Min 分配，进入下一档。
				break
			}
			cut[edgeIdx] = true
		}
	}

	// 冗余清理：按代价从高到低尝试恢复被切边，只要恢复后字典序放行量不劣化即恢复，得到最小代价切集。
	cut = pruneRedundantCuts(g, cut)

	routing, err := g.BuildRouting(cutScales(cut))
	if err != nil {
		return nil, err
	}
	finalAdm, _ := runAllTiers(g, routing)

	s := &Solution{
		Admission: finalAdm,
		Routing:   routing,
		CutEdges:  make([]int, 0, len(cut)),
		EdgeScale: make([]EdgeScale, 0, len(cut)),
	}
	for ei := range cut {
		s.CutEdges = append(s.CutEdges, ei)
		s.EdgeScale = append(s.EdgeScale, EdgeScale{Edge: ei, Scale: 0})
	}
	sort.Ints(s.CutEdges)
	sort.Slice(s.EdgeScale, func(i, j int) bool { return s.EdgeScale[i].Edge < s.EdgeScale[j].Edge })

	perNode, total := g.Propagate(routing, finalAdm)
	s.LoadPerPriority = perNode
	s.Load = total
	for p := Priority(0); p < NumPriorities; p++ {
		for v, nd := range g.Nodes {
			kept := nd.Arrival[p] * finalAdm[v][p]
			s.Retained[p] += kept
			s.Rejected[p] += nd.Arrival[p] - kept
		}
	}
	for ei := range cut {
		s.DegradeCost += g.Edges[ei].DegradeCost
	}
	s.FullyAdmitted = true
	for p := Priority(0); p < NumPriorities; p++ {
		if s.Rejected[p] > Eps {
			s.FullyAdmitted = false
			break
		}
	}
	return s, nil
}

func capacityTol(cap float64) float64 { return Eps * math.Max(1.0, cap) }

func cutScales(cut map[int]bool) []EdgeScale {
	if len(cut) == 0 {
		return nil
	}
	out := make([]EdgeScale, 0, len(cut))
	for ei := range cut {
		out = append(out, EdgeScale{Edge: ei, Scale: 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Edge < out[j].Edge })
	return out
}

// runAllTiers 在固定路由上从高到低逐档水填，返回完整放行表与各档保留量。
func runAllTiers(g *Graph, routing *Routing) (Admission, [NumPriorities]float64) {
	adm := g.NewAdmission()
	locked := make([][NumPriorities]float64, len(g.Nodes))
	var retained [NumPriorities]float64
	for p := Priority(0); p < NumPriorities; p++ {
		part, kept := runTier(g, routing, p, locked)
		retained[p] = kept
		for v := range g.Nodes {
			adm[v][p] = part[v][p]
			locked[v][p] = g.Nodes[v].Arrival[p] * part[v][p]
		}
	}
	return adm, retained
}

// runTier 在固定路由上为单个优先级做 Max-Min 公平水填，并扣减更高优先级已锁定占用的容量。
func runTier(g *Graph, routing *Routing, p Priority, locked [][NumPriorities]float64) (Admission, float64) {
	n := len(g.Nodes)
	adm := g.NewAdmission()

	// 更高优先级在每个节点上的已锁定负载。
	baseLoad := make([]float64, n)
	for v := 0; v < n; v++ {
		for q := Priority(0); q < p; q++ {
			sum := 0.0
			for entry := 0; entry < n; entry++ {
				sum += locked[entry][q] * routing.Coef[q][entry][v]
			}
			baseLoad[v] += sum
		}
	}

	type flow struct {
		entry  int
		arrive float64
		rate   float64
		done   bool
	}
	flows := make([]*flow, 0, n)
	for entry := 0; entry < n; entry++ {
		a := g.Nodes[entry].Arrival[p]
		if a > 0 {
			flows = append(flows, &flow{entry: entry, arrive: a})
			adm[entry][p] = 0
		}
	}

	// 渐进式水填：每轮让所有未冻结流以相同“速率增量”上升，
	// 直到某流到达到达上限，或某瓶颈节点剩余容量耗尽；冻结相关流后继续，得到经典 Max-Min 公平解。
	for {
		active := make([]*flow, 0, len(flows))
		for _, f := range flows {
			if !f.done {
				active = append(active, f)
			}
		}
		if len(active) == 0 {
			break
		}
		current := make([]float64, n)
		for _, f := range flows {
			if f.rate == 0 {
				continue
			}
			for v := 0; v < n; v++ {
				current[v] += f.rate * routing.Coef[p][f.entry][v]
			}
		}

		delta := math.Inf(1)
		hitCap := map[int]bool{}
		hitNode := map[int]bool{}
		for _, f := range active {
			d := f.arrive - f.rate
			if d < delta-Eps {
				delta = d
				hitCap = map[int]bool{f.entry: true}
				hitNode = map[int]bool{}
			} else if approxEQ(d, delta) {
				hitCap[f.entry] = true
			}
		}
		for v := 0; v < n; v++ {
			slack := g.Nodes[v].Cap - baseLoad[v] - current[v]
			coefSum := 0.0
			uses := false
			for _, f := range active {
				c := routing.Coef[p][f.entry][v]
				if c > Eps {
					coefSum += c
					uses = true
				}
			}
			if !uses || coefSum <= Eps {
				continue
			}
			d := slack / coefSum
			if d < 0 {
				d = 0
			}
			if d < delta-Eps {
				delta = d
				hitCap = map[int]bool{}
				hitNode = map[int]bool{v: true}
			} else if approxEQ(d, delta) {
				hitNode[v] = true
			}
		}
		if math.IsInf(delta, 1) {
			delta = 0
		}
		if delta < 0 {
			delta = 0
		}

		for _, f := range active {
			f.rate = math.Min(f.arrive, f.rate+delta)
		}
		for _, f := range active {
			if hitCap[f.entry] || approxEQ(f.rate, f.arrive) {
				f.done = true
				adm[f.entry][p] = 1.0
				continue
			}
			for v := range hitNode {
				if routing.Coef[p][f.entry][v] > Eps {
					f.done = true
					break
				}
			}
			if f.done {
				adm[f.entry][p] = f.rate / f.arrive
			}
		}
		if approxZero(delta) {
			for _, f := range active {
				if !f.done {
					f.done = true
					adm[f.entry][p] = f.rate / f.arrive
				}
			}
		}
	}

	totalKept := 0.0
	for _, f := range flows {
		totalKept += f.rate
		adm[f.entry][p] = f.rate / f.arrive
	}
	return adm, totalKept
}
