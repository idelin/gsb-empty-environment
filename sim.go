package main

import (
	"container/heap"
	"fmt"
)

// Event kinds in the discrete-event model. All times are integer ticks.
const (
	evArrive = iota
	evTransmit
	evDeliver
)

type event struct {
	tick int
	seq  int // FIFO tie-break within one tick
	kind int
	pkt  Packet
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Link is a point-to-point link with a FIFO queue. It serializes one
// packet per Interval ticks and delivers after Delay ticks.
type Link struct {
	Interval int // serialization ticks per packet
	Delay    int // propagation ticks
	Capacity int // max queued packets; 0 means unlimited

	queue    []Packet
	nextFree int
}

// Sim is a discrete-event simulator: packets are classified, queued
// and transmitted, all at integer ticks. Nothing touches a real network.
type Sim struct {
	now    int
	seq    int
	events eventHeap

	classifier Classifier
	link       *Link

	Log       []string
	Delivered int
	Dropped   int
}

func NewSim(c Classifier, l *Link) *Sim {
	return &Sim{classifier: c, link: l}
}

func (s *Sim) schedule(tick, kind int, p Packet) {
	s.seq++
	heap.Push(&s.events, event{tick: tick, seq: s.seq, kind: kind, pkt: p})
}

// Inject schedules a synthetic packet to enter the classifier at tick.
func (s *Sim) Inject(tick int, p Packet) {
	s.schedule(tick, evArrive, p)
}

func (s *Sim) logf(format string, args ...any) {
	s.Log = append(s.Log, fmt.Sprintf("tick=%-3d ", s.now)+fmt.Sprintf(format, args...))
}

// Run processes events up to and including maxTick and returns the log.
func (s *Sim) Run(maxTick int) []string {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		if e.tick > maxTick {
			break
		}
		s.now = e.tick
		switch e.kind {
		case evArrive:
			s.onArrive(e.pkt)
		case evTransmit:
			s.onTransmit()
		case evDeliver:
			s.onDeliver(e.pkt)
		}
	}
	return s.Log
}

func (s *Sim) onArrive(p Packet) {
	act, rule := s.classifier.Decide(p)
	if rule == nil {
		s.Dropped++
		s.logf("%s dropped (no rule matched, default deny)", p)
		return
	}
	if act == Deny {
		s.Dropped++
		s.logf("%s dropped by rule #%d (deny)", p, rule.ID)
		return
	}
	l := s.link
	if l.Capacity > 0 && len(l.queue) >= l.Capacity {
		s.Dropped++
		s.logf("%s dropped (queue full, cap=%d)", p, l.Capacity)
		return
	}
	wasEmpty := len(l.queue) == 0
	l.queue = append(l.queue, p)
	s.logf("%s allowed by rule #%d, enqueued (qlen=%d)", p, rule.ID, len(l.queue))
	if wasEmpty {
		tx := s.now
		if tx < l.nextFree {
			tx = l.nextFree
		}
		s.schedule(tx, evTransmit, Packet{})
	}
}

func (s *Sim) onTransmit() {
	l := s.link
	if len(l.queue) == 0 {
		return
	}
	p := l.queue[0]
	l.queue = l.queue[1:]
	l.nextFree = s.now + l.Interval
	s.logf("%s dequeued, link busy until tick=%d", p, l.nextFree)
	s.schedule(s.now+l.Delay, evDeliver, p)
	if len(l.queue) > 0 {
		s.schedule(l.nextFree, evTransmit, Packet{})
	}
}

func (s *Sim) onDeliver(p Packet) {
	s.Delivered++
	s.logf("%s delivered", p)
}
