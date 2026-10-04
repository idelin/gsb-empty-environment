// Package batchq implements a capacity-bounded batch task queue.
//
// Callers submit a batch of tasks and choose an admission mode:
// AllOrNothing admits the whole batch only if it fits, LongestPrefix
// admits the longest prefix of the batch that fits in the remaining
// capacity. Admission happens in strict FIFO order: a reservation is
// served only after every earlier reservation has been served, so a
// later batch can never be admitted ahead of an earlier one.
//
// Reserve returns a Reservation holding the admitted tasks and the
// reserved capacity. The caller then either Enqueue (commits the tasks
// to the queue) or Cancel (releases the reservation). Cancellation
// after reservation always refunds exactly the reserved amount,
// regardless of admission mode.
package batchq

import "sync"

// Mode selects how a batch is admitted.
type Mode int

const (
	// AllOrNothing admits the whole batch, or nothing if it does not fit.
	AllOrNothing Mode = iota
	// LongestPrefix admits the longest prefix of the batch that fits.
	LongestPrefix
)

// Task is a unit of work with a capacity cost.
type Task struct {
	ID   string
	Cost int
}

// Queue is a capacity-bounded FIFO batch queue.
type Queue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	capacity int
	used     int
	maxUsed  int

	nextTicket uint64
	serving    uint64

	log []logEntry
}

type logEntry struct {
	seq  uint64
	task Task
}

// New returns a Queue with the given capacity.
func New(capacity int) *Queue {
	if capacity < 0 {
		panic("batchq: negative capacity")
	}
	q := &Queue{capacity: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Capacity returns the total capacity of the queue.
func (q *Queue) Capacity() int {
	return q.capacity
}

// Used returns the currently reserved (and enqueued) capacity.
func (q *Queue) Used() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used
}

// MaxUsed returns the high-water mark of reserved capacity. It is
// guaranteed never to exceed Capacity.
func (q *Queue) MaxUsed() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.maxUsed
}

// Reserve admits tasks according to mode and reserves capacity for the
// admitted tasks. Reservations are served in FIFO order of arrival.
// The returned Reservation must be settled with Enqueue or Cancel.
func (q *Queue) Reserve(tasks []Task, mode Mode) *Reservation {
	for _, t := range tasks {
		if t.Cost <= 0 {
			panic("batchq: task cost must be positive")
		}
	}

	q.mu.Lock()
	ticket := q.nextTicket
	q.nextTicket++
	for ticket != q.serving {
		q.cond.Wait()
	}

	free := q.capacity - q.used
	var admitted []Task
	switch mode {
	case AllOrNothing:
		if totalCost(tasks) <= free {
			admitted = append(admitted, tasks...)
		}
	case LongestPrefix:
		for _, t := range tasks {
			if t.Cost > free {
				break
			}
			free -= t.Cost
			admitted = append(admitted, t)
		}
	default:
		panic("batchq: unknown mode")
	}

	cost := totalCost(admitted)
	q.used += cost
	if q.used > q.maxUsed {
		q.maxUsed = q.used
	}

	q.serving++
	q.cond.Broadcast()
	q.mu.Unlock()

	return &Reservation{q: q, seq: ticket, tasks: admitted, cost: cost}
}

// Tasks returns the enqueued tasks in admission (FIFO) order.
func (q *Queue) Tasks() []Task {
	q.mu.Lock()
	entries := make([]logEntry, len(q.log))
	copy(entries, q.log)
	q.mu.Unlock()

	// Entries are appended at Enqueue time, which may be out of
	// reservation order; sort by reservation sequence (stable, so
	// tasks of one reservation stay contiguous and in order).
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].seq > entries[j].seq; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
	tasks := make([]Task, len(entries))
	for i, e := range entries {
		tasks[i] = e.task
	}
	return tasks
}

func totalCost(tasks []Task) int {
	total := 0
	for _, t := range tasks {
		total += t.Cost
	}
	return total
}

// Reservation is a pending admission. Exactly one of Enqueue or Cancel
// may take effect; later calls are no-ops.
type Reservation struct {
	q     *Queue
	seq   uint64
	tasks []Task
	cost  int
	done  bool
}

// Seq returns the reservation's position in the FIFO admission order.
func (r *Reservation) Seq() uint64 {
	return r.seq
}

// Admitted returns the tasks admitted by this reservation.
func (r *Reservation) Admitted() []Task {
	out := make([]Task, len(r.tasks))
	copy(out, r.tasks)
	return out
}

// Enqueue commits the admitted tasks to the queue and returns them.
// The reserved capacity remains held by the queue. Calling Enqueue
// after Cancel (or twice) is a no-op and returns nil.
func (r *Reservation) Enqueue() []Task {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	if r.done {
		return nil
	}
	r.done = true
	for _, t := range r.tasks {
		r.q.log = append(r.q.log, logEntry{seq: r.seq, task: t})
	}
	out := make([]Task, len(r.tasks))
	copy(out, r.tasks)
	return out
}

// Cancel releases the reservation, refunding exactly the reserved
// capacity. The same rule applies to both admission modes. Calling
// Cancel after Enqueue (or twice) is a no-op.
func (r *Reservation) Cancel() {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	r.q.used -= r.cost
}
