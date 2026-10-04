// Package assertmerge merges hierarchical assertion events that arrive
// out of order (children may arrive before their parents) into trees,
// and emits the trees in test declaration order.
//
// The merger uses a bounded input buffer: Submit blocks when the buffer
// is full (backpressure) and TrySubmit reports ErrBufferFull.
//
// A tree is streamed to Output as soon as its test is finished, fully
// resolved (no missing parents, no dangling logs) and not failed.
// Failed tests are held until Close so that supplementary logs submitted
// after the failure are still merged. Close (or Abort) flushes every
// remaining tree; nodes whose parents never arrived are attached under
// synthetic Gap nodes instead of being dropped.
package assertmerge

import "errors"

var (
	// ErrClosed is returned by Submit/TrySubmit/Finish after Close or Abort.
	ErrClosed = errors.New("assertmerge: merger is closed")
	// ErrBufferFull is returned by TrySubmit when the bounded buffer is full.
	ErrBufferFull = errors.New("assertmerge: buffer is full")
)

// Kind discriminates assertion events from log events.
type Kind int

const (
	// KindAssertion is a hierarchical assertion node.
	KindAssertion Kind = iota
	// KindLog attaches a log line to an assertion node (ParentID is the target).
	KindLog
)

// Status is the outcome of an assertion.
type Status int

const (
	StatusNone Status = iota
	StatusPass
	StatusFail
)

// Event is a single assertion or log record arriving from a parallel runner.
// IDs must be unique within (TestID, Kind). Duplicate events (same TestID,
// Kind and ID) are idempotently discarded and counted in Stats.Duplicates.
type Event struct {
	TestID   string // test the event belongs to
	ID       string // unique event id within the test (per kind)
	ParentID string // assertion: parent assertion id ("" = root); log: target assertion id
	Kind     Kind   // KindAssertion or KindLog
	Label    string // assertion label
	Status   Status // assertion outcome
	Message  string // log text (KindLog only)
}

// Node is one assertion in an emitted tree.
type Node struct {
	ID       string
	Label    string
	Status   Status
	Gap      bool     // true for synthetic placeholders standing in for missing parents
	Logs     []string // log lines attached to this assertion, in arrival order
	Children []*Node  // in arrival order

	parentID string
	seq      int
}

// Tree is the merged result for one test.
type Tree struct {
	TestID   string
	Root     *Node // nil when the test produced no events
	Complete bool  // false when the test was not finished or gaps were inserted
	Failed   bool  // true when any assertion in the tree failed
}

// Stats reports counters gathered while merging. Call Stats only after Wait
// has returned (or after the Output channel was closed).
type Stats struct {
	Received    int // accepted events (excluding duplicates and late drops)
	Duplicates  int // discarded duplicate events
	DroppedLate int // events for tests whose tree was already emitted
	Gaps        int // synthetic gap nodes inserted at flush
}
