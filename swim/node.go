package swim

import (
	"fmt"
	"math/rand"
	"time"
)

// State 是成员节点在本地视图中的健康状态。
type State int

const (
	Alive State = iota
	Suspect
	Down
)

func (s State) String() string {
	switch s {
	case Alive:
		return "ALIVE"
	case Suspect:
		return "SUSPECT"
	case Down:
		return "DOWN"
	}
	return "UNKNOWN"
}

// Node 是一个 SWIM 协议节点：每个协议周期随机选取一个对等节点 Ping，
// 直连超时后委托 K 个代理节点发送 PingReq 间接探测。
type Node struct {
	Addr      string
	Transport Transport
	Clock     Clock

	// K 为间接探测的代理节点数量。
	K int
	// PingTimeout 为直连 Ping 的超时时限。
	PingTimeout time.Duration
	// IndirectTimeout 为间接 PingReq 的超时时限。
	IndirectTimeout time.Duration

	// SelectTarget 每个周期选取探测目标，默认随机；测试可注入以定向探测。
	SelectTarget func(peers []string) string

	rand    *rand.Rand
	seq     uint64
	members map[string]State
	acks    map[uint64]bool
}

func NewNode(addr string, transport Transport, clock Clock) *Node {
	return &Node{
		Addr:            addr,
		Transport:       transport,
		Clock:           clock,
		K:               2,
		PingTimeout:     100 * time.Millisecond,
		IndirectTimeout: 200 * time.Millisecond,
		rand:            rand.New(rand.NewSource(time.Now().UnixNano())),
		members:         make(map[string]State),
		acks:            make(map[uint64]bool),
	}
}

// Join 将对等节点加入本地成员视图，初始状态为 Alive。
func (n *Node) Join(peers ...string) {
	for _, p := range peers {
		if p != n.Addr {
			n.members[p] = Alive
		}
	}
}

// State 返回本地视图中某节点的状态。
func (n *Node) State(peer string) State { return n.members[peer] }

// Members 返回本地成员视图快照。
func (n *Node) Members() map[string]State {
	out := make(map[string]State, len(n.members))
	for addr, st := range n.members {
		out[addr] = st
	}
	return out
}

// Handle 处理收到的协议消息（由 Transport 同步调用）。
func (n *Node) Handle(msg Message) {
	switch msg.Type {
	case Ping:
		n.Transport.Send(Message{Type: Ack, From: n.Addr, To: msg.From, Seq: msg.Seq})
	case PingReq:
		// 代理节点代为直连目标，成功则把 Ack 转发回发起者。
		seq := n.nextSeq()
		n.acks[seq] = false
		n.Transport.Send(Message{Type: Ping, From: n.Addr, To: msg.Target, Seq: seq})
		if n.acks[seq] {
			n.Transport.Send(Message{Type: Ack, From: n.Addr, To: msg.From, Seq: msg.Seq})
		}
		delete(n.acks, seq)
	case Ack:
		if _, pending := n.acks[msg.Seq]; pending {
			n.acks[msg.Seq] = true
		}
	}
}

// RunPeriod 执行一个协议周期：选取目标、直连 Ping、必要时触发 K 路间接探测，
// 并根据结果推进目标在本地视图中的状态机 Alive -> Suspect -> Down。
func (n *Node) RunPeriod() (target string, ok bool) {
	target = n.pickTarget()
	if target == "" {
		return "", false
	}
	if n.probe(target) {
		n.members[target] = Alive
		return target, true
	}
	switch n.members[target] {
	case Alive:
		n.members[target] = Suspect
	case Suspect:
		n.members[target] = Down
	}
	return target, false
}

// probe 先直连 Ping，超时后委托 K 个代理 PingReq；任一 Ack 在时限内到达即成功。
func (n *Node) probe(target string) bool {
	seq := n.nextSeq()
	n.acks[seq] = false
	deadline := n.Clock.Now().Add(n.PingTimeout)
	n.Transport.Send(Message{Type: Ping, From: n.Addr, To: target, Seq: seq})
	direct := n.acks[seq] && !n.Clock.Now().After(deadline)
	delete(n.acks, seq)
	if direct {
		return true
	}

	deadline = n.Clock.Now().Add(n.IndirectTimeout)
	for _, proxy := range n.pickProxies(target) {
		seq = n.nextSeq()
		n.acks[seq] = false
		n.Transport.Send(Message{Type: PingReq, From: n.Addr, To: proxy, Target: target, Seq: seq})
		ok := n.acks[seq]
		delete(n.acks, seq)
		if ok && !n.Clock.Now().After(deadline) {
			return true
		}
	}
	return false
}

func (n *Node) pickTarget() string {
	peers := n.alivePeers("")
	if len(peers) == 0 {
		return ""
	}
	if n.SelectTarget != nil {
		return n.SelectTarget(peers)
	}
	return peers[n.rand.Intn(len(peers))]
}

func (n *Node) pickProxies(target string) []string {
	peers := n.alivePeers(target)
	n.rand.Shuffle(len(peers), func(i, j int) { peers[i], peers[j] = peers[j], peers[i] })
	if len(peers) > n.K {
		peers = peers[:n.K]
	}
	return peers
}

func (n *Node) alivePeers(except string) []string {
	var peers []string
	for addr, st := range n.members {
		if addr != except && st != Down {
			peers = append(peers, addr)
		}
	}
	return peers
}

func (n *Node) nextSeq() uint64 {
	n.seq++
	return n.seq
}

func (n *Node) String() string {
	return fmt.Sprintf("node %s members=%v", n.Addr, n.members)
}
