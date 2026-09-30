package main

import "container/heap"

// SimConfig describes a deterministic discrete-event scenario:
//
//	source -> queue Q1 -> lossy link L1 -> relay (duplicates)
//	         -> link L2A -\
//	                       -> merge (first copy wins) -> deliver
//	         -> link L2B -/
//
// Losses on L1 trigger timeout-driven retransmission from the source.
// All times are integer ticks; serialization = ceil(bytes/rate) ticks.
type SimConfig struct {
	NumPackets  int
	PacketBytes int
	Interval    int64 // source inter-arrival ticks
	Rate1       int   // bytes/tick on L1
	Prop1       int64
	RTO         int64 // retransmission timeout, measured from send_start
	LossEvery   int   // drop every kth L1 transmission (0 = lossless)
	Rate2A      int
	Prop2A      int64
	Rate2B      int
	Prop2B      int64
	MaxAttempts int   // 0 = unlimited retransmissions
	MaxTicks    int64 // safety stop, 0 = unlimited
}

type SimResult struct {
	Trace     *Trace
	Delivered map[int]int64 // packet -> deliver tick
	Lost      []int         // packets that exhausted attempts
	Makespan  int64
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

const (
	mGen = iota
	mTxDone
	mRecv1
	mRecv2
	mTimeout
)

type simMsg struct {
	tick    int64
	seq     int
	typ     int
	pkt     int
	attempt int
	link    string
	refID   int // causal event ID carried by the message
}

type msgHeap []simMsg

func (h msgHeap) Len() int { return len(h) }
func (h msgHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h msgHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *msgHeap) Push(x any)   { *h = append(*h, x.(simMsg)) }
func (h *msgHeap) Pop() any {
	old := *h
	n := len(old)
	m := old[n-1]
	*h = old[:n-1]
	return m
}

type queuedPkt struct {
	pkt, attempt int
	evtID        int
}

type sim struct {
	cfg SimConfig
	tr  *Trace
	h   msgHeap
	seq int

	queue      []queuedPkt
	busyUntil  int64
	txCount    int
	prevDeqID  int
	acked      map[[2]int]bool
	delivered  map[int]int64
	deliverEvt map[int]int
	enqEvt     map[[2]int]int
	sendStart  map[[2]int]int
	lost       []int
}

func (s *sim) push(typ int, tick int64, pkt, attempt int, link string, refID int) {
	s.seq++
	heap.Push(&s.h, simMsg{tick: tick, seq: s.seq, typ: typ, pkt: pkt, attempt: attempt, link: link, refID: refID})
}

// RunSim executes the discrete-event model and returns the causal trace.
func RunSim(cfg SimConfig) *SimResult {
	s := &sim{
		cfg:        cfg,
		tr:         NewTrace(),
		acked:      map[[2]int]bool{},
		delivered:  map[int]int64{},
		deliverEvt: map[int]int{},
		enqEvt:     map[[2]int]int{},
		sendStart:  map[[2]int]int{},
	}
	for p := 0; p < cfg.NumPackets; p++ {
		s.push(mGen, int64(p)*cfg.Interval, p, 0, "", 0)
	}
	for len(s.h) > 0 {
		m := heap.Pop(&s.h).(simMsg)
		if cfg.MaxTicks > 0 && m.tick > cfg.MaxTicks {
			break
		}
		switch m.typ {
		case mGen:
			s.enqueue(m.pkt, 0, m.tick, nil)
		case mTxDone:
			s.onTxDone(m)
		case mRecv1:
			s.onRecv1(m)
		case mRecv2:
			s.onRecv2(m)
		case mTimeout:
			s.onTimeout(m)
		}
	}
	res := &SimResult{Trace: s.tr, Delivered: s.delivered, Lost: s.lost}
	for _, e := range s.tr.Events() {
		if e.Tick > res.Makespan {
			res.Makespan = e.Tick
		}
	}
	return res
}

func (s *sim) enqueue(pkt, attempt int, tick int64, parents []int) {
	id := s.tr.Add(Event{Tick: tick, Kind: KindEnqueue, Packet: pkt, Attempt: attempt, Link: "Q1", Parents: parents})
	s.enqEvt[[2]int{pkt, attempt}] = id
	s.queue = append(s.queue, queuedPkt{pkt: pkt, attempt: attempt, evtID: id})
	s.tryServe(tick)
}

func (s *sim) tryServe(now int64) {
	if now < s.busyUntil || len(s.queue) == 0 {
		return
	}
	qp := s.queue[0]
	s.queue = s.queue[1:]

	deqParents := []int{qp.evtID}
	if s.prevDeqID != 0 {
		deqParents = append(deqParents, s.prevDeqID) // server was busy with predecessor
	}
	deqID := s.tr.Add(Event{Tick: now, Kind: KindDequeue, Packet: qp.pkt, Attempt: qp.attempt, Link: "Q1", Parents: deqParents})
	s.prevDeqID = deqID

	ssID := s.tr.Add(Event{Tick: now, Kind: KindSendStart, Packet: qp.pkt, Attempt: qp.attempt, Link: "L1", Parents: []int{deqID}})
	s.sendStart[[2]int{qp.pkt, qp.attempt}] = ssID
	serial := ceilDiv(int64(s.cfg.PacketBytes), int64(s.cfg.Rate1))
	seTick := now + serial
	seID := s.tr.Add(Event{Tick: seTick, Kind: KindSendEnd, Packet: qp.pkt, Attempt: qp.attempt, Link: "L1", Parents: []int{ssID}})
	s.busyUntil = seTick

	s.push(mTxDone, seTick, qp.pkt, qp.attempt, "L1", seID)
	if s.cfg.RTO > 0 {
		s.push(mTimeout, now+s.cfg.RTO, qp.pkt, qp.attempt, "L1", ssID)
	}
}

func (s *sim) onTxDone(m simMsg) {
	s.txCount++
	if s.cfg.LossEvery > 0 && s.txCount%s.cfg.LossEvery == 0 {
		s.tr.Add(Event{Tick: m.tick, Kind: KindDrop, Packet: m.pkt, Attempt: m.attempt, Link: "L1", Parents: []int{m.refID}, Note: "link loss"})
	} else {
		s.push(mRecv1, m.tick+s.cfg.Prop1, m.pkt, m.attempt, "L1", m.refID)
	}
	s.tryServe(m.tick)
}

func (s *sim) onRecv1(m simMsg) {
	recvID := s.tr.Add(Event{Tick: m.tick, Kind: KindRecv, Packet: m.pkt, Attempt: m.attempt, Link: "L1", Parents: []int{m.refID}})
	s.acked[[2]int{m.pkt, m.attempt}] = true
	// Relay duplicates the packet onto both multipath links.
	dupID := s.tr.Add(Event{Tick: m.tick, Kind: KindDuplicate, Packet: m.pkt, Attempt: m.attempt, Link: "R1", Parents: []int{recvID}})
	links := []struct {
		name string
		rate int
		prop int64
	}{
		{"L2A", s.cfg.Rate2A, s.cfg.Prop2A},
		{"L2B", s.cfg.Rate2B, s.cfg.Prop2B},
	}
	for _, l := range links {
		ss := s.tr.Add(Event{Tick: m.tick, Kind: KindSendStart, Packet: m.pkt, Attempt: m.attempt, Link: l.name, Parents: []int{dupID}})
		seTick := m.tick + ceilDiv(int64(s.cfg.PacketBytes), int64(l.rate))
		se := s.tr.Add(Event{Tick: seTick, Kind: KindSendEnd, Packet: m.pkt, Attempt: m.attempt, Link: l.name, Parents: []int{ss}})
		s.push(mRecv2, seTick+l.prop, m.pkt, m.attempt, l.name, se)
	}
}

func (s *sim) onRecv2(m simMsg) {
	recvID := s.tr.Add(Event{Tick: m.tick, Kind: KindRecv, Packet: m.pkt, Attempt: m.attempt, Link: m.link, Parents: []int{m.refID}})
	if _, ok := s.delivered[m.pkt]; !ok {
		// Merge: the first arriving copy delivers the packet.
		dID := s.tr.Add(Event{Tick: m.tick, Kind: KindDeliver, Packet: m.pkt, Attempt: m.attempt, Link: "DST", Parents: []int{recvID}})
		s.delivered[m.pkt] = m.tick
		s.deliverEvt[m.pkt] = dID
	} else {
		s.tr.Add(Event{Tick: m.tick, Kind: KindDiscard, Packet: m.pkt, Attempt: m.attempt, Link: m.link, Parents: []int{recvID, s.deliverEvt[m.pkt]}, Note: "duplicate copy discarded"})
	}
}

func (s *sim) onTimeout(m simMsg) {
	key := [2]int{m.pkt, m.attempt}
	if s.acked[key] {
		return // ACK arrived before the timer fired
	}
	toID := s.tr.Add(Event{Tick: m.tick, Kind: KindTimeout, Packet: m.pkt, Attempt: m.attempt, Link: "L1", Parents: []int{m.refID}})
	next := m.attempt + 1
	if s.cfg.MaxAttempts > 0 && next >= s.cfg.MaxAttempts {
		s.lost = append(s.lost, m.pkt)
		return
	}
	s.enqueue(m.pkt, next, m.tick, []int{toID})
}
