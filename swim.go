package main

// 协议周期内的探测时点（以周期的小数部分表示）。
const (
	// probeOffset 是直接 ping 在一个周期内发生的时点。
	probeOffset = 0.25
	// indirectOffset 是间接 ping（经 k 个中介节点）发生的时点。
	indirectOffset = 0.75
)

// EventType 区分注入的故障类型。
type EventType int

const (
	// CrashInjection 真实节点宕机（永久故障）。
	CrashInjection EventType = iota
	// PacketLossInjection 短暂网络丢包扰动（瞬时故障）。
	PacketLossInjection
)

func (t EventType) String() string {
	if t == CrashInjection {
		return "crash"
	}
	return "packet-loss"
}

// Injection 描述一次故障注入事件。
type Injection struct {
	ID       int
	Type     EventType
	Target   int     // 目标节点 ID
	Cycle    int     // 注入发生的协议周期
	Offset   float64 // 周期内的注入时点（0.0=探测前，0.9=探测后）
	Duration float64 // 丢包窗口长度（周期数，仅 PacketLossInjection 使用）
}

// Time 返回注入的连续时间（以周期为单位）。
func (in Injection) Time() float64 { return float64(in.Cycle) + in.Offset }

// ObsKind 是模拟器产生的观测事件类型。
type ObsKind int

const (
	// Suspected 节点首次被怀疑。
	Suspected ObsKind = iota
	// SuspicionRefuted 怀疑被澄清（节点重新响应）。
	SuspicionRefuted
	// MarkedFailed 怀疑超时，节点被最终标记为故障。
	MarkedFailed
)

// Observation 是模拟器在运行中记录的一条观测。
type Observation struct {
	Kind   ObsKind
	Target int
	Cycle  int
}

type node struct {
	id          int
	crashedAt   float64 // -1 表示仍存活
	lossStart   float64 // 丢包窗口 [lossStart, lossEnd)
	lossEnd     float64
	suspected   bool
	suspectedAt int
	failed      bool
}

// downAt 判断节点在时刻 t 是否无法响应（宕机或处于丢包窗口内）。
func (n *node) downAt(t float64) bool {
	if n.crashedAt >= 0 && t >= n.crashedAt {
		return true
	}
	return t >= n.lossStart && t < n.lossEnd
}

// Cluster 是一个简化的 SWIM 集群模拟器。
// 采用环形探测拓扑：节点 i 由节点 (i+1)%N 监控，
// 每个周期执行 1 次直接 ping，失败后追加间接 ping，
// 怀疑超时（SuspicionTimeout 个周期）未澄清则标记为故障。
type Cluster struct {
	NumNodes         int
	SuspicionTimeout int
	nodes            []*node
}

// NewCluster 创建包含 numNodes 个节点的集群。
func NewCluster(numNodes, suspicionTimeout int) *Cluster {
	c := &Cluster{NumNodes: numNodes, SuspicionTimeout: suspicionTimeout}
	for i := 0; i < numNodes; i++ {
		c.nodes = append(c.nodes, &node{id: i, crashedAt: -1, lossStart: -1, lossEnd: -1})
	}
	return c
}

// Run 按注入计划运行 cycles 个协议周期，返回全部观测事件。
func (c *Cluster) Run(injections []Injection, cycles int) []Observation {
	byCycle := make(map[int][]Injection)
	for _, in := range injections {
		byCycle[in.Cycle] = append(byCycle[in.Cycle], in)
	}

	var obs []Observation
	for cyc := 0; cyc < cycles; cyc++ {
		// 1. 应用本周期调度的故障注入。
		for _, in := range byCycle[cyc] {
			n := c.nodes[in.Target]
			if in.Type == CrashInjection {
				n.crashedAt = in.Time()
			} else {
				n.lossStart = in.Time()
				n.lossEnd = in.Time() + in.Duration
			}
		}
		// 2. 怀疑超时判定：超时仍未澄清则最终标记为故障。
		for _, n := range c.nodes {
			if n.suspected && !n.failed && cyc >= n.suspectedAt+c.SuspicionTimeout {
				n.failed = true
				n.suspected = false
				obs = append(obs, Observation{Kind: MarkedFailed, Target: n.id, Cycle: cyc})
			}
		}
		// 3. 探测轮：每个存活监控节点探测其环上下一节点。
		for _, target := range c.nodes {
			if target.failed {
				continue
			}
			monitor := c.nodes[(target.id+1)%len(c.nodes)]
			if monitor.crashedAt >= 0 || monitor.failed {
				continue // 监控节点自身已宕机
			}
			directOK := !target.downAt(float64(cyc) + probeOffset)
			indirectOK := !target.downAt(float64(cyc) + indirectOffset)
			if directOK || indirectOK {
				if target.suspected {
					target.suspected = false
					obs = append(obs, Observation{Kind: SuspicionRefuted, Target: target.id, Cycle: cyc})
				}
				continue
			}
			if !target.suspected {
				target.suspected = true
				target.suspectedAt = cyc
				obs = append(obs, Observation{Kind: Suspected, Target: target.id, Cycle: cyc})
			}
		}
	}
	return obs
}
