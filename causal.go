// Package causal 实现跨时区分布式追踪日志的时间因果拓扑重构。
//
// 问题背景：不同服务器的物理时钟存在未知漂移（skew），直接比较
// 各服务器日志上的物理时间戳会出现“负延迟”等荒谬结果
// （例如 A 12:00:00 发出请求，B 却在 11:59:59 收到）。
//
// 算法思路：
//  1. 通过 RPC 消息 ID 关联 send/recv 事件，得到因果边 send -> recv；
//  2. 同一服务器上的事件按日志顺序构成本地因果边；
//  3. 对每条跨机因果边建立差分约束：
//     corrected(recv) >= corrected(send) + minLatency
//     即 offset[to] <= offset[from] + (recvTime - sendTime) - minLatency；
//  4. 以某台服务器为基准（offset = 0），用 Bellman-Ford 求满足全部
//     约束的最大偏移解（即差分约束系统的最短路径解）；
//  5. 若约束系统存在负环，则日志在因果上不自洽，返回错误。
//
// 校正后时间 = 物理时间 - offset[server]，校正后的因果图中不再
// 存在负延迟反例，拓扑偏序在物理上自洽。
package causal

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kind 表示事件类型。
type Kind int

const (
	Send Kind = iota
	Recv
)

func (k Kind) String() string {
	if k == Send {
		return "SEND"
	}
	return "RECV"
}

// Event 是一条追踪日志事件。
type Event struct {
	Server string    // 产生该事件的服务器
	Kind   Kind      // SEND 或 RECV
	Time   time.Time // 服务器本地物理时钟时间戳（可含时区）
	MsgID  string    // RPC 关联 ID，用于配对 send/recv
	Peer   string    // 对端服务器
	Seq    int       // 该服务器日志中的序号（0 起，由 Reconstruct 填充）
}

// Edge 是因果图中的一条 happens-before 边。
type Edge struct {
	From int    // Events 中的下标
	To   int    // Events 中的下标
	Kind string // "rpc"（跨机消息边）或 "local"（本机程序序边）
}

// Graph 是重构后的因果拓扑图，包含每台服务器的时钟校正量。
type Graph struct {
	Events  []Event
	Edges   []Edge
	Offsets map[string]time.Duration // 每台服务器的时钟基线校正量
}

// constraint 是差分约束 offset[to] <= offset[from] + weight。
type constraint struct {
	from   string
	to     string
	weight time.Duration
}

// Reconstruct 根据事件列表重构因果拓扑图并求解各服务器时钟校正量。
// minLatency 是一次 RPC 在物理上可能的最小网络延迟。
func Reconstruct(events []Event, minLatency time.Duration) (*Graph, error) {
	if len(events) == 0 {
		return nil, errors.New("causal: no events")
	}

	evs := make([]Event, len(events))
	copy(evs, events)

	// 按服务器填充日志序号（输入顺序即日志顺序）。
	seq := map[string]int{}
	for i := range evs {
		evs[i].Seq = seq[evs[i].Server]
		seq[evs[i].Server]++
	}

	g := &Graph{Events: evs, Offsets: map[string]time.Duration{}}

	// 1. 通过 MsgID 配对 send/recv，建立 RPC 因果边与差分约束。
	sends := map[string]int{}
	recvs := map[string]int{}
	for i, e := range evs {
		if e.Kind == Send {
			if _, dup := sends[e.MsgID]; dup {
				return nil, fmt.Errorf("causal: duplicate send for msg %q", e.MsgID)
			}
			sends[e.MsgID] = i
		} else {
			if _, dup := recvs[e.MsgID]; dup {
				return nil, fmt.Errorf("causal: duplicate recv for msg %q", e.MsgID)
			}
			recvs[e.MsgID] = i
		}
	}

	var cons []constraint
	for id, si := range sends {
		ri, ok := recvs[id]
		if !ok {
			return nil, fmt.Errorf("causal: send without recv for msg %q", id)
		}
		g.Edges = append(g.Edges, Edge{From: si, To: ri, Kind: "rpc"})
		s, r := evs[si], evs[ri]
		// corrected(r) >= corrected(s) + minLatency
		// => r.Time - o[r.Server] >= s.Time - o[s.Server] + minLatency
		// => o[r.Server] <= o[s.Server] + (r.Time - s.Time) - minLatency
		cons = append(cons, constraint{
			from:   s.Server,
			to:     r.Server,
			weight: r.Time.Sub(s.Time) - minLatency,
		})
	}
	for id := range recvs {
		if _, ok := sends[id]; !ok {
			return nil, fmt.Errorf("causal: recv without send for msg %q", id)
		}
	}

	// 2. 本地程序序边：同一服务器上相邻日志事件。
	byServer := map[string][]int{}
	for i, e := range evs {
		byServer[e.Server] = append(byServer[e.Server], i)
	}
	for _, idxs := range byServer {
		for k := 0; k+1 < len(idxs); k++ {
			g.Edges = append(g.Edges, Edge{From: idxs[k], To: idxs[k+1], Kind: "local"})
		}
	}

	// 3. Bellman-Ford 求解差分约束系统（offset 取满足约束的最大值）。
	// 引入虚拟基准节点，向每台服务器连权重 0 的边（即 offset <= 0），
	// 表达“无因果证据时不校正时钟”的最小干预先验：只有当日志出现
	// 负延迟等反例时，偏移量才会被因果约束强制拉为负值。
	servers := make([]string, 0, len(byServer))
	for s := range byServer {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	const root = "\x00virtual-root"
	for _, s := range servers {
		cons = append(cons, constraint{from: root, to: s, weight: 0})
	}

	const inf = time.Duration(1<<62 - 1)
	dist := map[string]time.Duration{}
	for _, s := range servers {
		dist[s] = inf
	}
	dist[root] = 0

	for iter := 0; iter < len(servers); iter++ {
		changed := false
		for _, c := range cons {
			if dist[c.from] == inf {
				continue
			}
			if nd := dist[c.from] + c.weight; nd < dist[c.to] {
				dist[c.to] = nd
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// 再松弛一轮：仍能收紧说明存在负环，日志因果不自洽。
	for _, c := range cons {
		if dist[c.from] == inf {
			continue
		}
		if dist[c.from]+c.weight < dist[c.to] {
			return nil, fmt.Errorf(
				"causal: inconsistent logs, negative cycle between %q and %q",
				c.from, c.to)
		}
	}

	for _, s := range servers {
		if dist[s] == inf {
			dist[s] = 0 // 与基准服务器无因果连通的节点不校正
		}
		g.Offsets[s] = dist[s]
	}
	return g, nil
}

// CorrectedTime 返回事件在校正后时间轴上的时间。
func (g *Graph) CorrectedTime(e Event) time.Time {
	return e.Time.Add(-g.Offsets[e.Server])
}

// NegativeDelays 返回物理时间戳上呈负延迟的 RPC 边（校正前的荒谬反例）。
func (g *Graph) NegativeDelays() []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Kind != "rpc" {
			continue
		}
		if g.Events[e.To].Time.Before(g.Events[e.From].Time) {
			out = append(out, e)
		}
	}
	return out
}

// Violations 返回校正后仍违反因果序的边（正常应为空）。
func (g *Graph) Violations(minLatency time.Duration) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Kind != "rpc" {
			continue
		}
		need := g.CorrectedTime(g.Events[e.From]).Add(minLatency)
		if g.CorrectedTime(g.Events[e.To]).Before(need) {
			out = append(out, e)
		}
	}
	return out
}

// TopoOrder 按校正后时间对事件排序，返回事件下标（稳定拓扑序）。
func (g *Graph) TopoOrder() []int {
	order := make([]int, len(g.Events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ta := g.CorrectedTime(g.Events[order[a]])
		tb := g.CorrectedTime(g.Events[order[b]])
		if !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return order[a] < order[b]
	})
	return order
}

// DOT 将校正后的因果拓扑图导出为 Graphviz DOT 格式。
func (g *Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph causal {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	for i, e := range g.Events {
		fmt.Fprintf(&b, "  n%d [label=\"%s %s %s\\nraw=%s\\ncorrected=%s\"];\n",
			i, e.Server, e.Kind, e.MsgID,
			e.Time.Format("15:04:05.000"),
			g.CorrectedTime(e).UTC().Format("15:04:05.000"))
	}
	for _, e := range g.Edges {
		style := "solid"
		if e.Kind == "local" {
			style = "dashed"
		}
		fmt.Fprintf(&b, "  n%d -> n%d [style=%s, label=\"%s\"];\n",
			e.From, e.To, style, e.Kind)
	}
	b.WriteString("}\n")
	return b.String()
}

// Report 生成人类可读的校正报告。
func (g *Graph) Report(minLatency time.Duration) string {
	var b strings.Builder
	b.WriteString("== 时钟基线校正量 (corrected = raw - offset) ==\n")
	servers := make([]string, 0, len(g.Offsets))
	for s := range g.Offsets {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	for _, s := range servers {
		fmt.Fprintf(&b, "  %-10s offset = %s\n", s, g.Offsets[s])
	}

	b.WriteString("\n== 校正前负延迟反例 ==\n")
	neg := g.NegativeDelays()
	if len(neg) == 0 {
		b.WriteString("  (无)\n")
	}
	for _, e := range neg {
		s, r := g.Events[e.From], g.Events[e.To]
		fmt.Fprintf(&b, "  msg %s: %s SEND %s -> %s RECV %s (物理倒流 %s)\n",
			s.MsgID, s.Server, s.Time.Format("15:04:05.000"),
			r.Server, r.Time.Format("15:04:05.000"),
			s.Time.Sub(r.Time))
	}

	b.WriteString("\n== 校正后调用拓扑（按校正时间排序） ==\n")
	for _, i := range g.TopoOrder() {
		e := g.Events[i]
		fmt.Fprintf(&b, "  %s  %-10s %-4s msg=%-6s peer=%s\n",
			g.CorrectedTime(e).UTC().Format("15:04:05.000"),
			e.Server, e.Kind, e.MsgID, e.Peer)
	}

	b.WriteString("\n== 校正后 RPC 延迟 ==\n")
	for _, e := range g.Edges {
		if e.Kind != "rpc" {
			continue
		}
		s, r := g.Events[e.From], g.Events[e.To]
		fmt.Fprintf(&b, "  msg %-6s %s -> %s  latency = %s\n",
			s.MsgID, s.Server, r.Server,
			g.CorrectedTime(r).Sub(g.CorrectedTime(s)))
	}

	if v := g.Violations(minLatency); len(v) > 0 {
		fmt.Fprintf(&b, "\n警告: 仍有 %d 条边违反因果序！\n", len(v))
	} else {
		b.WriteString("\n校验通过: 校正后无负延迟，因果偏序物理自洽。\n")
	}
	return b.String()
}
