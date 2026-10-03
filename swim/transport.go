package swim

import "time"

// Transport 负责节点间消息投递。Send 同步投递并返回是否成功发出（未丢包）。
type Transport interface {
	Send(msg Message) bool
}

// SimTransport 是内存模拟网络：同步投递、可配置每跳延迟与丢包规则，
// 用于在测试时钟下复现弱网络环境。
type SimTransport struct {
	clock   Clock
	Latency time.Duration
	// Drop 返回 true 时丢弃该消息（模拟丢包）。
	Drop  func(msg Message) bool
	nodes map[string]*Node
	// 统计，便于测试断言与演示输出。
	Sent      int
	Dropped   int
	Delivered int
}

func NewSimTransport(clock Clock, latency time.Duration) *SimTransport {
	return &SimTransport{clock: clock, Latency: latency, nodes: make(map[string]*Node)}
}

func (t *SimTransport) Register(n *Node) { t.nodes[n.Addr] = n }

func (t *SimTransport) Send(msg Message) bool {
	t.Sent++
	t.clock.Advance(t.Latency)
	if t.Drop != nil && t.Drop(msg) {
		t.Dropped++
		return false
	}
	dst, ok := t.nodes[msg.To]
	if !ok {
		t.Dropped++
		return false
	}
	t.Delivered++
	dst.Handle(msg)
	return true
}
