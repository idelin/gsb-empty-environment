package sim

import (
	"container/heap"
	"errors"
)

var ErrEventLimit = errors.New("event limit reached")

type Scheduler struct {
	now   int64
	seq   int64
	queue eventHeap
}

func NewScheduler(start int64) *Scheduler {
	return &Scheduler{now: start}
}

func (s *Scheduler) Now() int64 {
	return s.now
}

func (s *Scheduler) At(delay int64, action func()) {
	if delay < 0 {
		delay = 0
	}
	s.seq++
	heap.Push(&s.queue, &scheduledEvent{
		at:     s.now + delay,
		seq:    s.seq,
		action: action,
	})
}

func (s *Scheduler) Pending() int {
	return s.queue.Len()
}

func (s *Scheduler) Run(maxEvents int) error {
	for range maxEvents {
		if s.queue.Len() == 0 {
			return nil
		}
		event := heap.Pop(&s.queue).(*scheduledEvent)
		s.now = event.at
		event.action()
	}
	if s.queue.Len() > 0 {
		return ErrEventLimit
	}
	return nil
}

type scheduledEvent struct {
	at     int64
	seq    int64
	action func()
}

type eventHeap []*scheduledEvent

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(value any) { *h = append(*h, value.(*scheduledEvent)) }

func (h *eventHeap) Pop() any {
	old := *h
	last := len(old) - 1
	event := old[last]
	*h = old[:last]
	return event
}
