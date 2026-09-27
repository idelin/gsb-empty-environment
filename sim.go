package mtusim

import (
	"container/heap"
	"fmt"
)

// Sim is a discrete-event simulator. Time advances in integer ticks only;
// packets, queues and links are pure data structures and no real network
// traffic is ever generated.
type Sim struct {
	Now int
	Log []string

	pq  simHeap
	seq int
}

// Event is anything that can happen at a tick.
type Event interface{ Run(s *Sim) }

type simEvent struct {
	at  int
	seq int
	ev  Event
}

type simHeap []simEvent

func (h simHeap) Len() int { return len(h) }
func (h simHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h simHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *simHeap) Push(x interface{}) { *h = append(*h, x.(simEvent)) }
func (h *simHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Schedule posts ev at the given tick (clamped to the current time).
func (s *Sim) Schedule(at int, ev Event) {
	if at < s.Now {
		at = s.Now
	}
	s.seq++
	heap.Push(&s.pq, simEvent{at: at, seq: s.seq, ev: ev})
}

// Run drains the event queue, advancing Now in integer ticks.
func (s *Sim) Run() {
	for s.pq.Len() > 0 {
		item := heap.Pop(&s.pq).(simEvent)
		s.Now = item.at
		item.ev.Run(s)
	}
}

func (s *Sim) logf(format string, args ...interface{}) {
	s.Log = append(s.Log, fmt.Sprintf("tick %4d  ", s.Now)+fmt.Sprintf(format, args...))
}

// Link is a FIFO transmission queue with an MTU, a per-fragment
// serialization cost and an integer propagation delay, all in ticks.
type Link struct {
	Name    string
	MTU     int
	Delay   int // propagation ticks
	PerFrag int // serialization ticks per fragment (>= 1)

	busyUntil int
	Dropped   int // fragments dropped because they exceeded the MTU
}

// NewLink builds a link that serializes one fragment per tick.
func NewLink(name string, mtu, delay int) *Link {
	return &Link{Name: name, MTU: mtu, Delay: delay, PerFrag: 1}
}

// Enqueue queues a fragment for transmission to dst.
func (l *Link) Enqueue(s *Sim, f Fragment, payload []byte, dst *Receiver) {
	depart := s.Now
	if depart < l.busyUntil {
		depart = l.busyUntil
	}
	per := l.PerFrag
	if per < 1 {
		per = 1
	}
	l.busyUntil = depart + per
	s.logf("link %-4s queue   pkt=%d epoch=%d range=[%3d,%3d) wire=%d depart=%d",
		l.Name, f.PacketID, f.Epoch, f.Offset, f.Offset+f.Len, f.WireSize(), depart)
	s.Schedule(depart, departEvent{l, f, payload, dst})
}

type departEvent struct {
	link    *Link
	frag    Fragment
	payload []byte
	dst     *Receiver
}

func (e departEvent) Run(s *Sim) {
	if e.frag.WireSize() > e.link.MTU {
		e.link.Dropped++
		s.logf("link %-4s DROP    pkt=%d epoch=%d range=[%3d,%3d) wire=%d > mtu=%d",
			e.link.Name, e.frag.PacketID, e.frag.Epoch,
			e.frag.Offset, e.frag.Offset+e.frag.Len, e.frag.WireSize(), e.link.MTU)
		return
	}
	s.logf("link %-4s xmit    pkt=%d epoch=%d range=[%3d,%3d)",
		e.link.Name, e.frag.PacketID, e.frag.Epoch, e.frag.Offset, e.frag.Offset+e.frag.Len)
	s.Schedule(s.Now+e.link.Delay, arriveEvent{e.dst, e.frag, e.payload})
}

type arriveEvent struct {
	dst     *Receiver
	frag    Fragment
	payload []byte
}

func (e arriveEvent) Run(s *Sim) {
	e.dst.Receive(s, e.frag, e.payload)
}

// Source fragments datagrams under the current path MTU and re-plans
// (with a fresh epoch) whenever a retransmission timer fires after an
// MTU correction.
type Source struct {
	Name      string
	Link      *Link
	Dst       *Receiver
	PathMTU   int
	HeaderLen int
	Align     int
	Timeout   int // retransmission delay in ticks

	epoch   map[int]int
	pending map[int][]byte
}

// NewSource builds a sender attached to a link.
func NewSource(name string, link *Link, dst *Receiver, mtu, header, align, timeout int) *Source {
	return &Source{
		Name: name, Link: link, Dst: dst, PathMTU: mtu,
		HeaderLen: header, Align: align, Timeout: timeout,
		epoch: make(map[int]int), pending: make(map[int][]byte),
	}
}

// Send schedules transmission of a datagram at the given tick.
func (src *Source) Send(s *Sim, at, packetID int, data []byte) {
	s.Schedule(at, sendEvent{src, packetID, data})
}

// EpochOf returns the current fragmentation epoch of a packet.
func (src *Source) EpochOf(packetID int) int { return src.epoch[packetID] }

// Ack stops retransmission of a delivered packet.
func (src *Source) Ack(packetID int) { delete(src.pending, packetID) }

type sendEvent struct {
	src  *Source
	id   int
	data []byte
}

func (e sendEvent) Run(s *Sim) { e.src.transmit(s, e.id, e.data) }

func (src *Source) transmit(s *Sim, id int, data []byte) {
	epoch := src.epoch[id] + 1
	src.epoch[id] = epoch
	p := FragParams{HeaderLen: src.HeaderLen, MTU: src.PathMTU, Align: src.Align}
	frags, err := p.Fragmentize(id, epoch, len(data))
	if err != nil {
		delete(src.pending, id)
		s.logf("src  %-4s FAIL    pkt=%d len=%d mtu=%d: %v", src.Name, id, len(data), src.PathMTU, err)
		return
	}
	src.pending[id] = data
	s.logf("src  %-4s plan    pkt=%d epoch=%d len=%d -> %d frag(s) (mtu=%d header=%d align=%d)",
		src.Name, id, epoch, len(data), len(frags), src.PathMTU, src.HeaderLen, src.Align)
	for _, f := range frags {
		src.Link.Enqueue(s, f, data[f.Offset:f.Offset+f.Len], src.Dst)
	}
	s.Schedule(s.Now+src.Timeout, timeoutEvent{src, id, epoch})
}

type timeoutEvent struct {
	src   *Source
	id    int
	epoch int
}

func (e timeoutEvent) Run(s *Sim) {
	data, ok := e.src.pending[e.id]
	if !ok {
		return // already delivered
	}
	if e.src.epoch[e.id] != e.epoch {
		return // superseded by a newer plan
	}
	s.logf("src  %-4s timeout pkt=%d epoch=%d: re-planning with path mtu=%d",
		e.src.Name, e.id, e.epoch, e.src.PathMTU)
	e.src.transmit(s, e.id, data)
}

// Receiver reassembles datagrams. Fragments from a newer epoch reset the
// reassembly context; fragments from an older epoch are stale and ignored.
// Mixing epochs inside one context is rejected by the Reassembler itself.
type Receiver struct {
	Name string

	reasm map[int]*Reassembler
	epoch map[int]int
	Done  map[int][]byte

	// OnDone is called when a datagram completes (used to stop retransmission).
	OnDone func(s *Sim, packetID int)
}

// NewReceiver builds an empty receiver.
func NewReceiver(name string) *Receiver {
	return &Receiver{
		Name:  name,
		reasm: make(map[int]*Reassembler),
		epoch: make(map[int]int),
		Done:  make(map[int][]byte),
	}
}

// Receive handles one arrived fragment.
func (rx *Receiver) Receive(s *Sim, f Fragment, payload []byte) {
	cur := rx.epoch[f.PacketID]
	switch {
	case f.Epoch < cur:
		s.logf("rx   %-4s stale   pkt=%d epoch=%d (current %d) range=[%3d,%3d) ignored",
			rx.Name, f.PacketID, f.Epoch, cur, f.Offset, f.Offset+f.Len)
		return
	case f.Epoch > cur:
		if cur != 0 {
			s.logf("rx   %-4s reset   pkt=%d epoch %d -> %d (mtu corrected, re-planned)",
				rx.Name, f.PacketID, cur, f.Epoch)
		}
		rx.epoch[f.PacketID] = f.Epoch
		rx.reasm[f.PacketID] = NewReassembler(f.PacketID, f.Epoch)
	}
	r := rx.reasm[f.PacketID]
	if err := r.Add(f, payload); err != nil {
		s.logf("rx   %-4s reject  pkt=%d epoch=%d range=[%3d,%3d): %v",
			rx.Name, f.PacketID, f.Epoch, f.Offset, f.Offset+f.Len, err)
		return
	}
	s.logf("rx   %-4s accept  pkt=%d epoch=%d range=[%3d,%3d) (%d/%d bytes)",
		rx.Name, f.PacketID, f.Epoch, f.Offset, f.Offset+f.Len, r.Received(), r.totalOr(-1))
	if r.Complete {
		data := r.Bytes()
		rx.Done[f.PacketID] = data
		s.logf("rx   %-4s done    pkt=%d epoch=%d reassembled %d bytes",
			rx.Name, f.PacketID, f.Epoch, len(data))
		if rx.OnDone != nil {
			rx.OnDone(s, f.PacketID)
		}
	}
}

func (r *Reassembler) totalOr(d int) int {
	if r.total < 0 {
		return d
	}
	return r.total
}

// MTUChangeEvent applies a path MTU correction at a tick: the link starts
// dropping oversized fragments and the source re-plans future transmissions.
type MTUChangeEvent struct {
	Link   *Link
	Src    *Source
	NewMTU int
}

// Run applies the MTU change.
func (e MTUChangeEvent) Run(s *Sim) {
	s.logf("path mtu update: link %s mtu %d -> %d", e.Link.Name, e.Link.MTU, e.NewMTU)
	e.Link.MTU = e.NewMTU
	if e.Src != nil {
		e.Src.PathMTU = e.NewMTU
	}
}

// ScheduleMTUChange posts an MTU correction at the given tick.
func ScheduleMTUChange(s *Sim, at int, link *Link, src *Source, newMTU int) {
	s.Schedule(at, MTUChangeEvent{Link: link, Src: src, NewMTU: newMTU})
}
