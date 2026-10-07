package boundedq

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// CheckLinearizable reports whether the given history of completed queue
// operations admits a legal sequential execution of a bounded queue with
// the given capacity, respecting real-time order: if op A returned before
// op B was invoked, A must precede B in the sequential order.
//
// The sequential specification is:
//   - Enqueue(v) succeeds iff the queue is not closed and not full, and
//     appends v; it returns ErrClosed iff the queue is closed.
//   - Dequeue() succeeds iff the queue is non-empty, and removes and
//     returns the oldest element; it returns ErrClosed iff the queue is
//     closed and empty.
//   - Close() always succeeds and closes the queue (idempotently).
//
// The search is exponential and intended for small histories (at most 64
// operations; practical use is far smaller). It returns a witness ordering
// (indices into ops) when the history is linearizable.
func CheckLinearizable(capacity int, ops []Op) (order []int, ok bool) {
	if capacity <= 0 {
		panic("boundedq: capacity must be positive")
	}
	if len(ops) > 64 {
		panic("boundedq: history too large for bitmask search")
	}
	for i, op := range ops {
		if op.Call >= op.Ret {
			panic(fmt.Sprintf("boundedq: op %d has Call >= Ret", i))
		}
	}

	// depends[i] is the bitmask of ops that must be placed before op i
	// because they returned before op i was invoked.
	depends := make([]uint64, len(ops))
	for i := range ops {
		for j := range ops {
			if i != j && ops[j].Ret < ops[i].Call {
				depends[i] |= 1 << uint(j)
			}
		}
	}

	s := &search{
		capacity: capacity,
		ops:      ops,
		depends:  depends,
		memo:     make(map[memoKey]bool),
	}
	if s.run(specState{}, 0) {
		// The witness is appended deepest-first during backtracking.
		for i, j := 0, len(s.witness)-1; i < j; i, j = i+1, j-1 {
			s.witness[i], s.witness[j] = s.witness[j], s.witness[i]
		}
		return s.witness, true
	}
	return nil, false
}

// specState is the state of the sequential queue specification.
type specState struct {
	items  []any // FIFO, items[0] is the oldest
	closed bool
}

// key canonically encodes the state for memoization. %#v rendering is
// sufficient for the small, comparable values used in test histories.
func (s specState) key() string {
	var b strings.Builder
	if s.closed {
		b.WriteString("closed|")
	} else {
		b.WriteString("open|")
	}
	for _, v := range s.items {
		fmt.Fprintf(&b, "%#v;", v)
	}
	return b.String()
}

// step applies op to s, returning the successor state and whether the
// operation's recorded result is legal in s.
func (s specState) step(op Op, capacity int) (specState, bool) {
	closed := errors.Is(op.Err, ErrClosed)
	switch op.Kind {
	case EnqueueOp:
		if closed {
			// Rejected enqueue: legal iff already closed; state unchanged.
			return s, s.closed
		}
		if op.Err != nil || s.closed || len(s.items) == capacity {
			return s, false
		}
		items := make([]any, len(s.items)+1)
		copy(items, s.items)
		items[len(s.items)] = op.Value
		return specState{items: items, closed: false}, true
	case DequeueOp:
		if closed {
			// Terminated dequeue: legal iff closed and drained.
			return s, s.closed && len(s.items) == 0
		}
		if op.Err != nil || len(s.items) == 0 {
			return s, false
		}
		if !reflect.DeepEqual(s.items[0], op.Value) {
			return s, false
		}
		items := make([]any, len(s.items)-1)
		copy(items, s.items[1:])
		return specState{items: items, closed: s.closed}, true
	case CloseOp:
		if op.Err != nil {
			return s, false
		}
		return specState{items: s.items, closed: true}, true
	}
	return s, false
}

type memoKey struct {
	state string
	mask  uint64 // ops already placed
}

type search struct {
	capacity int
	ops      []Op
	depends  []uint64
	memo     map[memoKey]bool
	witness  []int
}

// run attempts to extend the execution (state s, placed ops mask) to a
// full legal history.
func (sr *search) run(s specState, mask uint64) bool {
	if mask == 1<<uint(len(sr.ops))-1 {
		return true
	}
	key := memoKey{state: s.key(), mask: mask}
	if sr.memo[key] {
		return false
	}
	for i, op := range sr.ops {
		bit := uint64(1) << uint(i)
		if mask&bit != 0 {
			continue
		}
		// Op i is enabled only when every op that real-time order
		// forces before it has already been placed.
		if sr.depends[i]&^mask != 0 {
			continue
		}
		next, ok := s.step(op, sr.capacity)
		if !ok {
			continue
		}
		if sr.run(next, mask|bit) {
			sr.witness = append(sr.witness, i)
			return true
		}
	}
	sr.memo[key] = false
	return false
}
