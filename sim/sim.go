package sim

import (
	"container/heap"
	"time"
)

type Handler interface {
	Receive(msg interface{})
}

type event struct {
	at   time.Time
	seq  uint64
	from string
	to   string
	msg  interface{}
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].seq < h[j].seq
	}
	return h[i].at.Before(h[j].at)
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(event))
}

func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

type linkKey struct {
	from string
	to   string
}

type Simulator struct {
	clock       *ReferenceClock
	handlers    map[string]Handler
	paused      map[string][]event
	crashed     map[string]bool
	dropped     map[linkKey]bool
	delays      map[linkKey]time.Duration
	defaultWait time.Duration
	events      eventHeap
	seq         uint64

	Delivered uint64
	Dropped   uint64
	Held      uint64
}

func NewSimulator(start time.Time) *Simulator {
	return &Simulator{
		clock:    NewReferenceClock(start),
		handlers: make(map[string]Handler),
		paused:   make(map[string][]event),
		crashed:  make(map[string]bool),
		dropped:  make(map[linkKey]bool),
		delays:   make(map[linkKey]time.Duration),
	}
}

func (s *Simulator) Now() time.Time {
	return s.clock.Now()
}

func (s *Simulator) Clock() *ReferenceClock {
	return s.clock
}

func (s *Simulator) Register(name string, h Handler) {
	s.handlers[name] = h
}

func (s *Simulator) SetDefaultDelay(d time.Duration) {
	s.defaultWait = d
}

func (s *Simulator) SetDelay(from, to string, d time.Duration) {
	s.delays[linkKey{from: from, to: to}] = d
}

func (s *Simulator) Drop(from, to string) {
	s.dropped[linkKey{from: from, to: to}] = true
}

func (s *Simulator) ClearDrop(from, to string) {
	delete(s.dropped, linkKey{from: from, to: to})
}

func (s *Simulator) Pause(name string) {
	if _, ok := s.paused[name]; !ok {
		s.paused[name] = nil
	}
}

func (s *Simulator) Resume(name string) {
	held, ok := s.paused[name]
	if !ok {
		return
	}
	delete(s.paused, name)
	for _, ev := range held {
		ev.at = s.clock.Now()
		s.seq++
		ev.seq = s.seq
		heap.Push(&s.events, ev)
	}
}

func (s *Simulator) Crash(name string) {
	s.crashed[name] = true
}

func (s *Simulator) Recover(name string) {
	delete(s.crashed, name)
}

func (s *Simulator) Send(from, to string, msg interface{}) {
	key := linkKey{from: from, to: to}
	if s.dropped[key] {
		s.Dropped++
		return
	}
	delay, ok := s.delays[key]
	if !ok {
		delay = s.defaultWait
	}
	s.SendAfter(delay, from, to, msg)
}

func (s *Simulator) SendAfter(delay time.Duration, from, to string, msg interface{}) {
	s.SendAt(s.clock.Now().Add(delay), from, to, msg)
}

func (s *Simulator) SendAt(at time.Time, from, to string, msg interface{}) {
	if at.Before(s.clock.Now()) {
		at = s.clock.Now()
	}
	s.seq++
	heap.Push(&s.events, event{at: at, seq: s.seq, from: from, to: to, msg: msg})
}

func (s *Simulator) Step() bool {
	if len(s.events) == 0 {
		return false
	}
	ev := heap.Pop(&s.events).(event)
	if ev.at.After(s.clock.Now()) {
		s.clock.Set(ev.at)
	}
	if s.crashed[ev.to] {
		s.Dropped++
		return true
	}
	if _, ok := s.paused[ev.to]; ok {
		s.paused[ev.to] = append(s.paused[ev.to], ev)
		s.Held++
		return true
	}
	h, ok := s.handlers[ev.to]
	if !ok {
		s.Dropped++
		return true
	}
	h.Receive(ev.msg)
	s.Delivered++
	return true
}

func (s *Simulator) RunUntil(deadline time.Time) {
	for len(s.events) > 0 && !s.events[0].at.After(deadline) {
		s.Step()
	}
	if deadline.After(s.clock.Now()) {
		s.clock.Set(deadline)
	}
}

func (s *Simulator) Advance(d time.Duration) {
	s.RunUntil(s.clock.Now().Add(d))
}

func (s *Simulator) Pending() int {
	return len(s.events)
}
