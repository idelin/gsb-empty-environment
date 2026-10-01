// Package causal 实现离散分布式追踪日志的时间因果拓扑重构。
//
// 不同服务器的物理时钟存在未知漂移（未知 offset）。分析器利用 RPC 的
// 请求/响应 发送-接收 四元组（SendRequest, RecvRequest, SendResponse,
// RecvResponse）建立 Lamport happens-before 偏序图，并把
// “消息必须先发送后到达” 转化为一组关于各服务器时钟基线 offset 的
// 差分约束（difference constraints）：
//
//	corrected(e) = Raw(e) + offset[server(e)]
//	对每条跨机因果边 u -> v： corrected(v) >= corrected(u) + minLatency
//	即： offset[v.server] - offset[u.server] >= Raw(u) + minLatency - Raw(v)
//
// 用 Bellman-Ford 最长路求解该约束系统，得到每台服务器的最小时钟校正量，
// 从而消除“负延迟”这类物理上荒谬的反例，输出物理自洽的调用拓扑图。
package causal

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Kind 是追踪事件的类型（RPC 四元组）。
type Kind int

const (
	SendRequest  Kind = iota // 客户端发送请求
	RecvRequest              // 服务端接收请求
	SendResponse             // 服务端发送响应
	RecvResponse             // 客户端接收响应
)

func (k Kind) String() string {
	switch k {
	case SendRequest:
		return "SEND_REQ"
	case RecvRequest:
		return "RECV_REQ"
	case SendResponse:
		return "SEND_RESP"
	case RecvResponse:
		return "RECV_RESP"
	}
	return "UNKNOWN"
}

// Event 是一条离散的追踪日志记录。Raw 是服务器本地物理时钟读数（可能漂移）。
type Event struct {
	ID     string
	Server string
	RPC    string // 关联同一次 RPC 四元组的标识
	Kind   Kind
	Raw    time.Time
}

// Edge 是一条 happens-before 因果边（事件下标 From -> To）。
type Edge struct {
	From, To  int
	CrossHost bool // 是否跨服务器（网络边）
}

// Graph 是因果偏序图。
type Graph struct {
	Events []Event
	Edges  []Edge

	servers   []string
	serverIdx map[string]int
	byRPC     map[string][]int
}

// NewGraph 创建空的因果图。
func NewGraph() *Graph {
	return &Graph{
		serverIdx: make(map[string]int),
		byRPC:     make(map[string][]int),
	}
}

// Add 追加一条事件。
func (g *Graph) Add(e Event) {
	if _, ok := g.serverIdx[e.Server]; !ok {
		g.serverIdx[e.Server] = len(g.servers)
		g.servers = append(g.servers, e.Server)
	}
	g.byRPC[e.RPC] = append(g.byRPC[e.RPC], len(g.Events))
	g.Events = append(g.Events, e)
}

// Build 根据四元组配对与单机程序序建立因果边。
//
// 规则：
//  1. 同一 RPC：SendRequest -> RecvRequest，SendResponse -> RecvResponse（网络边）。
//  2. 同一服务器上的事件按本地时钟排序，相邻事件连边（程序序）。
func (g *Graph) Build() error {
	g.Edges = g.Edges[:0]

	// 规则 1：RPC 四元组网络边。
	for rpc, idxs := range g.byRPC {
		var sendReq, recvReq, sendResp, recvResp = -1, -1, -1, -1
		for _, i := range idxs {
			switch g.Events[i].Kind {
			case SendRequest:
				sendReq = i
			case RecvRequest:
				recvReq = i
			case SendResponse:
				sendResp = i
			case RecvResponse:
				recvResp = i
			}
		}
		if sendReq >= 0 && recvReq >= 0 {
			g.addEdge(sendReq, recvReq)
		}
		if sendResp >= 0 && recvResp >= 0 {
			g.addEdge(sendResp, recvResp)
		}
		if sendReq < 0 || recvReq < 0 {
			return fmt.Errorf("rpc %q: 缺少请求发送/接收事件，无法配对", rpc)
		}
	}

	// 规则 2：单机程序序（本地时钟单调假设）。
	byServer := make(map[string][]int)
	for i, e := range g.Events {
		byServer[e.Server] = append(byServer[e.Server], i)
	}
	for _, idxs := range byServer {
		sort.Slice(idxs, func(a, b int) bool {
			return g.Events[idxs[a]].Raw.Before(g.Events[idxs[b]].Raw)
		})
		for k := 0; k+1 < len(idxs); k++ {
			g.addEdge(idxs[k], idxs[k+1])
		}
	}
	return nil
}

func (g *Graph) addEdge(from, to int) {
	g.Edges = append(g.Edges, Edge{
		From:      from,
		To:        to,
		CrossHost: g.Events[from].Server != g.Events[to].Server,
	})
}

// 差分约束：offset[to] - offset[from] >= weight。
type constraint struct {
	from, to int // 服务器下标
	weight   time.Duration
}

// Result 是重构结果。
type Result struct {
	Offsets   map[string]time.Duration // 每台服务器的时钟基线校正量
	Corrected []time.Time              // 与 Events 对齐的校正后物理时间
	Order     []int                    // 按校正后时间排序的事件下标（拓扑输出）
}

// ErrInfeasible 表示约束系统存在正环，日志自相矛盾，无法物理自洽。
var ErrInfeasible = errors.New("因果约束不可满足：日志存在正权重环（自相矛盾）")

// Reconstruct 求解时钟校正量并输出物理自洽的拓扑。
//
// minLatency 是跨机消息的最小网络延迟下界（如 1ms）。
func (g *Graph) Reconstruct(minLatency time.Duration) (*Result, error) {
	n := len(g.servers)
	var cons []constraint
	for _, e := range g.Edges {
		if !e.CrossHost {
			continue // 同机边 offset 相同，约束恒等
		}
		u, v := g.Events[e.From], g.Events[e.To]
		w := u.Raw.Sub(v.Raw) + minLatency
		cons = append(cons, constraint{
			from:   g.serverIdx[u.Server],
			to:     g.serverIdx[v.Server],
			weight: w,
		})
	}

	// Bellman-Ford 最长路：offset[to] = max(offset[to], offset[from] + w)。
	// 初值全 0 等价于超级源点，得到的是满足全部下界约束的最小校正解。
	offset := make([]time.Duration, n)
	for iter := 0; iter < n; iter++ {
		changed := false
		for _, c := range cons {
			if offset[c.to] < offset[c.from]+c.weight {
				offset[c.to] = offset[c.from] + c.weight
				changed = true
			}
		}
		if !changed {
			break
		}
		if iter == n-1 {
			return nil, ErrInfeasible
		}
	}

	res := &Result{
		Offsets:   make(map[string]time.Duration, n),
		Corrected: make([]time.Time, len(g.Events)),
	}
	for i, s := range g.servers {
		res.Offsets[s] = offset[i]
	}
	for i, e := range g.Events {
		res.Corrected[i] = e.Raw.Add(offset[g.serverIdx[e.Server]])
	}
	res.Order = make([]int, len(g.Events))
	for i := range res.Order {
		res.Order[i] = i
	}
	sort.Slice(res.Order, func(a, b int) bool {
		ia, ib := res.Order[a], res.Order[b]
		if res.Corrected[ia].Equal(res.Corrected[ib]) {
			return ia < ib
		}
		return res.Corrected[ia].Before(res.Corrected[ib])
	})
	return res, nil
}

// Violations 返回校正后仍违反因果的边（正常应为空），用于自检。
func (g *Graph) Violations(res *Result, minLatency time.Duration) []Edge {
	var bad []Edge
	for _, e := range g.Edges {
		need := minLatency
		if !e.CrossHost {
			need = 0
		}
		if res.Corrected[e.To].Sub(res.Corrected[e.From]) < need {
			bad = append(bad, e)
		}
	}
	return bad
}

// RPCLatency 计算某次 RPC 校正后的请求/响应网络延迟与总时延。
func (g *Graph) RPCLatency(res *Result, rpc string) (req, resp, total time.Duration, err error) {
	idxs, ok := g.byRPC[rpc]
	if !ok {
		return 0, 0, 0, fmt.Errorf("rpc %q 不存在", rpc)
	}
	var t [4]time.Time
	var have [4]bool
	for _, i := range idxs {
		k := g.Events[i].Kind
		t[k] = res.Corrected[i]
		have[k] = true
	}
	if have[SendRequest] && have[RecvRequest] {
		req = t[RecvRequest].Sub(t[SendRequest])
	}
	if have[SendResponse] && have[RecvResponse] {
		resp = t[RecvResponse].Sub(t[SendResponse])
	}
	if have[SendRequest] && have[RecvResponse] {
		total = t[RecvResponse].Sub(t[SendRequest])
	}
	return req, resp, total, nil
}
