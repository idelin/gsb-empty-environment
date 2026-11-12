package pubcheck

// EdgeKind 区分先行发生边的来源。
type EdgeKind int

const (
	// EdgeProgramOrder 同一 goroutine 内按登记次序形成的程序顺序边。
	EdgeProgramOrder EdgeKind = iota
	// EdgeSynchronizesWith 由显式释放/获取同步事件配对产生的同步边。
	EdgeSynchronizesWith
)

// String 返回边类型的稳定名称。
func (k EdgeKind) String() string {
	switch k {
	case EdgeProgramOrder:
		return "program-order"
	case EdgeSynchronizesWith:
		return "synchronizes-with"
	default:
		return "unknown-edge"
	}
}

// Edge 是先行发生图中的一条有向边：From 先行发生于 To。
type Edge struct {
	From int
	To   int
	Kind EdgeKind
	Sync SyncID
}

// Graph 是由轨迹构造出的先行发生图。
type Graph struct {
	Events []Event
	Edges  []Edge
	adj    [][]Edge
}

// BuildGraph 依据检查器中的事件构造先行发生图。
//
// 边只有两个合法来源：
//  1. 同一 goroutine 内相邻事件的程序顺序边；
//  2. 释放侧同步事件到获取侧同步事件的同步边，配对模式由 NewSync 决定。
//
// Wall 时间不参与任何边的构造。
func (c *Checker) BuildGraph() *Graph {
	events := c.Events()
	g := &Graph{Events: events, adj: make([][]Edge, len(events))}

	last := make(map[GoroutineID]int)
	for seq, ev := range events {
		if prev, ok := last[ev.Goroutine]; ok {
			g.add(Edge{From: prev, To: seq, Kind: EdgeProgramOrder})
		}
		last[ev.Goroutine] = seq
	}

	queues := make(map[SyncID][]int)
	barrierBySync := make(map[SyncID][]int)

	for seq, ev := range events {
		mode := c.syncs[ev.Sync]
		switch ev.Kind {
		case KindReleaseSync:
			if mode == SyncBarrier {
				barrierBySync[ev.Sync] = append(barrierBySync[ev.Sync], seq)
			} else {
				queues[ev.Sync] = append(queues[ev.Sync], seq)
			}
		case KindAcquireSync:
			if mode == SyncBarrier {
				for _, rel := range barrierBySync[ev.Sync] {
					g.add(Edge{From: rel, To: seq, Kind: EdgeSynchronizesWith, Sync: ev.Sync})
				}
			} else {
				q := queues[ev.Sync]
				if len(q) > 0 {
					rel := q[0]
					queues[ev.Sync] = q[1:]
					g.add(Edge{From: rel, To: seq, Kind: EdgeSynchronizesWith, Sync: ev.Sync})
				}
			}
		}
	}

	return g
}

func (g *Graph) add(e Edge) {
	g.Edges = append(g.Edges, e)
	g.adj[e.From] = append(g.adj[e.From], e)
}

// HappensBefore 报告事件 from 是否通过程序顺序边与同步边先行发生于事件 to。
// 同一事件返回 false。判定完全基于图可达性，与 Wall 时间无关。
func (g *Graph) HappensBefore(from, to int) bool {
	if from == to || from < 0 || to < 0 || from >= len(g.Events) || to >= len(g.Events) {
		return false
	}
	seen := make([]bool, len(g.Events))
	stack := []int{from}
	seen[from] = true
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range g.adj[cur] {
			if e.To == to {
				return true
			}
			if !seen[e.To] {
				seen[e.To] = true
				stack = append(stack, e.To)
			}
		}
	}
	return false
}

// Path 返回先行发生路径上的事件序号；不存在路径时返回 nil。
func (g *Graph) Path(from, to int) []int {
	if from == to || from < 0 || to < 0 || from >= len(g.Events) || to >= len(g.Events) {
		return nil
	}
	parent := make([]int, len(g.Events))
	for i := range parent {
		parent[i] = -1
	}
	parent[from] = from
	queue := []int{from}
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.adj[cur] {
			if parent[e.To] != -1 {
				continue
			}
			parent[e.To] = cur
			if e.To == to {
				found = true
				break
			}
			queue = append(queue, e.To)
		}
	}
	if !found {
		return nil
	}
	var reversed []int
	for cur := to; cur != from; cur = parent[cur] {
		reversed = append(reversed, cur)
	}
	reversed = append(reversed, from)
	path := make([]int, len(reversed))
	for i, seq := range reversed {
		path[len(path)-1-i] = seq
	}
	return path
}
