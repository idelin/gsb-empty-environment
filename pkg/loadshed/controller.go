package loadshed

import (
	"math"
	"sort"
)

// HysteresisConfig 配置滞回防抖与平滑回升策略。
type HysteresisConfig struct {
	// CooldownTicks 是 SOFT 边被切断后、以及某档发生脱落后，允许恢复前必须等待的最少 tick 数。
	CooldownTicks int
	// SustainTicks 是瓶颈余量连续高于恢复水位所需的连续 tick 数。
	SustainTicks int
	// RecoverHeadroomRatio 是恢复水位：节点空闲余量需 >= RecoverHeadroomRatio*Cap 才认为恢复安全。
	RecoverHeadroomRatio float64
	// RatioStep 是每个恢复 tick 放行比例的最大回升步长（(0,1]）。
	RatioStep float64
	// EdgeStep 是每个恢复 tick 单条 SOFT 边生效比例的最大回升步长（(0,1]）。
	EdgeStep float64
}

// DefaultHysteresisConfig 返回一组保守可用的默认滞回参数。
func DefaultHysteresisConfig() HysteresisConfig {
	return HysteresisConfig{
		CooldownTicks:        3,
		SustainTicks:         2,
		RecoverHeadroomRatio: 0.15,
		RatioStep:            0.2,
		EdgeStep:             0.5,
	}
}

func (c *HysteresisConfig) normalize() {
	if c.CooldownTicks < 0 {
		c.CooldownTicks = 0
	}
	if c.SustainTicks < 1 {
		c.SustainTicks = 1
	}
	if c.RecoverHeadroomRatio <= 0 || math.IsNaN(c.RecoverHeadroomRatio) {
		c.RecoverHeadroomRatio = 0.15
	}
	if c.RatioStep <= 0 || c.RatioStep > 1 || math.IsNaN(c.RatioStep) {
		c.RatioStep = 0.2
	}
	if c.EdgeStep <= 0 || c.EdgeStep > 1 || math.IsNaN(c.EdgeStep) {
		c.EdgeStep = 0.5
	}
}

// Controller 在静态求解器之上维护跨 tick 的滞回状态，输出平滑、防震荡的控制动作。
type Controller struct {
	g    *Graph
	cfg  HysteresisConfig
	tick int
	adm  Admission
	// edgeScale[ei] 是每条边当前生效比例（初始全 1）。
	edgeScale []float64
	// edgeCooldown[ei] 是该边最近一次被切断/下调的 tick，用于冷却判定。
	edgeCooldown []int
	// ratioCooldown[p] 是该优先级最近一次发生脱落下调的 tick。
	ratioCooldown [NumPriorities]int
	// sustain 记录节点余量连续达标的计数与起始 tick。
	sustainCount int
	last         *Snapshot
}

// Snapshot 是单个 tick 的可观测控制面状态。
type Snapshot struct {
	// Tick 是单调递增的时间步。
	Tick int
	// Admission 是当前实际生效的放行比例。
	Admission Admission
	// EdgeScale 是当前所有非全量生效边的比例（被切或恢复中）。
	EdgeScale []EdgeScale
	// Load 是按当前决策精确传播得到的各节点总负载。
	Load []float64
	// LoadPerPriority 是各节点分优先级负载。
	LoadPerPriority [][NumPriorities]float64
	// CutEdges 是当前完全切断（比例≈0）的 SOFT 边编号。
	CutEdges []int
	// RecoveringEdges 是处于部分恢复（0<比例<1）的边编号。
	RecoveringEdges []int
	// Rejected[p] 是该 tick 各优先级被脱落的入口流量。
	Rejected [NumPriorities]float64
	// Retained[p] 是该 tick 各优先级被保留的入口流量。
	Retained [NumPriorities]float64
	// HeadroomOK 表示全网瓶颈余量当前是否高于恢复水位。
	HeadroomOK bool
	// SustainCount 是余量连续达标的当前计数。
	SustainCount int
	// Cooling 表示仍有边或脱落档处于冷却期，本 tick 不允许恢复。
	Cooling bool
}

// NewController 基于已通过环路校验的 DAG 创建控制器，初始为全放行、边全量生效。
func NewController(g *Graph, cfg HysteresisConfig) *Controller {
	cfg.normalize()
	c := &Controller{
		g:            g,
		cfg:          cfg,
		adm:          g.NewAdmission(),
		edgeScale:    make([]float64, len(g.Edges)),
		edgeCooldown: make([]int, len(g.Edges)),
	}
	for i := range c.edgeScale {
		c.edgeScale[i] = 1.0
	}
	for p := Priority(0); p < NumPriorities; p++ {
		c.ratioCooldown[p] = -cfg.CooldownTicks - 1
	}
	for i := range c.edgeCooldown {
		c.edgeCooldown[i] = -cfg.CooldownTicks - 1
	}
	return c
}

// Tick 推进一个时间步。图的拓扑保持不变，调用方可在 tick 间修改节点容量与到达速率（容量骤降/恢复场景）。
func (c *Controller) Tick() (*Snapshot, error) {
	c.tick++
	g := c.g

	// 1) 无滞回的理想目标解：当前容量/流量下应当切断哪些边、各档放行多少。
	target, err := Solve(g)
	if err != nil {
		return nil, err
	}

	// 2) 保护性即时动作（永远不等待冷却，避免过载期间犹豫）：
	//    目标要求切断的边立即切到 0；目标放行比例低于当前的档位立即下调。
	targetCut := map[int]bool{}
	for _, ei := range target.CutEdges {
		targetCut[ei] = true
	}
	for ei := range c.edgeScale {
		if targetCut[ei] && c.edgeScale[ei] > Eps {
			c.edgeScale[ei] = 0
			c.edgeCooldown[ei] = c.tick
		}
	}
	for v := range g.Nodes {
		for p := Priority(0); p < NumPriorities; p++ {
			want := target.Admission[v][p]
			if want < c.adm[v][p]-Eps {
				c.adm[v][p] = want
				c.ratioCooldown[p] = c.tick
			}
		}
	}

	// 3) 恢复动作受滞回门控：余量持续达标 + 过冷却期，才按步长逐步回升。
	headroomOK := c.allHeadroomHealthy()
	if headroomOK {
		c.sustainCount++
	} else {
		c.sustainCount = 0
	}
	cooling := c.inCooldown()
	if c.sustainCount >= c.cfg.SustainTicks && !cooling {
		c.recoverOneStep(target)
	}

	return c.snapshot(headroomOK, cooling), nil
}

// allHeadroomHealthy 判断每个节点在当前决策下的空闲余量是否达到恢复水位。
func (c *Controller) allHeadroomHealthy() bool {
	routing, err := c.g.BuildRouting(c.activeScales())
	if err != nil {
		return false
	}
	_, total := c.g.Propagate(routing, c.adm)
	for v, nd := range c.g.Nodes {
		headroom := nd.Cap - total[v]
		waterline := c.cfg.RecoverHeadroomRatio * nd.Cap
		if headroom < waterline-capacityTol(nd.Cap) {
			return false
		}
	}
	return true
}

func (c *Controller) inCooldown() bool {
	for ei := range c.edgeScale {
		if c.edgeScale[ei] < 1-Eps && c.tick-c.edgeCooldown[ei] <= c.cfg.CooldownTicks {
			return true
		}
	}
	for p := Priority(0); p < NumPriorities; p++ {
		if c.anyShed(p) && c.tick-c.ratioCooldown[p] <= c.cfg.CooldownTicks {
			return true
		}
	}
	return false
}

func (c *Controller) anyShed(p Priority) bool {
	for v, nd := range c.g.Nodes {
		if nd.Arrival[p] > 0 && c.adm[v][p] < 1-Eps {
			return true
		}
	}
	return false
}

// recoverOneStep 在一个 tick 内至多推进一类恢复，保证可观测、可断言且不会瞬间回涌：
// 优先恢复一条 SOFT 边（按代价从高到低，先恢复最宝贵的降级能力），
// 无边可恢复时再按档位从低到高、以固定步长抬升放行比例，且每次抬升都做容量与水位试算。
func (c *Controller) recoverOneStep(target *Solution) {
	g := c.g
	targetFull := map[int]bool{}
	for _, ei := range target.CutEdges {
		targetFull[ei] = true
	}

	// 3a) 边恢复：仅恢复目标已不再要求切断的边。
	candidates := make([]int, 0)
	for ei, scale := range c.edgeScale {
		if scale < 1-Eps && !targetFull[ei] {
			candidates = append(candidates, ei)
		}
	}
	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			ci, cj := g.Edges[candidates[i]].DegradeCost, g.Edges[candidates[j]].DegradeCost
			if !approxEQ(ci, cj) {
				return ci > cj
			}
			return candidates[i] < candidates[j]
		})
		ei := candidates[0]
		next := math.Min(1.0, c.edgeScale[ei]+c.cfg.EdgeStep)
		old := c.edgeScale[ei]
		c.edgeScale[ei] = next
		if c.feasibleAndHealthy() {
			c.edgeCooldown[ei] = c.tick
			return
		}
		// 试算不安全则回滚，本 tick 不再尝试比例恢复，等待下一周期重新累计。
		c.edgeScale[ei] = old
		c.sustainCount = 0
		return
	}

	// 3b) 放行比例恢复：从最低优先级开始（高优最后动，进一步保护关键流量）。
	for p := NumPriorities - 1; p >= 0; p-- {
		if !c.anyShed(p) {
			continue
		}
		changed := false
		for v := range g.Nodes {
			if g.Nodes[v].Arrival[p] == 0 || c.adm[v][p] >= 1-Eps {
				continue
			}
			c.adm[v][p] = math.Min(1.0, c.adm[v][p]+c.cfg.RatioStep)
			changed = true
		}
		if changed {
			if c.feasibleAndHealthy() {
				c.ratioCooldown[p] = c.tick
				return
			}
			// 回滚本档本 tick 的全部抬升。
			for v := range g.Nodes {
				if g.Nodes[v].Arrival[p] > 0 {
					c.adm[v][p] = math.Max(0, c.adm[v][p]-c.cfg.RatioStep)
				}
			}
			c.sustainCount = 0
			return
		}
	}
}

// feasibleAndHealthy 试算当前决策：既不超容量，又仍满足恢复水位（双重门控）。
func (c *Controller) feasibleAndHealthy() bool {
	routing, err := c.g.BuildRouting(c.activeScales())
	if err != nil {
		return false
	}
	_, total := c.g.Propagate(routing, c.adm)
	for v, nd := range c.g.Nodes {
		if total[v] > nd.Cap+capacityTol(nd.Cap) {
			return false
		}
		if nd.Cap-total[v] < c.cfg.RecoverHeadroomRatio*nd.Cap-capacityTol(nd.Cap) {
			return false
		}
	}
	return true
}

func (c *Controller) activeScales() []EdgeScale {
	out := make([]EdgeScale, 0)
	for ei, scale := range c.edgeScale {
		if scale < 1-Eps {
			out = append(out, EdgeScale{Edge: ei, Scale: scale})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Edge < out[j].Edge })
	return out
}

func (c *Controller) snapshot(headroomOK, cooling bool) *Snapshot {
	routing, _ := c.g.BuildRouting(c.activeScales())
	perNode, total := c.g.Propagate(routing, c.adm)
	snap := &Snapshot{
		Tick:            c.tick,
		Admission:       c.cloneAdm(),
		EdgeScale:       c.activeScales(),
		Load:            total,
		LoadPerPriority: perNode,
		HeadroomOK:      headroomOK,
		SustainCount:    c.sustainCount,
		Cooling:         cooling,
	}
	for ei, scale := range c.edgeScale {
		switch {
		case scale <= Eps:
			snap.CutEdges = append(snap.CutEdges, ei)
		case scale < 1-Eps:
			snap.RecoveringEdges = append(snap.RecoveringEdges, ei)
		}
	}
	for p := Priority(0); p < NumPriorities; p++ {
		for v, nd := range c.g.Nodes {
			kept := nd.Arrival[p] * c.adm[v][p]
			snap.Retained[p] += kept
			snap.Rejected[p] += nd.Arrival[p] - kept
		}
	}
	c.last = snap
	return snap
}

func (c *Controller) cloneAdm() Admission {
	out := make(Admission, len(c.adm))
	for i := range c.adm {
		out[i] = c.adm[i]
	}
	return out
}

// Last 返回最近一次 Tick 的快照（未 Tick 时为 nil）。
func (c *Controller) Last() *Snapshot { return c.last }
