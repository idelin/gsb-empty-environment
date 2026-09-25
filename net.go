package main

// 消息类型
const (
	MsgAcquire = "acquire" // 节点 -> 授予者: 请求/续期租约
	MsgGrant   = "grant"   // 授予者 -> 节点: 授予租约(携带 fencing token)
	MsgDeny    = "deny"    // 授予者 -> 节点: 拒绝(租约被他人持有)
	MsgWrite   = "write"   // 节点 -> 资源: 携带 fencing token 的写
	MsgAck     = "ack"     // 资源 -> 节点: 写已接受
	MsgReject  = "reject"  // 资源 -> 节点: 写被拒绝(token 非法)
)

type Message struct {
	From  string
	To    string
	Kind  string
	Lease Lease
	Token int64
	Value string
	TTL   int64
}

// Network 由模拟器驱动的确定性网络: 固定延迟, 支持显式指定延迟(乱序/滞留消息)。
type Network struct {
	sim      *Sim
	handlers map[string]func(Message)
	delay    int64
}

func NewNetwork(sim *Sim, delay int64) *Network {
	return &Network{sim: sim, handlers: make(map[string]func(Message)), delay: delay}
}

func (n *Network) Register(id string, h func(Message)) { n.handlers[id] = h }

// Send 以默认延迟发送。
func (n *Network) Send(m Message) { n.SendAfter(m, n.delay) }

// SendAfter 以指定延迟发送, 用于建模乱序与长时间滞留的旧消息。
func (n *Network) SendAfter(m Message, d int64) {
	n.sim.After(d, func() {
		if h, ok := n.handlers[m.To]; ok {
			h(m)
		}
	})
}
