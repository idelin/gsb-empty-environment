package reassembly

import "sync"

// EventKind identifies the type of a stream Event.
type EventKind int

const (
	// EventData delivers the next contiguous run of confirmed bytes.
	// Bytes in a single EventData are always gap-free; data on opposite
	// sides of a lost interval is never spliced into one event.
	EventData EventKind = iota
	// EventGap reports an interval that was evicted under memory pressure
	// and will never be delivered. Seq is the first missing byte and End
	// is one past the last missing byte. Consumers must treat the byte
	// stream as discontinuous here (e.g. reset pattern-matching state) so
	// that bytes from both sides of the gap are never joined into a
	// spurious match.
	EventGap
	// EventClosed is the final event on a connection. The subscription
	// channel is closed after it is delivered.
	EventClosed
)

// Event is a single notification delivered to subscribers of a Conn.
type Event struct {
	Kind EventKind
	Seq  uint32 // EventData: sequence of Data[0]. EventGap: first missing byte.
	End  uint32 // EventGap only: one past the last missing byte.
	Data []byte // EventData only: contiguous confirmed bytes starting at Seq.
}

type ctrlKind int

const (
	ctrlEvent ctrlKind = iota
	ctrlSubscribe
	ctrlUnsubscribe
)

type ctrl struct {
	kind ctrlKind
	ev   Event
	ch   chan Event
}

// ctrlQueue is an unbounded FIFO feeding a connection's dispatcher
// goroutine, so writers never block on slow consumers.
type ctrlQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []ctrl
	closed bool
}

func (q *ctrlQueue) init() { q.cond = sync.NewCond(&q.mu) }

func (q *ctrlQueue) push(c ctrl) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, c)
	q.cond.Signal()
}

func (q *ctrlQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Signal()
}

// pop returns the next message, or ok=false once the queue is closed and
// fully drained.
func (q *ctrlQueue) pop() (c ctrl, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return ctrl{}, false
	}
	c = q.items[0]
	q.items[0] = ctrl{}
	q.items = q.items[1:]
	return c, true
}
