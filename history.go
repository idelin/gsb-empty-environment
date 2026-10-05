package mpqueue

import (
	"fmt"
	"reflect"
)

// OpKind identifies the kind of a committed operation in the history.
type OpKind int8

const (
	// OpNone marks the absence of a shutdown winner in a Summary.
	OpNone    OpKind = -1
	OpEnqueue OpKind = iota
	OpDequeue
	OpClose
	OpAbort
)

func (op OpKind) String() string {
	switch op {
	case OpEnqueue:
		return "enqueue"
	case OpDequeue:
		return "dequeue"
	case OpClose:
		return "close"
	case OpAbort:
		return "abort"
	}
	return "none"
}

// Event is one committed operation. Events carry a monotonically
// increasing Seq starting at 1, assigned under the queue lock, so a
// history is a linearization of the operations that actually took effect.
// Cancelled and rejected operations never appear in the history.
type Event[T any] struct {
	Seq   uint64
	Op    OpKind
	Value T   // item for OpEnqueue / OpDequeue
	Count int // remainder size for OpAbort
}

// Summary is the result of auditing a history with Verify.
type Summary struct {
	Enqueued  int
	Dequeued  int
	Released  int    // items handed to the Abort caller
	Remaining int    // items still owned by the queue at history end
	Winner    OpKind // OpClose, OpAbort, or OpNone if still open
}

// Verify audits a recorded history against the given capacity and checks:
//
//   - sequence numbers are contiguous and ordered;
//   - the buffer never exceeded capacity and never went negative;
//   - no enqueue committed after a shutdown, no dequeue after an abort;
//   - at most one shutdown won (first-wins semantics);
//   - the abort remainder count matches the number of live items;
//   - ownership conservation: enqueued == dequeued + released + remaining.
func Verify[T any](hist []Event[T], capacity int) (Summary, error) {
	s := Summary{Winner: OpNone}
	live := 0
	for i, e := range hist {
		if want := uint64(i + 1); e.Seq != want {
			return s, fmt.Errorf("mpqueue: history gap: event %d has seq %d, want %d", i, e.Seq, want)
		}
		switch e.Op {
		case OpEnqueue:
			if s.Winner != OpNone {
				return s, fmt.Errorf("mpqueue: enqueue after shutdown at seq %d", e.Seq)
			}
			live++
			if live > capacity {
				return s, fmt.Errorf("mpqueue: capacity exceeded at seq %d: %d live, capacity %d", e.Seq, live, capacity)
			}
			s.Enqueued++
		case OpDequeue:
			if s.Winner == OpAbort {
				return s, fmt.Errorf("mpqueue: dequeue after abort at seq %d", e.Seq)
			}
			live--
			if live < 0 {
				return s, fmt.Errorf("mpqueue: dequeue from empty queue at seq %d", e.Seq)
			}
			s.Dequeued++
		case OpClose:
			if s.Winner != OpNone {
				return s, fmt.Errorf("mpqueue: second shutdown %q at seq %d after %q", e.Op, e.Seq, s.Winner)
			}
			s.Winner = OpClose
		case OpAbort:
			if s.Winner != OpNone {
				return s, fmt.Errorf("mpqueue: second shutdown %q at seq %d after %q", e.Op, e.Seq, s.Winner)
			}
			if e.Count != live {
				return s, fmt.Errorf("mpqueue: abort released %d items but %d were live at seq %d", e.Count, live, e.Seq)
			}
			live = 0
			s.Released = e.Count
			s.Winner = OpAbort
		default:
			return s, fmt.Errorf("mpqueue: unknown op %d at seq %d", e.Op, e.Seq)
		}
	}
	s.Remaining = live
	if s.Enqueued != s.Dequeued+s.Released+s.Remaining {
		return s, fmt.Errorf("mpqueue: ownership leak: enqueued=%d dequeued=%d released=%d remaining=%d",
			s.Enqueued, s.Dequeued, s.Released, s.Remaining)
	}
	return s, nil
}

// Replay re-executes a verified history on a model queue and returns the
// items still buffered at the end and the items released by the abort, in
// FIFO order. It additionally checks FIFO order: every dequeued value must
// match the oldest buffered value, and the abort remainder must match the
// buffered contents at abort time.
func Replay[T any](hist []Event[T], capacity int) (remaining []T, released []T, err error) {
	if _, err := Verify(hist, capacity); err != nil {
		return nil, nil, err
	}
	var buf []T
	for _, e := range hist {
		switch e.Op {
		case OpEnqueue:
			buf = append(buf, e.Value)
		case OpDequeue:
			if !reflect.DeepEqual(buf[0], e.Value) {
				return nil, nil, fmt.Errorf("mpqueue: FIFO violated at seq %d: dequeued %v, oldest buffered %v", e.Seq, e.Value, buf[0])
			}
			buf = buf[1:]
		case OpAbort:
			released = append(released, buf...)
			buf = nil
		}
	}
	return append(remaining, buf...), released, nil
}
