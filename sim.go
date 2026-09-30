package main

import "container/heap"

type Link struct {
	Name        string
	Rate        int
	Prop        int
	OmitTxStart bool
	queue       []txReq
	busy        bool
}

type txReq struct {
	pkt      int
	pathIdx  int
	pos      int
	attempt  int
	enqEvent int
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

const (
	actTxEnd = iota
	actDeliver
	actTimeout
)

type action struct {
	tick      int
	seq       int
	kind      int
	link      int
	pkt       int
	pathIdx   int
	pos       int
	attempt   int
	enqEvent  int
	txStartID int
	txEndID   int
	expect    int
}

type actionHeap []action

func (h actionHeap) Len() int { return len(h) }
func (h actionHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h actionHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *actionHeap) Push(x interface{}) { *h = append(*h, x.(action)) }
func (h *actionHeap) Pop() interface{} {
	old := *h
	n := len(old)
	a := old[n-1]
	*h = old[:n-1]
	return a
}

type pktState struct {
	id          int
	size        int
	paths       [][]int
	createdID   int
	attempt     int
	delivered   bool
	deliveredID int
	lost        bool
	timeoutTick int
	timeoutFrom int
}

type Sim struct {
	now         int
	actions     *actionHeap
	seq         int
	links       []*Link
	rto         int
	maxAttempts int
	drop        func(link string, packet, attempt int) bool
	DAG         *DAG
	pkts        map[int]*pktState
	order       []int
}

func NewSim(links []*Link, rto, maxAttempts int, drop func(string, int, int) bool) *Sim {
	h := &actionHeap{}
	heap.Init(h)
	return &Sim{
		actions: h, links: links, rto: rto, maxAttempts: maxAttempts,
		drop: drop, DAG: NewDAG(), pkts: map[int]*pktState{},
	}
}

func (s *Sim) schedule(a action) {
	a.seq = s.seq
	s.seq++
	heap.Push(s.actions, a)
}

func (s *Sim) Send(pktID, size int, paths [][]int, at int) {
	created := s.DAG.AddEvent(at, "created", pktID, -1, "")
	p := &pktState{
		id: pktID, size: size, paths: paths, createdID: created,
		attempt: 0, timeoutTick: -1, deliveredID: -1,
	}
	s.pkts[pktID] = p
	s.order = append(s.order, pktID)
	for pathIdx := range paths {
		s.enqueue(pktID, pathIdx, 0, 0, created, at)
	}
}

func (s *Sim) enqueue(pktID, pathIdx, pos, attempt, parent, tick int) {
	p := s.pkts[pktID]
	path := p.paths[pathIdx]
	l := s.links[path[pos]]
	copyID := attempt*len(p.paths) + pathIdx
	note := l.Name
	if attempt > 0 && pos == 0 {
		note = l.Name + " (retransmit)"
	}
	ev := s.DAG.AddEvent(tick, "enqueued", pktID, copyID, note)
	s.DAG.AddEdge(parent, ev, PhaseOther)
	l.queue = append(l.queue, txReq{
		pkt: pktID, pathIdx: pathIdx, pos: pos, attempt: attempt, enqEvent: ev,
	})
	if !l.busy {
		s.startNext(path[pos], tick)
	}
}

func (s *Sim) startNext(linkIdx, tick int) {
	l := s.links[linkIdx]
	req := l.queue[0]
	l.queue = l.queue[1:]
	p := s.pkts[req.pkt]
	ser := ceilDiv(p.size, l.Rate)
	copyID := req.attempt*len(p.paths) + req.pathIdx
	txStart := -1
	if !l.OmitTxStart {
		txStart = s.DAG.AddEvent(tick, "tx_start", req.pkt, copyID, l.Name)
		s.DAG.AddEdge(req.enqEvent, txStart, PhaseQueue)
	}
	l.busy = true
	s.schedule(action{
		tick: tick + ser, kind: actTxEnd, link: linkIdx,
		pkt: req.pkt, pathIdx: req.pathIdx, pos: req.pos, attempt: req.attempt,
		enqEvent: req.enqEvent, txStartID: txStart,
	})
}

func (s *Sim) onTxEnd(a action) {
	l := s.links[a.link]
	p := s.pkts[a.pkt]
	copyID := a.attempt*len(p.paths) + a.pathIdx
	ser := ceilDiv(p.size, l.Rate)
	txEnd := s.DAG.AddEvent(a.tick, "tx_end", a.pkt, copyID, l.Name)
	if a.txStartID >= 0 {
		s.DAG.AddEdge(a.txStartID, txEnd, PhaseSerialize)
	} else {
		enqTick := s.DAG.Event(a.enqEvent).Tick
		s.DAG.AddInferredEdge(a.enqEvent, txEnd, PhaseQueueSerialize, ser, a.tick-enqTick)
	}
	l.busy = false
	if len(l.queue) > 0 {
		s.startNext(a.link, a.tick)
	}
	if s.drop != nil && s.drop(l.Name, a.pkt, a.attempt) {
		dropped := s.DAG.AddEvent(a.tick, "dropped", a.pkt, copyID, l.Name)
		s.DAG.AddEdge(txEnd, dropped, PhaseOther)
	} else {
		s.schedule(action{
			tick: a.tick + l.Prop, kind: actDeliver,
			pkt: a.pkt, pathIdx: a.pathIdx, pos: a.pos, attempt: a.attempt,
			txEndID: txEnd,
		})
	}
	t := a.tick + s.rto
	if p.attempt == a.attempt && t > p.timeoutTick {
		p.timeoutTick = t
		p.timeoutFrom = txEnd
		s.schedule(action{tick: t, kind: actTimeout, pkt: a.pkt, attempt: a.attempt, expect: t})
	}
}

func (s *Sim) onDeliver(a action) {
	p := s.pkts[a.pkt]
	path := p.paths[a.pathIdx]
	copyID := a.attempt*len(p.paths) + a.pathIdx
	recv := s.DAG.AddEvent(a.tick, "received", a.pkt, copyID, s.links[path[a.pos]].Name)
	s.DAG.AddEdge(a.txEndID, recv, PhasePropagate)
	if a.pos+1 < len(path) {
		s.enqueue(a.pkt, a.pathIdx, a.pos+1, a.attempt, recv, a.tick)
		return
	}
	if !p.delivered {
		p.delivered = true
		p.deliveredID = s.DAG.AddEvent(a.tick, "delivered", a.pkt, copyID, "merged: first arrival wins")
		s.DAG.AddEdge(recv, p.deliveredID, PhaseOther)
	}
}

func (s *Sim) onTimeout(a action) {
	p := s.pkts[a.pkt]
	if p.delivered || p.attempt != a.attempt || p.timeoutTick != a.expect {
		return
	}
	to := s.DAG.AddEvent(a.tick, "timeout", a.pkt, -1, "")
	s.DAG.AddEdge(p.timeoutFrom, to, PhaseRetransmit)
	if a.attempt+1 > s.maxAttempts {
		p.lost = true
		lost := s.DAG.AddEvent(a.tick, "lost", a.pkt, -1, "max attempts exceeded")
		s.DAG.AddEdge(to, lost, PhaseOther)
		return
	}
	p.attempt++
	p.timeoutTick = -1
	for pathIdx := range p.paths {
		s.enqueue(a.pkt, pathIdx, 0, p.attempt, to, a.tick)
	}
}

func (s *Sim) Run() {
	for s.actions.Len() > 0 {
		a := heap.Pop(s.actions).(action)
		s.now = a.tick
		switch a.kind {
		case actTxEnd:
			s.onTxEnd(a)
		case actDeliver:
			s.onDeliver(a)
		case actTimeout:
			s.onTimeout(a)
		}
	}
}
