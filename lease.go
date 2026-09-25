package main

// Lease 租约: Token 即 fencing token, 由授予者严格单调发放。
// Expiry 是授予者本地时钟域上的过期时刻 —— 持有者用自己的时钟去比较,
// 两个时钟域之间的偏差就是安全假设可能失效的地方。
type Lease struct {
	Holder string
	Token  int64
	Expiry int64
}

// Grantor 租约授予者: 同一时刻只承认一个持有者;
// 每次授予(含续期)发放严格递增的 fencing token。
type Grantor struct {
	id         string
	sim        *Sim
	net        *Network
	clock      *Clock
	current    Lease
	hasCurrent bool
	lastToken  int64
}

func NewGrantor(id string, sim *Sim, net *Network, clock *Clock) *Grantor {
	g := &Grantor{id: id, sim: sim, net: net, clock: clock}
	net.Register(id, g.handle)
	return g
}

func (g *Grantor) handle(m Message) {
	if m.Kind != MsgAcquire {
		return
	}
	now := g.clock.Now()
	if g.hasCurrent && now < g.current.Expiry && g.current.Holder != m.From {
		g.sim.Logf("grantor: 拒绝 %s 的租约请求 (持有者 %s 的租约剩余 %dms)",
			m.From, g.current.Holder, g.current.Expiry-now)
		g.net.Send(Message{From: g.id, To: m.From, Kind: MsgDeny})
		return
	}
	g.lastToken++
	g.current = Lease{Holder: m.From, Token: g.lastToken, Expiry: now + m.TTL}
	g.hasCurrent = true
	g.sim.Logf("grantor: 授予 %s 租约 token=%d (本地时钟过期时刻 t=%dms)",
		m.From, g.current.Token, g.current.Expiry)
	g.net.Send(Message{From: g.id, To: m.From, Kind: MsgGrant, Lease: g.current})
}
