package desim

import "fmt"

// Packet is the unit of traffic in the simulated network.
type Packet struct {
	ID    int
	Size  int // bytes
	Src   int
	Dst   int
	Born  uint64 // tick at which the packet was emitted
	GenID int    // generator that emitted it, -1 for injected packets
}

// Queue is a FIFO byte-capacity buffer in front of a link.
type Queue struct {
	Cap   int // bytes
	Used  int
	Pkts  []Packet
	Drops int
}

func (q *Queue) Enqueue(p Packet) bool {
	if q.Used+p.Size > q.Cap {
		q.Drops++
		return false
	}
	q.Pkts = append(q.Pkts, p)
	q.Used += p.Size
	return true
}

func (q *Queue) Dequeue() (Packet, bool) {
	if len(q.Pkts) == 0 {
		return Packet{}, false
	}
	p := q.Pkts[0]
	q.Pkts = q.Pkts[1:]
	q.Used -= p.Size
	return p, true
}

func (q *Queue) Empty() bool { return len(q.Pkts) == 0 }

// Link is a unidirectional link with a serialization rate and a fixed
// propagation latency, both in integer ticks.
type Link struct {
	ID        int
	From      int
	To        int
	Rate      int    // bytes per tick
	Latency   uint64 // ticks
	BusyUntil uint64 // tick at which the transmitter becomes free
	Q         Queue
}

// Node is a simulation endpoint / router.
type Node struct {
	ID    int
	Name  string
	Links []int // outgoing link IDs
}

// Generator emits packets periodically (with optional jitter) via timer
// events. Its pending timer can be cancelled through its generational
// handle, after which it never fires again.
type Generator struct {
	Node    int
	Dst     int
	MinSize int
	MaxSize int
	Period  uint64
	Jitter  uint64
	Active  bool
	Handle  Handle
	Sent    int
}

// Sim is the discrete-event network simulation.
type Sim struct {
	sch        *Scheduler
	rng        RNG
	Nodes      []Node
	Links      []Link
	Gens       []Generator
	NextPkt    int
	Trace      []string
	Delivered  int
	TotalDelay uint64
}

// NewSim creates a simulation with a deterministic RNG seeded by seed.
func NewSim(seed uint64) *Sim {
	return &Sim{sch: NewScheduler(), rng: newRNG(seed)}
}

// Now returns the current simulation tick.
func (s *Sim) Now() uint64 { return s.sch.Now() }

func (s *Sim) name(id int) string {
	if id >= 0 && id < len(s.Nodes) {
		return s.Nodes[id].Name
	}
	return "?"
}

func (s *Sim) tracef(format string, args ...any) {
	s.Trace = append(s.Trace, fmt.Sprintf("%6d "+format,
		append([]any{s.sch.Now()}, args...)...))
}

// AddNode adds a node and returns its ID.
func (s *Sim) AddNode(name string) int {
	id := len(s.Nodes)
	s.Nodes = append(s.Nodes, Node{ID: id, Name: name})
	return id
}

// AddLink adds a unidirectional link from->to and returns its ID.
// queueCap is the byte capacity of the link's transmit queue.
func (s *Sim) AddLink(from, to, rate int, latency uint64, queueCap int) int {
	id := len(s.Links)
	s.Links = append(s.Links, Link{
		ID:      id,
		From:    from,
		To:      to,
		Rate:    rate,
		Latency: latency,
		Q:       Queue{Cap: queueCap},
	})
	s.Nodes[from].Links = append(s.Nodes[from].Links, id)
	return id
}

// AddGenerator adds a periodic traffic generator and returns its ID.
// The first packet is emitted one period from tick 0.
func (s *Sim) AddGenerator(node, dst, minSize, maxSize int, period, jitter uint64) int {
	id := len(s.Gens)
	g := Generator{
		Node:    node,
		Dst:     dst,
		MinSize: minSize,
		MaxSize: maxSize,
		Period:  period,
		Jitter:  jitter,
		Active:  true,
		Handle:  Handle{Slot: -1},
	}
	s.Gens = append(s.Gens, g)
	s.Gens[id].Handle = s.sch.ScheduleAfter(period, CatTimer, EvTimer, Payload{TimerID: id})
	return id
}

// CancelGenerator stops a generator: its pending timer event is cancelled
// via its generational handle and will never fire.
func (s *Sim) CancelGenerator(id int) {
	g := &s.Gens[id]
	if !g.Active {
		return
	}
	g.Active = false
	if !s.sch.Cancel(g.Handle) {
		panic("desim: generator handle unexpectedly stale")
	}
	s.tracef("cancel gen=%d", id)
}

// Inject emits a one-off packet (not tied to a generator) at the current tick.
func (s *Sim) Inject(src, dst, size int) {
	s.emit(-1, src, dst, size)
}

func (s *Sim) emit(genID, src, dst, size int) {
	p := Packet{ID: s.NextPkt, Size: size, Src: src, Dst: dst, Born: s.sch.Now(), GenID: genID}
	s.NextPkt++
	s.tracef("emit pkt=%d gen=%d %s->%s size=%d", p.ID, genID, s.name(src), s.name(dst), size)
	s.forward(src, p)
}

// forward hands a packet to the outgoing link toward its destination.
func (s *Sim) forward(node int, p Packet) {
	li := s.nextHop(node, p.Dst)
	if li < 0 {
		s.tracef("drop pkt=%d node=%s reason=no-route", p.ID, s.name(node))
		return
	}
	l := &s.Links[li]
	if l.Q.Empty() && s.sch.Now() >= l.BusyUntil {
		s.startTx(l, p)
		return
	}
	if !l.Q.Enqueue(p) {
		s.tracef("drop pkt=%d link=%d reason=queue-full", p.ID, l.ID)
	}
}

// startTx begins serializing a packet onto a link. It schedules the
// link-free event (CatLink) and the packet arrival (CatPacket).
func (s *Sim) startTx(l *Link, p Packet) {
	now := s.sch.Now()
	ser := uint64((p.Size + l.Rate - 1) / l.Rate)
	if ser < 1 {
		ser = 1
	}
	l.BusyUntil = now + ser
	s.tracef("tx-begin pkt=%d link=%d end=%d", p.ID, l.ID, l.BusyUntil)
	s.sch.ScheduleAt(l.BusyUntil, CatLink, EvLinkFree, Payload{LinkID: l.ID})
	s.sch.ScheduleAt(l.BusyUntil+l.Latency, CatPacket, EvPacketArrive,
		Payload{Packet: p, NodeID: l.To})
}

func (s *Sim) onLinkFree(linkID int) {
	l := &s.Links[linkID]
	if p, ok := l.Q.Dequeue(); ok {
		s.startTx(l, p)
	}
}

func (s *Sim) onArrive(p Packet, node int) {
	if node == p.Dst {
		s.Delivered++
		delay := s.sch.Now() - p.Born
		s.TotalDelay += delay
		s.tracef("deliver pkt=%d node=%s delay=%d", p.ID, s.name(node), delay)
		return
	}
	s.tracef("forward pkt=%d node=%s", p.ID, s.name(node))
	s.forward(node, p)
}

func (s *Sim) onTimer(id int) {
	g := &s.Gens[id]
	if !g.Active {
		return
	}
	size := g.MinSize
	if g.MaxSize > g.MinSize {
		size += s.rng.Intn(g.MaxSize - g.MinSize + 1)
	}
	s.emit(id, g.Node, g.Dst, size)
	g.Sent++
	delay := int64(g.Period)
	if g.Jitter > 0 {
		delay += int64(s.rng.Intn(int(2*g.Jitter+1))) - int64(g.Jitter)
	}
	if delay < 1 {
		delay = 1
	}
	g.Handle = s.sch.ScheduleAfter(uint64(delay), CatTimer, EvTimer, Payload{TimerID: id})
}

// nextHop returns the first link on a shortest (hop-count) path src->dst,
// or -1 if unreachable. BFS over links is deterministic.
func (s *Sim) nextHop(src, dst int) int {
	if src == dst {
		return -1
	}
	prev := make([]int, len(s.Nodes))
	via := make([]int, len(s.Nodes))
	for i := range prev {
		prev[i] = -1
	}
	prev[src] = src
	queue := []int{src}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, li := range s.Nodes[n].Links {
			to := s.Links[li].To
			if prev[to] != -1 {
				continue
			}
			prev[to] = n
			via[to] = li
			if to == dst {
				cur := dst
				for prev[cur] != src {
					cur = prev[cur]
				}
				return via[cur]
			}
			queue = append(queue, to)
		}
	}
	return -1
}

func (s *Sim) dispatch(ev Event) {
	switch ev.Kind {
	case EvPacketArrive:
		s.onArrive(ev.Data.Packet, ev.Data.NodeID)
	case EvLinkFree:
		s.onLinkFree(ev.Data.LinkID)
	case EvTimer:
		s.onTimer(ev.Data.TimerID)
	}
}

// Step dispatches the next event. It returns false when no events remain.
func (s *Sim) Step() bool {
	ev, ok := s.sch.Next()
	if !ok {
		return false
	}
	s.dispatch(ev)
	return true
}

// RunUntil dispatches all events with tick <= end.
func (s *Sim) RunUntil(end uint64) {
	for {
		ev, ok := s.sch.Peek()
		if !ok || ev.Tick > end {
			return
		}
		s.sch.Next()
		s.dispatch(ev)
	}
}
