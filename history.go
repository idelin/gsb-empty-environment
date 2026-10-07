package boundedq

import (
	"sync"
	"sync/atomic"
)

// OpKind identifies the queue operation an Op records.
type OpKind int

const (
	// EnqueueOp records a completed Enqueue call. Value is the enqueued
	// element; Err is nil on success or ErrClosed if the enqueue was
	// rejected because the queue was closed.
	EnqueueOp OpKind = iota
	// DequeueOp records a completed Dequeue call. Value is the dequeued
	// element on success; Err is nil on success or ErrClosed if the queue
	// was closed and drained.
	DequeueOp
	// CloseOp records a Close call. Err is nil.
	CloseOp
)

// Op is one completed operation in a history. Call and Ret are timestamps
// from a common, strictly increasing clock (see Recorder): Call is taken
// just before invoking the operation and Ret just after it returns.
// Histories must contain only completed operations; operations that failed
// with a context error cannot be modelled and must be omitted.
type Op struct {
	Kind  OpKind
	Value any   // EnqueueOp input / DequeueOp output
	Err   error // nil, or ErrClosed
	Call  int64
	Ret   int64
}

// Recorder collects a concurrent history of queue operations. The zero
// value is ready to use and safe for concurrent use.
type Recorder struct {
	clock atomic.Int64
	mu    sync.Mutex
	ops   []Op
}

// Begin returns the invocation timestamp for an operation about to start.
func (r *Recorder) Begin() int64 { return r.clock.Add(1) }

// End records an operation that began with the timestamp returned by
// Begin and has just returned (value, err).
func (r *Recorder) End(call int64, kind OpKind, value any, err error) {
	ret := r.clock.Add(1)
	r.mu.Lock()
	r.ops = append(r.ops, Op{Kind: kind, Value: value, Err: err, Call: call, Ret: ret})
	r.mu.Unlock()
}

// Ops returns the recorded operations in recording order.
func (r *Recorder) Ops() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Op(nil), r.ops...)
}
