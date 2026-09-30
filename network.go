package sim

// Link is a bidirectional point-to-point link. Each direction has a
// FIFO transmission queue: a packet occupies the link for TxTime ticks
// (serialisation) and then takes Delay ticks to propagate.
type Link struct {
	ID     int
	A, B   *Router
	Cost   int
	Delay  int // propagation delay in ticks
	TxTime int // serialisation time per packet in ticks
	Up     bool

	Packets    int // total packets transmitted
	MaxBacklog int // worst-case queue backlog in ticks

	busyAB int // next free tick of the A->B queue
	busyBA int // next free tick of the B->A queue
}

func (l *Link) other(r *Router) *Router {
	if r == l.A {
		return l.B
	}
	return l.A
}

// Send enqueues a packet on the link's per-direction FIFO queue and
// schedules its delivery entirely inside the discrete-event model.
func (l *Link) Send(from *Router, msg Message) {
	s := from.net.Sim
	now := s.Now()
	var busy *int
	if from == l.A {
		busy = &l.busyAB
	} else {
		busy = &l.busyBA
	}
	start := now
	if *busy > start {
		start = *busy
	}
	*busy = start + l.TxTime
	l.Packets++
	if backlog := *busy - now; backlog > l.MaxBacklog {
		l.MaxBacklog = backlog
	}
	to := l.other(from)
	s.After(start+l.TxTime+l.Delay-now, func() {
		to.receive(msg, from)
	})
}

// Network is a set of routers connected by links, driven by one
// discrete-event simulator.
type Network struct {
	Sim     *Simulator
	Routers map[int]*Router
	Links   []*Link
}

func NewNetwork(s *Simulator) *Network {
	return &Network{Sim: s, Routers: make(map[int]*Router)}
}

func (n *Network) AddRouter(r *Router) {
	r.net = n
	n.Routers[r.ID] = r
}

func (n *Network) AddLink(a, b *Router, cost, delay, txTime int) *Link {
	l := &Link{
		ID: len(n.Links), A: a, B: b,
		Cost: cost, Delay: delay, TxTime: txTime, Up: true,
	}
	n.Links = append(n.Links, l)
	a.links = append(a.links, l)
	b.links = append(b.links, l)
	return l
}

// SetLinkUp brings a link up or down; both endpoints detect the change
// and originate a fresh LSA.
func (n *Network) SetLinkUp(l *Link, up bool) {
	if l.Up == up {
		return
	}
	l.Up = up
	n.Sim.After(0, l.A.onLinkChange)
	n.Sim.After(0, l.B.onLinkChange)
}

// SetRouterDown fails a router and all of its links.
func (n *Network) SetRouterDown(r *Router) {
	if r.Down {
		return
	}
	r.Down = true
	for _, l := range r.links {
		n.SetLinkUp(l, false)
	}
}

// LinkBetween returns the link connecting routers a and b, or nil.
func (n *Network) LinkBetween(a, b int) *Link {
	ra := n.Routers[a]
	for _, l := range ra.links {
		if l.other(ra).ID == b {
			return l
		}
	}
	return nil
}
