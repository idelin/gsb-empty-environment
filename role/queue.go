package role

// msgQueue is the per-controller, strictly ordered outbound message queue.
// OpenFlow requires messages from one connection to be processed in order,
// and role demotion must invalidate the state-modifying messages the
// controller queued before the demotion while preserving ordering around
// barrier requests.
//
// Every envelope receives a monotonically increasing sequence number. When
// a master is demoted the arbitrator places an invalidation watermark at
// the current queue tail. State-modifying envelopes below the watermark
// come back from pop with VerdictDrop; barriers and read messages remain
// deliverable so barrier replies still delimit the invalidated region
// exactly once, in order.
type msgQueue struct {
	items []queuedItem
	// head is the index of the next item to dequeue.
	head int
	// seq is the sequence number assigned to the last enqueued item.
	seq uint64
	// invalidateBefore is the demotion watermark: the seq of the last
	// item queued before the demotion. Items with seq <= watermark are
	// pre-transition; later items carry larger seq values.
	invalidateBefore uint64
}

// queuedItem pairs an envelope with its queue-wide sequence number.
type queuedItem struct {
	env *Envelope
	seq uint64
}

// push appends a message and returns the assigned sequence number.
func (q *msgQueue) push(env *Envelope) uint64 {
	q.seq++
	q.items = append(q.items, queuedItem{env: env, seq: q.seq})
	return q.seq
}

// markInvalidated places the invalidation watermark at the current tail so
// that everything already queued (seq <= watermark) is treated as
// pre-transition. Items enqueued afterwards receive higher sequence
// numbers and are unaffected.
func (q *msgQueue) markInvalidated() {
	q.invalidateBefore = q.seq
}

// pop returns the next envelope in order, annotating it with the verdict
// implied by any prior demotion. It returns nil when the queue is empty.
func (q *msgQueue) pop() *Envelope {
	if q.head >= len(q.items) {
		return nil
	}
	item := q.items[q.head]
	q.head++
	// Compact once the consumed prefix has grown large enough to keep the
	// backing array small on long-lived connections.
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	} else if q.head > 16 && q.head*2 >= len(q.items) {
		n := copy(q.items, q.items[q.head:])
		q.items = q.items[:n]
		q.head = 0
	}
	if item.seq <= q.invalidateBefore && IsStateModifying(item.env.MsgType) {
		item.env.Verdict = VerdictDrop
	} else {
		item.env.Verdict = VerdictDeliver
	}
	return item.env
}

// len reports the number of envelopes still waiting to be dequeued.
func (q *msgQueue) len() int {
	return len(q.items) - q.head
}
