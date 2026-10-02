package sim

import (
	"fmt"
	"hash/fnv"
	"net/netip"

	"rib"
)

// Packet is a simulated packet. All fields are in-memory only.
type Packet struct {
	ID   int
	Src  netip.Addr
	Dst  netip.Addr
	Born uint64 // tick at injection
	Hops int
}

// Link is a store-and-forward link: each packet occupies the link for
// Interval ticks (serialization, FIFO queueing) and then takes Delay
// propagation ticks to arrive.
type Link struct {
	Name     string
	Delay    uint64
	Interval uint64
	OnDrop   func(p *Packet, why string)

	freeAt uint64
}

// Send queues p on the link; arrival is delivered to deliver.
func (l *Link) Send(s *Sim, p *Packet, deliver func(p *Packet)) {
	start := s.Now()
	if l.freeAt > start {
		start = l.freeAt
	}
	depart := start + l.Interval
	l.freeAt = depart
	s.At(depart+l.Delay, func() { deliver(p) })
}

// Router forwards packets using a rib.Table snapshot taken per packet.
type Router struct {
	Name  string
	Table *rib.Table
	// Links maps a next-hop address to the outgoing link and the handler on
	// the far side.
	Links map[netip.Addr]*Link
	// Local marks addresses terminated on this router.
	Local map[netip.Addr]bool
	// Deliver is called for packets whose Dst is local.
	Deliver func(s *Sim, p *Packet)
	// OnDrop is called for unroutable packets.
	OnDrop func(s *Sim, p *Packet)

	ends map[*Link]func(p *Packet)
}

func NewRouter(name string, tab *rib.Table) *Router {
	return &Router{
		Name:  name,
		Table: tab,
		Links: map[netip.Addr]*Link{},
		Local: map[netip.Addr]bool{},
		ends:  map[*Link]func(p *Packet){},
	}
}

// Connect attaches a link for the given next-hop address; receive is invoked
// with packets arriving at the far end.
func (r *Router) Connect(nh netip.Addr, l *Link, receive func(p *Packet)) {
	r.Links[nh] = l
	r.ends[l] = receive
}

// Receive handles a packet arriving at (or injected into) this router.
func (r *Router) Receive(s *Sim, p *Packet) {
	p.Hops++
	if r.Local[p.Dst] {
		if r.Deliver != nil {
			r.Deliver(s, p)
		}
		return
	}
	v := r.Table.Acquire()
	_, rt, ok := v.Lookup(p.Dst)
	ver := v.Version()
	v.Release()
	if !ok {
		r.drop(s, p, fmt.Sprintf("no route (table v%d)", ver))
		return
	}
	nh, ok := rt.SelectNextHop(flowHash(p))
	if !ok {
		r.drop(s, p, "empty next-hop group")
		return
	}
	l := r.Links[nh]
	if l == nil {
		r.drop(s, p, "no link to next-hop "+nh.String())
		return
	}
	deliver := r.ends[l]
	l.Send(s, p, deliver)
}

func (r *Router) drop(s *Sim, p *Packet, why string) {
	if r.OnDrop != nil {
		r.OnDrop(s, p)
	}
	_ = why
}

func flowHash(p *Packet) uint64 {
	h := fnv.New64a()
	s := p.Src.As16()
	d := p.Dst.As16()
	h.Write(s[:])
	h.Write(d[:])
	return h.Sum64()
}
