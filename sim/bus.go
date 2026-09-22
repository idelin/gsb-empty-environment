package sim

import "fmt"

// Msg 是节点之间传递的一条消息。
type Msg struct {
	ID       string // 幂等键（命令号或事件号）
	Kind     string // "cmd" | "event" | "ack"
	From     string
	To       string
	Payload  any
	delivers int // 同一条消息的投递次数（重发会产生新 Msg）
}

// Handler 处理一条投递到节点的消息。
type Handler func(n *Node, m *Msg)

// DropRule 判定一条消息是否在传输中丢失；规则可以按内容有界地丢包。
type DropRule func(m *Msg) bool

type Bus struct {
	sim      *Sim
	handlers map[string]Handler
	drop     DropRule
	lo, hi   int64 // 投递延迟的随机区间（含端点）

	sendCount  map[string]int
	dropCount  map[string]int
	delivCount map[string]int
}

func newBus(s *Sim) *Bus {
	return &Bus{
		sim:        s,
		handlers:   map[string]Handler{},
		lo:         3,
		hi:         7,
		sendCount:  map[string]int{},
		dropCount:  map[string]int{},
		delivCount: map[string]int{},
	}
}

// Handle 注册某节点的消息处理函数。
func (b *Bus) Handle(node string, h Handler) { b.handlers[node] = h }

// SetDelay 设置随机投递延迟区间 [lo, hi]。
func (b *Bus) SetDelay(lo, hi int64) {
	if lo < 1 || hi < lo {
		panic("sim: bad delay range")
	}
	b.lo, b.hi = lo, hi
}

// SetDropRule 安装丢包规则。
func (b *Bus) SetDropRule(fn DropRule) { b.drop = fn }

// Send 发送一条消息。To 节点当前不存活则直接丢弃（崩溃期间无处投递）；
// 命中丢包规则也丢弃。存活节点收到的消息在稍后的独立事件中投递。
func (b *Bus) Send(m *Msg) {
	if m.From == "" {
		m.From = m.To
	}
	b.sendCount[m.Kind]++
	b.sim.trace(Trace{Kind: "send", Msg: m})

	target := b.sim.nodes[m.To]
	if target == nil || !target.alive {
		b.dropCount[m.Kind]++
		b.sim.trace(Trace{Kind: "drop", Label: "node-down", Msg: m})
		return
	}
	if b.drop != nil && b.drop(m) {
		b.dropCount[m.Kind]++
		b.sim.trace(Trace{Kind: "drop", Label: "rule", Msg: m})
		return
	}

	delay := b.lo
	if b.hi > b.lo {
		delay += b.sim.rng.Int63n(b.hi - b.lo + 1)
	}
	m2 := *m
	b.sim.After(delay, func() {
		target := b.sim.nodes[m2.To]
		if target == nil || !target.alive {
			b.dropCount[m2.Kind]++
			b.sim.trace(Trace{Kind: "drop", Label: "node-down", Msg: &m2})
			return
		}
		h := b.handlers[m2.To]
		if h == nil {
			panic("sim: no handler for node " + m2.To)
		}
		b.delivCount[m2.Kind]++
		b.sim.trace(Trace{Kind: "deliver", Msg: &m2})
		h(target, &m2)
	})
}

// SendCount 返回某类消息的发送次数。
func (b *Bus) SendCount(kind string) int { return b.sendCount[kind] }

// DropCount 返回某类消息的丢弃次数。
func (b *Bus) DropCount(kind string) int { return b.dropCount[kind] }

// DeliveredCount 返回某类消息成功投递的次数。
func (b *Bus) DeliveredCount(kind string) int { return b.delivCount[kind] }

// DeliveriesTo 返回目标节点收到的消息总数（含重复，用于断言重投发生过）。
func (b *Bus) DeliveriesTo(node string) int {
	n := 0
	for _, t := range b.sim.traces {
		if t.Kind == "deliver" && t.Msg.To == node {
			n++
		}
	}
	return n
}

func (m *Msg) String() string {
	return fmt.Sprintf("%s %s->%s id=%s", m.Kind, m.From, m.To, m.ID)
}
