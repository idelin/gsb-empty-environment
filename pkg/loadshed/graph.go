package loadshed

import (
	"fmt"
	"math"
)

// Graph 是微服务有向依赖图（要求为 DAG，含环时求解被阻断）。
type Graph struct {
	// Nodes 按添加顺序保存节点，下标即节点稳定编号。
	Nodes []*Node
	// Edges 按添加顺序保存边，下标即边稳定编号（支持平行边）。
	Edges []*Edge

	nodeIdx map[string]int
	// outAdj[u] 是节点 u 的出边编号列表（按添加顺序）。
	outAdj [][]int
	// inAdj[v] 是汇入节点 v 的边编号列表（按添加顺序）。
	inAdj [][]int

	topo []int
}

// NewGraph 校验并构建依赖图。重复 ID、未知端点、非法 fanout/容量/速率都会返回错误；含环返回 *CycleError。
func NewGraph(nodes []Node, edges []Edge) (*Graph, error) {
	g := &Graph{nodeIdx: make(map[string]int, len(nodes))}
	for i := range nodes {
		n := nodes[i]
		if n.ID == "" {
			return nil, fmt.Errorf("loadshed: node #%d has empty ID", i)
		}
		if _, dup := g.nodeIdx[n.ID]; dup {
			return nil, fmt.Errorf("loadshed: duplicate node ID %q", n.ID)
		}
		if math.IsNaN(n.Cap) || n.Cap < 0 {
			return nil, fmt.Errorf("loadshed: node %q cap must be >= 0", n.ID)
		}
		for p, a := range n.Arrival {
			if math.IsNaN(a) || a < 0 {
				return nil, fmt.Errorf("loadshed: node %q arrival[%s] must be >= 0", n.ID, Priority(p))
			}
		}
		g.nodeIdx[n.ID] = i
		g.Nodes = append(g.Nodes, &n)
	}
	n := len(nodes)
	g.outAdj = make([][]int, n)
	g.inAdj = make([][]int, n)
	for i := range edges {
		e := edges[i]
		u, okU := g.nodeIdx[e.From]
		v, okV := g.nodeIdx[e.To]
		if !okU {
			return nil, fmt.Errorf("loadshed: edge #%d from unknown node %q", i, e.From)
		}
		if !okV {
			return nil, fmt.Errorf("loadshed: edge #%d to unknown node %q", i, e.To)
		}
		if math.IsNaN(e.Fanout) || e.Fanout <= 0 {
			return nil, fmt.Errorf("loadshed: edge %q->%q fanout must be > 0", e.From, e.To)
		}
		if e.Dep == Soft && (math.IsNaN(e.DegradeCost) || e.DegradeCost < 0) {
			return nil, fmt.Errorf("loadshed: soft edge %q->%q degrade cost must be >= 0", e.From, e.To)
		}
		ec := e
		g.Edges = append(g.Edges, &ec)
		idx := len(g.Edges) - 1
		g.outAdj[u] = append(g.outAdj[u], idx)
		g.inAdj[v] = append(g.inAdj[v], idx)
	}

	// Kahn 拓扑排序；若未消费全部节点则存在有向环，转而求一条最短环。
	indeg := make([]int, n)
	for i := range g.Edges {
		indeg[g.to(i)]++
	}
	queue := make([]int, 0, n)
	for v := 0; v < n; v++ {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	head := 0
	for head < len(queue) {
		u := queue[head]
		head++
		g.topo = append(g.topo, u)
		for _, ei := range g.outAdj[u] {
			v := g.to(ei)
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if len(g.topo) != n {
		return nil, g.shortestCycle()
	}
	return g, nil
}

func (g *Graph) to(edgeIdx int) int { return g.nodeIdx[g.Edges[edgeIdx].To] }

// NodeIndex 返回节点 ID 对应的稳定编号，不存在时返回 false。
func (g *Graph) NodeIndex(id string) (int, bool) {
	v, ok := g.nodeIdx[id]
	return v, ok
}

// TopoOrder 返回拓扑序节点编号的副本。
func (g *Graph) TopoOrder() []int {
	out := make([]int, len(g.topo))
	copy(out, g.topo)
	return out
}

// shortestCycle 求节点数最少的有向环，对每个起点做带父指针 BFS，取回到起点的最短回边。
// 同长时取节点 ID 字典序最小者，保证结果确定可复现。
func (g *Graph) shortestCycle() error {
	n := len(g.Nodes)
	var best []int
	for start := 0; start < n; start++ {
		dist := make([]int, n)
		parent := make([]int, n)
		for i := range dist {
			dist[i] = -1
			parent[i] = -1
		}
		dist[start] = 0
		queue := []int{start}
		bestHit := -1 // 回到 start 的最优回边的上游节点
		bestLen := math.MaxInt
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			if dist[u]+1 > bestLen {
				break
			}
			for _, ei := range g.outAdj[u] {
				v := g.to(ei)
				if u == start && v == start {
					// 自环是长度 1 的极小环。
					bestHit = start
					bestLen = 1
					break
				}
				if v == start {
					if dist[u]+1 < bestLen {
						bestLen = dist[u] + 1
						bestHit = u
					}
					continue
				}
				if dist[v] >= 0 {
					continue
				}
				dist[v] = dist[u] + 1
				parent[v] = u
				queue = append(queue, v)
			}
			if bestLen == 1 {
				break
			}
		}
		if bestHit < 0 {
			continue
		}
		var cycle []int
		if bestLen == 1 {
			cycle = []int{start, start}
		} else {
			rev := make([]int, 0, bestLen)
			for x := bestHit; x != -1; x = parent[x] {
				rev = append(rev, x)
				if x == start {
					break
				}
			}
			cycle = make([]int, 0, len(rev)+1)
			for i := len(rev) - 1; i >= 0; i-- {
				cycle = append(cycle, rev[i])
			}
			cycle = append(cycle, start)
		}
		if best == nil || len(cycle) < len(best) || (len(cycle) == len(best) && lexNodesLess(g, cycle, best)) {
			best = cycle
		}
	}
	ids := make([]string, 0, len(best)+1)
	for _, v := range best {
		ids = append(ids, g.Nodes[v].ID)
	}
	return &CycleError{Nodes: ids}
}

func lexNodesLess(g *Graph, a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		ia, ib := g.Nodes[a[i]].ID, g.Nodes[b[i]].ID
		if ia != ib {
			return ia < ib
		}
	}
	return len(a) < len(b)
}

// EdgeScale 给出一条边的生效比例：0 表示完全切断（仅 SOFT 边允许），1 表示完全生效，
// (0,1) 之间用于滞回控制器的平滑恢复。
type EdgeScale struct {
	Edge  int
	Scale float64
}

// Routing 是按拓扑序计算出的路由系数表。
// Coef[p][entry][v] 表示入口 entry 的 1 单位优先级 p 放行流量，经全部共享路径与扇出放大后
// 在节点 v 上贡献的负载量；多路径汇入在此精确累加，绝不按树重复计算。
type Routing struct {
	Coef     [NumPriorities][][]float64
	EdgeLoad [NumPriorities][][]float64
}

// BuildRouting 在给定边生效比例下计算路由系数。scales 为 nil 表示所有边完全生效。
func (g *Graph) BuildRouting(scales []EdgeScale) (*Routing, error) {
	n := len(g.Nodes)
	edgeScale := make([]float64, len(g.Edges))
	for i := range edgeScale {
		edgeScale[i] = 1.0
	}
	for _, s := range scales {
		if s.Edge < 0 || s.Edge >= len(g.Edges) {
			return nil, fmt.Errorf("loadshed: edge scale index %d out of range", s.Edge)
		}
		if math.IsNaN(s.Scale) || s.Scale < 0 || s.Scale > 1 {
			return nil, fmt.Errorf("loadshed: edge %d scale must be in [0,1]", s.Edge)
		}
		if s.Scale == 0 && g.Edges[s.Edge].Dep == Hard {
			return nil, fmt.Errorf("loadshed: hard edge %q->%q cannot be cut", g.Edges[s.Edge].From, g.Edges[s.Edge].To)
		}
		edgeScale[s.Edge] = s.Scale
	}
	r := &Routing{}
	for p := Priority(0); p < NumPriorities; p++ {
		coef := make([][]float64, n)
		edgeLoad := make([][]float64, n)
		for entry := 0; entry < n; entry++ {
			coef[entry] = make([]float64, n)
			edgeLoad[entry] = make([]float64, len(g.Edges))
		}
		// 拓扑序 DP：入口在自身贡献 1 单位本地负载，再沿出边按 fanout*scale 传播；
		// 多入口、多路径在同节点的贡献在 Propagate 中线性叠加，保证精确守恒。
		for _, u := range g.topo {
			for entry := 0; entry < n; entry++ {
				incoming := 0.0
				for _, ei := range g.inAdj[u] {
					incoming += edgeLoad[entry][ei]
				}
				if entry == u {
					incoming += 1.0
				}
				coef[entry][u] = incoming
				for _, ei := range g.outAdj[u] {
					edgeLoad[entry][ei] = incoming * g.Edges[ei].Fanout * edgeScale[ei]
				}
			}
		}
		r.Coef[p] = coef
		r.EdgeLoad[p] = edgeLoad
	}
	return r, nil
}

// Admission 给出每个入口节点、每个优先级的放行比例（[0,1]）。
type Admission [][NumPriorities]float64

// NewAdmission 创建默认全放行（比例 1）的放行表。
func (g *Graph) NewAdmission() Admission {
	a := make(Admission, len(g.Nodes))
	for i := range a {
		for p := Priority(0); p < NumPriorities; p++ {
			a[i][p] = 1.0
		}
	}
	return a
}

// Propagate 在给定路由与放行比例下，精确计算每个节点各优先级负载与总负载。
func (g *Graph) Propagate(r *Routing, adm Admission) (perNode [][NumPriorities]float64, total []float64) {
	n := len(g.Nodes)
	perNode = make([][NumPriorities]float64, n)
	total = make([]float64, n)
	for v := 0; v < n; v++ {
		for p := Priority(0); p < NumPriorities; p++ {
			sum := 0.0
			for entry := 0; entry < n; entry++ {
				arrival := g.Nodes[entry].Arrival[p]
				if arrival == 0 {
					continue
				}
				sum += arrival * adm[entry][p] * r.Coef[p][entry][v]
			}
			perNode[v][p] = sum
			total[v] += sum
		}
	}
	return perNode, total
}

// TotalArrival 返回某优先级全网入口到达速率之和。
func (g *Graph) TotalArrival(p Priority) float64 {
	sum := 0.0
	for _, nd := range g.Nodes {
		sum += nd.Arrival[p]
	}
	return sum
}
