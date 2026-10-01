package main

import "container/heap"

// event is either a script command (send) or a packet delivery, both
// scheduled at an integer tick. Ties break by insertion order.
type event struct {
	tick  int
	order int
	cmd   *Command
	pkt   *Packet
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// EventQueue is the discrete-event scheduler: all packets, queues and link
// deliveries happen at integer ticks; no real network traffic exists.
type EventQueue struct {
	h   eventHeap
	seq int
}

func (q *EventQueue) Push(tick int, cmd *Command, pkt *Packet) {
	heap.Push(&q.h, event{tick: tick, order: q.seq, cmd: cmd, pkt: pkt})
	q.seq++
}

func (q *EventQueue) Pop() (event, bool) {
	if len(q.h) == 0 {
		return event{}, false
	}
	return heap.Pop(&q.h).(event), true
}

// Sim wires script commands to the observer through a link with a
// per-packet integer delay. The sender side tracks the current epoch and
// per-direction next sequence number for each connection.
type Sim struct {
	tuples   map[string]Tuple
	Obs      *Observer
	q        *EventQueue
	delay    int // default link delay in ticks
	epoch    map[string]int
	next     map[string][2]int // conn -> dir -> next seq to send
	LastTick int
}

func NewSim(tuples map[string]Tuple, defaultDelay, timeWait int) *Sim {
	return &Sim{
		tuples: tuples,
		Obs:    NewObserver(timeWait),
		q:      &EventQueue{},
		delay:  defaultDelay,
		epoch:  make(map[string]int),
		next:   make(map[string][2]int),
	}
}

func (s *Sim) Submit(c Command) {
	cmd := c
	s.q.Push(cmd.Tick, &cmd, nil)
}

// Run drains the event queue, advancing the discrete clock event by event.
func (s *Sim) Run() {
	for {
		e, ok := s.q.Pop()
		if !ok {
			break
		}
		s.LastTick = e.tick
		if e.cmd != nil {
			s.exec(*e.cmd)
		} else {
			s.Obs.Handle(e.tick, *e.pkt)
		}
	}
	s.Obs.Finish()
}

// exec turns a script command into a packet and pushes it onto the link,
// to be delivered after the (integer) link delay.
func (s *Sim) exec(c Command) {
	tuple, ok := s.tuples[c.Conn]
	if !ok {
		return // rejected by the parser; unreachable in practice
	}
	p := Packet{ConnID: c.Conn, Tuple: tuple, Dir: c.Dir, Payload: c.Payload}
	switch c.Kind {
	case "SYN":
		if c.Dir == C2S {
			s.epoch[c.Conn]++ // a client SYN starts a new incarnation
		}
		if s.epoch[c.Conn] == 0 {
			s.epoch[c.Conn] = 1
		}
		p.SYN = true
		p.Seq = c.Seq
		s.setNext(c.Conn, c.Dir, c.Seq+1)
	case "DATA":
		p.Seq = c.Seq
		if end := c.Seq + len(c.Payload); end > s.next[c.Conn][c.Dir] {
			s.setNext(c.Conn, c.Dir, end)
		}
	case "FIN":
		p.FIN = true
		if c.HasSeq {
			p.Seq = c.Seq
		} else {
			p.Seq = s.next[c.Conn][c.Dir]
		}
		s.setNext(c.Conn, c.Dir, p.Seq+1)
	case "RST":
		p.RST = true
		p.Seq = c.Seq
	}
	p.Epoch = s.epoch[c.Conn]
	if p.Epoch == 0 {
		p.Epoch = 1
	}
	delay := s.delay
	if c.HasDelay {
		delay = c.Delay
	}
	pkt := p
	s.q.Push(c.Tick+delay, nil, &pkt)
}

// setNext stores the per-direction next sequence number (map of arrays
// cannot be assigned through, so copy-update-store).
func (s *Sim) setNext(conn string, dir Dir, v int) {
	n := s.next[conn]
	n[dir] = v
	s.next[conn] = n
}
