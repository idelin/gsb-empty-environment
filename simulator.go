package swimlab

// InjectionKind 注入动作类型。
type InjectionKind int

const (
	// InjectCrash 节点宕机，停止响应探测。
	InjectCrash InjectionKind = iota
	// InjectPacketLoss 短暂丢包，持续 Duration 个协议周期。
	InjectPacketLoss
	// InjectRecover 节点恢复并重新加入集群。
	InjectRecover
)

// Injection 描述某协议周期对某节点施加的动作。
type Injection struct {
	Tick     int
	NodeID   int
	Kind     InjectionKind
	Duration int // 仅 InjectPacketLoss 使用
}

// ObservationKind 检测器观测类型。
type ObservationKind int

const (
	// ObsSuspect 首次失败探测，节点进入怀疑状态。
	ObsSuspect ObservationKind = iota
	// ObsMark 连续失败达到阈值，节点被标记为故障。
	ObsMark
	// ObsClear 怀疑被澄清（节点恢复响应或重新加入）。
	ObsClear
)

// Observation 检测器在某个协议周期对某节点的一次状态变更。
type Observation struct {
	Tick   int
	NodeID int
	Kind   ObservationKind
}

// Config SWIM 检测器参数。
type Config struct {
	NumNodes      int // 集群节点数
	MarkThreshold int // 连续失败探测达到该次数后标记节点（怀疑超时）
}

type nodeView struct {
	alive     bool // 事实：节点是否存活
	lossUntil int  // 事实：丢包截止周期（不含）
	missed    int  // 检测器：连续失败探测计数
	suspected bool // 检测器：已怀疑
	marked    bool // 检测器：已标记故障
}

// Simulator 离散化的 SWIM 风格故障检测模拟器。
//
// 每个协议周期对每个成员执行一次探测：首次失败进入怀疑，
// 连续失败达到 MarkThreshold 后标记为故障；节点恢复响应即澄清怀疑。
// 短暂丢包（持续周期数小于 MarkThreshold）只会触发怀疑，不会造成误标记。
type Simulator struct {
	cfg   Config
	nodes []*nodeView
	obs   []Observation
}

// NewSimulator 创建一个所有节点均存活的模拟器。
func NewSimulator(cfg Config) *Simulator {
	nodes := make([]*nodeView, cfg.NumNodes)
	for i := range nodes {
		nodes[i] = &nodeView{alive: true}
	}
	return &Simulator{cfg: cfg, nodes: nodes}
}

// Run 运行 periods 个协议周期，按时刻应用注入，返回全部观测。
func (s *Simulator) Run(periods int, injections []Injection) []Observation {
	byTick := make(map[int][]Injection)
	for _, inj := range injections {
		byTick[inj.Tick] = append(byTick[inj.Tick], inj)
	}
	for t := 0; t < periods; t++ {
		for _, inj := range byTick[t] {
			s.apply(t, inj)
		}
		s.probeAll(t)
	}
	return s.obs
}

func (s *Simulator) apply(t int, inj Injection) {
	n := s.nodes[inj.NodeID]
	switch inj.Kind {
	case InjectCrash:
		n.alive = false
	case InjectPacketLoss:
		n.lossUntil = t + inj.Duration
	case InjectRecover:
		n.alive = true
		n.lossUntil = 0
		if n.suspected || n.marked {
			s.emit(t, inj.NodeID, ObsClear)
		}
		n.missed = 0
		n.suspected = false
		n.marked = false
	}
}

func (s *Simulator) probeAll(t int) {
	for id, n := range s.nodes {
		if n.alive && t >= n.lossUntil {
			if n.suspected && !n.marked {
				s.emit(t, id, ObsClear)
			}
			n.missed = 0
			n.suspected = false
			continue
		}
		if n.marked {
			continue // 已标记节点停止探测，等待重新加入
		}
		n.missed++
		if n.missed == 1 {
			n.suspected = true
			s.emit(t, id, ObsSuspect)
		}
		if n.missed >= s.cfg.MarkThreshold {
			n.marked = true
			s.emit(t, id, ObsMark)
		}
	}
}

func (s *Simulator) emit(t, nodeID int, kind ObservationKind) {
	s.obs = append(s.obs, Observation{Tick: t, NodeID: nodeID, Kind: kind})
}
