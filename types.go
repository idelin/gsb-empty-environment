// Package leasesim 在单机上模拟主备租约管理状态机。
//
// 时序模型：全部事件发生在离散的 tick 上。
//   - 单向网络延迟 LinkDelay tick；节点收到报文后 ProcessingDelay tick 才发出应答；
//   - 每个节点本地时钟 = 全局 tick + 偏移，偏移的界为 DriftBound（ε）。
//
// 租约安全不变式（L = LeaseDuration）：
//   - 主节点只在自己时钟 <= 最后多数派应答 + L - ε 时提供服务，超时自动降级；
//   - 从节点在自己时钟 >= 最后心跳 + L + ε 之前拒绝为其他候选者投票。
//
// 因为主侧减去 ε、从侧加上 ε，只要真实漂移不超过 ε，新旧主的租约窗口绝不重叠，
// 从而杜绝双主脑裂写入。
package leasesim

import "fmt"

// Role 是租约协商状态机的节点状态。
type Role int

const (
	Follower Role = iota
	Candidate
	Primary
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Primary:
		return "Primary"
	}
	return "?"
}

// MsgKind 是节点间报文类型。
type MsgKind int

const (
	MsgLeaseRequest MsgKind = iota // 候选者请求投票并授予租约
	MsgLeaseGrant                  // 投票者授予投票+租约
	MsgHeartbeat                   // 主节点心跳，用于续租
	MsgHeartbeatAck                // 从节点心跳应答
)

func (k MsgKind) String() string {
	switch k {
	case MsgLeaseRequest:
		return "LeaseRequest"
	case MsgLeaseGrant:
		return "LeaseGrant"
	case MsgHeartbeat:
		return "Heartbeat"
	case MsgHeartbeatAck:
		return "HeartbeatAck"
	}
	return "?"
}

// Message 是一条在网络中传输的报文。
type Message struct {
	From, To    int
	Kind        MsgKind
	Epoch       int
	DeliverTick int // 计划送达的全局 tick
}

// Config 是模拟参数。
type Config struct {
	NumNodes          int
	LeaseDuration     int   // L：租约长度（tick）
	DriftBound        int   // ε：时钟漂移上界（tick）
	LinkDelay         int   // 单向网络延迟（tick）
	ProcessingDelay   int   // 收到报文到发出应答的处理延迟（tick）
	HeartbeatInterval int   // 主节点心跳间隔（tick）
	RetryInterval     int   // 候选者重发 LeaseRequest 的间隔（tick）
	ClockOffset       []int // 可选：每节点时钟偏移，绝对值 <= DriftBound
}

// DefaultConfig 返回验收场景使用的参数。
func DefaultConfig() Config {
	return Config{
		NumNodes:          5,
		LeaseDuration:     11,
		DriftBound:        1,
		LinkDelay:         2,
		ProcessingDelay:   1,
		HeartbeatInterval: 2,
		RetryInterval:     4,
	}
}

// Majority 返回多数派门槛。
func (c Config) Majority() int { return c.NumNodes/2 + 1 }

// electionOffset 让各节点的选举超时确定性错开，避免平票。
func (c Config) electionOffset(id int) int {
	return ((id + c.NumNodes - 1) % c.NumNodes) * 2
}

// Node 是单个节点的状态机。
type Node struct {
	ID    int
	Role  Role
	Epoch int // 纪元，每次成功选举递增

	votedFor          int // 当前纪元已投票给谁（-1 表示未投）
	leader            int // 当前认为持有租约的节点（-1 表示无）
	lastLeaderContact int // 最后一次收到心跳/投出选票的本地 tick
	leaseStart        int // 主节点：租约开始的本地 tick
	lastRenewal       int // 主节点：最后一次获得多数派应答的本地 tick
	lastRetry         int // 候选者：最后一次广播 LeaseRequest 的本地 tick
	votes             map[int]bool
	ackTick           map[int]int // 主节点：各从节点最近一次应答的本地 tick
}

// leaseValid 报告本节点视角下当前租约是否仍有效（含 +ε 保守等待）。
func (n *Node) leaseValid(lt int, cfg Config) bool {
	return n.leader >= 0 && lt < n.lastLeaderContact+cfg.LeaseDuration+cfg.DriftBound
}

// Event 是一条记录在离散 tick 上的事件。
type Event struct {
	Tick   int
	Node   int
	From   int // 报文来源节点，无来源为 -1
	Kind   string
	Detail string
}

func (e Event) String() string {
	if e.From >= 0 {
		return fmt.Sprintf("tick %3d  node %d  %-13s from=%d  %s", e.Tick, e.Node, e.Kind, e.From, e.Detail)
	}
	return fmt.Sprintf("tick %3d  node %d  %-13s %s", e.Tick, e.Node, e.Kind, e.Detail)
}

// Spike 建模针对某个节点的网络黑洞/延迟突增：
// 在 [Start, End) 内送达该节点（或从该节点发出）的报文额外延迟 Extra tick。
type Spike struct {
	Node       int
	Start, End int
	Extra      int
}
