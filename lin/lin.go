// Package lin checks whether a recorded history of concurrent queue
// operations is linearizable with respect to the sequential specification
// of a bounded, closeable FIFO queue.
//
// A history is a set of operations, each with an invocation timestamp
// (Call) and a response timestamp (Ret) from a single monotonic clock.
// The checker searches, by backtracking with memoization, for a serial
// order of the operations that
//   - respects real-time order: if op A returned before op B was invoked
//     (A.Ret < B.Call), A must precede B, and
//   - is accepted by the sequential model of the queue.
//
// It is intended for small histories (at most 62 operations), as produced
// by focused concurrency tests.
package lin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Kind identifies a queue operation.
type Kind uint8

const (
	Enqueue Kind = iota
	Dequeue
	Close
)

func (k Kind) String() string {
	switch k {
	case Enqueue:
		return "Enqueue"
	case Dequeue:
		return "Dequeue"
	case Close:
		return "Close"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Status is the recorded outcome of an operation.
type Status uint8

const (
	// OK means the operation took effect (Enqueue committed, Dequeue
	// returned an item, Close succeeded).
	OK Status = iota
	// Closed means the operation failed with queue.ErrClosed.
	Closed
	// Canceled means the wait was cancelled (context done). Per the
	// queue's contract a cancelled operation has no observable effect,
	// so the model treats it as a no-op within its interval.
	Canceled
)

func (s Status) String() string {
	switch s {
	case OK:
		return "OK"
	case Closed:
		return "Closed"
	case Canceled:
		return "Canceled"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// Op is one recorded operation of a history.
type Op struct {
	Kind   Kind
	Arg    int    // Enqueue: the value offered
	Val    int    // Dequeue with Status OK: the value returned
	Status Status // recorded outcome
	Call   int64  // invocation timestamp (monotonic clock)
	Ret    int64  // response timestamp (monotonic clock), Call < Ret
}

// MaxOps is the largest history Check accepts.
const MaxOps = 62

// ErrTooManyOps is returned by Check for histories larger than MaxOps.
var ErrTooManyOps = errors.New("lin: history too large")

// Check reports whether the history is linearizable for a queue of the
// given capacity. If ok is true, order holds one legal serial order as
// indices into ops.
func Check(capacity int, ops []Op) (order []int, ok bool, err error) {
	if len(ops) > MaxOps {
		return nil, false, ErrTooManyOps
	}
	if capacity < 1 {
		return nil, false, errors.New("lin: capacity must be positive")
	}
	n := len(ops)
	// pre[i] is the bitmask of ops that real-time order forces before i.
	pre := make([]uint64, n)
	for i := range ops {
		for j := range ops {
			if i != j && ops[j].Ret < ops[i].Call {
				pre[i] |= 1 << uint(j)
			}
		}
	}
	full := uint64(1)<<uint(n) - 1

	type memoKey struct {
		done   uint64
		closed bool
		buf    string
	}
	failed := make(map[memoKey]struct{})

	var dfs func(done uint64, closed bool, buf []int) bool
	dfs = func(done uint64, closed bool, buf []int) bool {
		if done == full {
			return true
		}
		key := memoKey{done: done, closed: closed, buf: encodeBuf(buf)}
		if _, ok := failed[key]; ok {
			return false
		}
		for i := 0; i < n; i++ {
			bit := uint64(1) << uint(i)
			if done&bit != 0 || pre[i]&^done != 0 {
				continue
			}
			nextClosed, nextBuf, ok := step(capacity, closed, buf, ops[i])
			if !ok {
				continue
			}
			if dfs(done|bit, nextClosed, nextBuf) {
				order = append(order, i)
				return true
			}
		}
		failed[key] = struct{}{}
		return false
	}

	if !dfs(0, false, nil) {
		return nil, false, nil
	}
	// order was appended on unwind, so it is reversed.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, true, nil
}

// step applies one operation to the sequential model. It returns the new
// state and whether the recorded outcome is legal in the current state.
// buf is never mutated; successors share or copy as needed.
func step(capacity int, closed bool, buf []int, op Op) (bool, []int, bool) {
	switch op.Kind {
	case Enqueue:
		switch op.Status {
		case OK:
			// Committed before any close took effect, with room.
			if closed || len(buf) >= capacity {
				return closed, buf, false
			}
			next := make([]int, len(buf)+1)
			copy(next, buf)
			next[len(buf)] = op.Arg
			return closed, next, true
		case Closed:
			// Rejected by the commit-point rule: close already in effect.
			if !closed {
				return closed, buf, false
			}
			return closed, buf, true
		case Canceled:
			return closed, buf, true
		}
	case Dequeue:
		switch op.Status {
		case OK:
			if len(buf) == 0 || buf[0] != op.Val {
				return closed, buf, false
			}
			return closed, buf[1:], true
		case Closed:
			// Termination: closed and fully drained.
			if !closed || len(buf) != 0 {
				return closed, buf, false
			}
			return closed, buf, true
		case Canceled:
			return closed, buf, true
		}
	case Close:
		if op.Status != OK {
			return closed, buf, false
		}
		return true, buf, true
	}
	return closed, buf, false
}

func encodeBuf(buf []int) string {
	b := make([]byte, 8*len(buf))
	for i, v := range buf {
		binary.LittleEndian.PutUint64(b[8*i:], uint64(v))
	}
	return string(b)
}

// Recorder captures a concurrent history. The zero value is ready to use.
// Timestamps come from one atomic counter, so Call/Ret preserve real-time
// order across goroutines.
type Recorder struct {
	clock atomic.Int64
	mu    sync.Mutex
	ops   []Op
}

// Do records one operation: it timestamps the invocation, runs fn (which
// must perform the actual queue operation and return its outcome), and
// timestamps the response.
func (r *Recorder) Do(kind Kind, arg int, fn func() (val int, st Status)) {
	call := r.clock.Add(1)
	val, st := fn()
	ret := r.clock.Add(1)
	r.mu.Lock()
	r.ops = append(r.ops, Op{Kind: kind, Arg: arg, Val: val, Status: st, Call: call, Ret: ret})
	r.mu.Unlock()
}

// Ops returns the recorded history in completion order.
func (r *Recorder) Ops() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Op(nil), r.ops...)
}
