package backtest

import "container/heap"

// eventQueue is a min-heap ordered by (ArrivalTime, Seq): events become
// visible in arrival order, and events arriving at the same instant are
// dispatched in input order.
type eventQueue []Event

func (q eventQueue) Len() int { return len(q) }

func (q eventQueue) Less(i, j int) bool {
	ti, si := q[i].orderingKey()
	tj, sj := q[j].orderingKey()
	if !ti.Equal(tj) {
		return ti.Before(tj)
	}
	return si < sj
}

func (q eventQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *eventQueue) Push(x interface{}) {
	*q = append(*q, x.(Event))
}

func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	ev := old[n-1]
	*q = old[:n-1]
	return ev
}

func newEventQueue(events []Event) *eventQueue {
	q := make(eventQueue, 0, len(events))
	for _, ev := range events {
		q = append(q, ev)
	}
	heap.Init(&q)
	return &q
}

func (q *eventQueue) next() (Event, bool) {
	if q.Len() == 0 {
		return Event{}, false
	}
	return heap.Pop(q).(Event), true
}
