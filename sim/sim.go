// Package sim 是与 retry 运行时、steady 稳态求解器语义一致的虚拟时间
// 离散事件仿真器，用于在临界过载点交叉验证不动点求解结果。
package sim

import (
	"math/rand"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
	"prescreen.local/ps-9f073c0c88/steady"
)

// NodeCfg 描述仿真中的一个节点。
type NodeCfg struct {
	Name string
	// Capacity 硬容量（请求/秒），用于故障率曲线与雪崩判定。
	Capacity float64
	// Curve 负载-故障率曲线，与求解器共用。
	Curve steady.FailCurve
	// ServiceTime 每次尝试的确定性服务耗时（秒）。
	ServiceTime float64
	// Breaker 连续故障熔断器配置（零值表示禁用）。
	Breaker retry.BreakerConfig
	// IngressRetry 外部 offered 流量进入本节点时的重试配置；nil 表示不重试。
	IngressRetry *steady.EdgeRetry
}

// EdgeCfg 描述一条边及其重试配置。
type EdgeCfg struct {
	From   string
	To     string
	Fanout float64
	Retry  steady.EdgeRetry
	// BaseBackoff/Jitter 与运行时退避语义一致。
	BaseBackoff       time.Duration
	BackoffMultiplier float64
	Jitter            float64
	// HedgeDelay 对冲延迟（零值表示不启用对冲）。
	HedgeDelay time.Duration
	// MinProcessingTime 截止期校验使用的下游最小处理耗时。
	MinProcessingTime time.Duration
	// Deadline 每个逻辑请求在该边上的绝对预算时长；零值表示不设截止期。
	Deadline time.Duration
}

// Config 是一次仿真的完整配置。
type Config struct {
	Nodes []NodeCfg
	Edges []EdgeCfg
	// Offered 入口节点的外部到达率（请求/秒，泊松过程）。
	Offered map[string]float64
	// Window 预算滑动窗口时长。
	Window time.Duration
	// Duration 仿真总时长（秒）。
	Duration float64
	// Warmup 预热时长（秒），该时刻之后才计入测量。
	Warmup float64
	// Seed 随机种子，保证可复现。
	Seed int64
}

// NodeMetrics 是单节点测量指标。
type NodeMetrics struct {
	LogicalArrivals int64
	Attempts        int64 // 预热后发出的物理尝试数
	Successes       int64
	Failures        int64
	RetriesRejected int64
	BreakerRejected int64
	DeadlineReject  int64
}

// Result 是仿真输出。
type Result struct {
	Metrics         map[string]*NodeMetrics
	PhysicalRate    map[string]float64
	LogicalRate     map[string]float64
	FailProb        map[string]float64
	LoadRatio       map[string]float64
	Amplification   map[string]float64
	Avalanche       map[string]bool
	MeasureDuration float64
}

type eventKind int

const (
	evArrival eventKind = iota
	evAttemptComplete
	evHedge
	evRetry
)

type event struct {
	at   float64
	kind eventKind
	seq  int64
	call *call
	edge *rtEdge
}

type eventHeap []*event

func (h eventHeap) Len() int            { return len(h) }
func (h eventHeap) Less(i, j int) bool  { return h[i].at < h[j].at }
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

type call struct {
	id            uint64
	node          string
	deadline      float64
	arrival       float64
	attempts      int
	maxAttempts   int
	hedges        int
	settled       bool
	measured      bool
	pending       int // 在途尝试数
	failedAttempt int
}

type attemptRecord struct{ at float64 }

type rtEdge struct {
	cfg     EdgeCfg
	window  float64
	logical []attemptRecord // 窗口内逻辑请求
	extras  []attemptRecord // 窗口内额外尝试（重试+对冲）
}

type rtNode struct {
	cfg      NodeCfg
	attempts []attemptRecord // 窗口内物理尝试，用于估计瞬时负载
	breaker  *retry.Breaker
}

// Simulator 持有一次仿真的全部运行时状态。
type Simulator struct {
	cfg   Config
	rng   *rand.Rand
	now   float64
	evQ   eventHeap
	evSeq int64
	cid   uint64

	nodes map[string]*rtNode
	edges map[string]*rtEdge
	out   map[string][]EdgeCfg
	in    map[string][]EdgeCfg

	metrics map[string]*NodeMetrics
}

// New 构造仿真器。
func New(cfg Config) *Simulator {
	s := &Simulator{
		cfg:     cfg,
		rng:     rand.New(rand.NewSource(cfg.Seed)),
		nodes:   map[string]*rtNode{},
		edges:   map[string]*rtEdge{},
		out:     map[string][]EdgeCfg{},
		in:      map[string][]EdgeCfg{},
		metrics: map[string]*NodeMetrics{},
	}
	for _, n := range cfg.Nodes {
		nb := retry.NewBreaker(n.Breaker)
		s.nodes[n.Name] = &rtNode{cfg: n, breaker: nb}
		s.metrics[n.Name] = &NodeMetrics{}
	}
	for _, e := range cfg.Edges {
		win := cfg.Window
		if win <= 0 {
			win = 10 * time.Second
		}
		s.edges[edgeKey(e.From, e.To)] = &rtEdge{
			cfg:    e,
			window: win.Seconds(),
		}
		s.out[e.From] = append(s.out[e.From], e)
		s.in[e.To] = append(s.in[e.To], e)
	}
	// 为入口节点构造虚拟入边，使入站重试与边侧重试共用同一套预算/窗口逻辑。
	for _, n := range cfg.Nodes {
		if n.IngressRetry == nil {
			continue
		}
		ve := EdgeCfg{
			From: "", To: n.Name, Fanout: 1,
			Retry: *n.IngressRetry,
		}
		win := cfg.Window
		if win <= 0 {
			win = 10 * time.Second
		}
		s.edges[edgeKey("", n.Name)] = &rtEdge{cfg: ve, window: win.Seconds()}
		s.in[n.Name] = append([]EdgeCfg{ve}, s.in[n.Name]...)
	}
	return s
}

func edgeKey(from, to string) string { return from + "->" + to }
