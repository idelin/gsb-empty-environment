package main

// Node 租约领导候选人。它用本地(可能有偏差的)时钟判断租约是否有效,
// 判断可能出错 —— 出错时资源端的 fencing token 兜底。
type Node struct {
	id         string
	sim        *Sim
	net        *Network
	clock      *Clock
	grantorID  string
	resourceID string
	ttl        int64

	lease  Lease
	leader bool

	pausedUntil    int64
	believedLeader int // 本地时钟判定"租约仍有效"并发出写的次数(含误判)
	misjudged      int // 其中在真实时间上租约已过期的次数(模拟器全知视角)
	committed      []string
}

func NewNode(id string, sim *Sim, net *Network, clock *Clock,
	grantorID, resourceID string, ttl int64) *Node {
	n := &Node{
		id: id, sim: sim, net: net, clock: clock,
		grantorID: grantorID, resourceID: resourceID, ttl: ttl,
	}
	net.Register(id, n.handle)
	return n
}

func (n *Node) paused() bool { return n.sim.Now() < n.pausedUntil }

// Pause 建模进程暂停(如 GC stop-the-world): 期间不处理任何事件,
// 恢复时本地时钟静止了 d 毫秒(显式时钟偏差)。
func (n *Node) Pause(d int64) {
	n.pausedUntil = n.sim.Now() + d
	n.sim.Logf("%s: 进程暂停 %dms (GC stop-the-world)", n.id, d)
	n.sim.After(d, func() {
		n.clock.Freeze(d)
		n.sim.Logf("%s: 恢复运行, 本地时钟读数 t=%dms (真实 t=%dms, 偏差 -%dms)",
			n.id, n.clock.Now(), n.sim.Now(), d)
	})
}

// Campaign 请求(或续期)租约。
func (n *Node) Campaign() {
	if n.paused() {
		return
	}
	n.net.Send(Message{From: n.id, To: n.grantorID, Kind: MsgAcquire, TTL: n.ttl})
}

// WriteValue 用本地时钟检查租约后发出写。
// 注意: 这个检查只是启发式的, 时钟偏差下可能误判 —— 租约不是强隔离。
func (n *Node) WriteValue(value string) {
	if !n.leader || n.paused() {
		return
	}
	if n.clock.Now() >= n.lease.Expiry {
		n.sim.Logf("%s: 本地时钟 t=%dms 判定租约已过期, 放弃写", n.id, n.clock.Now())
		n.leader = false
		return
	}
	n.believedLeader++
	if n.sim.Now() >= n.lease.Expiry { // 地面真相: 租约其实已过期
		n.misjudged++
	}
	n.sim.Logf("%s: 本地时钟 t=%dms 判定租约有效, 发出写 token=%d value=%q",
		n.id, n.clock.Now(), n.lease.Token, value)
	n.net.Send(Message{From: n.id, To: n.resourceID, Kind: MsgWrite,
		Token: n.lease.Token, Value: value})
}

func (n *Node) handle(m Message) {
	if n.paused() {
		return // 暂停期间消息被丢弃(进程不运行)
	}
	switch m.Kind {
	case MsgGrant:
		n.lease = m.Lease
		n.leader = true
		n.sim.Logf("%s: 成为领导, token=%d", n.id, n.lease.Token)
	case MsgDeny:
		n.sim.Logf("%s: 租约请求被拒", n.id)
	case MsgAck:
		n.committed = append(n.committed, m.Value)
		n.sim.Logf("%s: 写 %q (token=%d) 已提交", n.id, m.Value, m.Token)
	case MsgReject:
		n.sim.Logf("%s: 写 %q (token=%d) 被资源拒绝", n.id, m.Value, m.Token)
	}
}

// Committed 返回该节点收到 ack 的写。
func (n *Node) Committed() []string { return n.committed }
