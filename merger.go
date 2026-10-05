// Package assertmerge merges hierarchical assertion events that arrive out of
// order from parallel test runs into per-test trees, and emits the trees in
// test declaration order.
//
// Children may arrive before their parents; events are held in a bounded
// buffer until the owning test signals completion (KindDone / KindAbort) or
// the merger is closed. Nodes whose parents never arrived are attached to
// synthetic gap nodes instead of being dropped.
package assertmerge

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrBufferFull is returned by Submit when the bounded buffer is at
	// capacity. The event is not stored; callers may retry after trees are
	// emitted (which frees buffer space) or drop the event.
	ErrBufferFull = errors.New("assertmerge: buffer full")
	// ErrUnknownTest is returned for events referencing an undeclared test.
	ErrUnknownTest = errors.New("assertmerge: unknown test")
	// ErrTestFinished is returned for assertion/log events that arrive after
	// their test has already finished.
	ErrTestFinished = errors.New("assertmerge: test already finished")
	// ErrClosed is returned for any event submitted after Close.
	ErrClosed = errors.New("assertmerge: merger closed")
)

// Kind identifies the type of an Event.
type Kind int

const (
	// KindAssert is a hierarchical assertion node. ParentID references the
	// parent node; an empty ParentID marks a root.
	KindAssert Kind = iota
	// KindLog supplements an assertion node with a log message. ParentID is
	// the ID of the node the log attaches to; NodeID is the log's own unique
	// ID used for deduplication.
	KindLog
	// KindDone signals that no more events will arrive for the test. It is
	// idempotent.
	KindDone
	// KindAbort terminates the test early: the tree is emitted as-is, with
	// gap nodes for missing parents. It is idempotent.
	KindAbort
)

func (k Kind) String() string {
	switch k {
	case KindAssert:
		return "assert"
	case KindLog:
		return "log"
	case KindDone:
		return "done"
	case KindAbort:
		return "abort"
	}
	return "unknown"
}

// Event is a single assertion-stream event.
type Event struct {
	TestID   string // must be one of the declared tests
	NodeID   string // unique within the test (per Kind)
	ParentID string // parent node ID; for KindLog, the supplemented node ID
	Kind     Kind
	Name     string
	Passed   bool
	Message  string // failure detail for KindAssert, text for KindLog
}

// Node is one assertion in an emitted tree.
type Node struct {
	ID       string
	ParentID string
	Name     string
	Passed   bool
	Message  string
	// Gap is true for synthetic placeholders standing in for parents (or log
	// targets) that never arrived. Gap nodes keep their children reachable.
	Gap      bool
	Logs     []string
	Children []*Node
}

// Stats summarizes how an emitted tree was assembled.
type Stats struct {
	Nodes      int // real assertion nodes (gaps excluded)
	Logs       int // log supplements attached
	Gaps       int // synthetic gap nodes created for missing parents/targets
	Duplicates int // duplicate events dropped
}

// Tree is the merged result for one test.
type Tree struct {
	TestID  string
	Roots   []*Node // sorted by ID; children sorted by ID as well
	Aborted bool    // true if the test finished via KindAbort
	Stats   Stats
}

// Find returns the first node with the given ID (depth-first), or nil.
func (t *Tree) Find(id string) *Node {
	var walk func(n *Node) *Node
	walk = func(n *Node) *Node {
		if n.ID == id {
			return n
		}
		for _, c := range n.Children {
			if r := walk(c); r != nil {
				return r
			}
		}
		return nil
	}
	for _, r := range t.Roots {
		if n := walk(r); n != nil {
			return n
		}
	}
	return nil
}

type logEntry struct {
	id      string
	message string
}

type testState struct {
	nodes    map[string]*Node
	logs     map[string][]logEntry // keyed by target node ID
	seenLogs map[string]struct{}   // dedup by log ID
	done     bool
	aborted  bool
	emitted  bool
	buffered int // events currently occupying the bounded buffer
	dups     int
	logCount int
}

func newTestState() *testState {
	return &testState{
		nodes:    make(map[string]*Node),
		logs:     make(map[string][]logEntry),
		seenLogs: make(map[string]struct{}),
	}
}

// Merger assembles out-of-order assertion events into per-test trees and
// emits them in test declaration order. It is safe for concurrent use.
type Merger struct {
	mu       sync.Mutex
	order    []string
	states   map[string]*testState
	nextEmit int
	max      int // buffer capacity in events; <= 0 means unbounded
	pending  int // events currently buffered across all tests
	ready    []*Tree
	closed   bool
}

// New creates a Merger for the given tests, listed in declaration order.
// maxBuffered bounds the total number of buffered (not yet emitted) events;
// pass <= 0 for an unbounded buffer.
func New(order []string, maxBuffered int) *Merger {
	m := &Merger{
		order:  append([]string(nil), order...),
		states: make(map[string]*testState, len(order)),
		max:    maxBuffered,
	}
	for _, id := range order {
		if _, ok := m.states[id]; !ok {
			m.states[id] = newTestState()
		}
	}
	return m
}

// Submit ingests one event. Duplicate assertion/log events (same TestID,
// Kind and NodeID) are dropped and counted in Stats.Duplicates. It returns
// ErrBufferFull when the bounded buffer is full, ErrUnknownTest for
// undeclared tests, ErrTestFinished for events arriving after the test
// finished, and ErrClosed after Close.
func (m *Merger) Submit(ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	st, ok := m.states[ev.TestID]
	if !ok {
		return ErrUnknownTest
	}
	switch ev.Kind {
	case KindDone:
		st.done = true
		m.advanceLocked()
		return nil
	case KindAbort:
		st.done = true
		st.aborted = true
		m.advanceLocked()
		return nil
	}
	if st.done {
		return ErrTestFinished
	}
	switch ev.Kind {
	case KindAssert:
		if _, dup := st.nodes[ev.NodeID]; dup {
			st.dups++
			return nil
		}
		if err := m.growLocked(st); err != nil {
			return err
		}
		st.nodes[ev.NodeID] = &Node{
			ID:       ev.NodeID,
			ParentID: ev.ParentID,
			Name:     ev.Name,
			Passed:   ev.Passed,
			Message:  ev.Message,
		}
	case KindLog:
		if _, dup := st.seenLogs[ev.NodeID]; dup {
			st.dups++
			return nil
		}
		if err := m.growLocked(st); err != nil {
			return err
		}
		st.seenLogs[ev.NodeID] = struct{}{}
		st.logs[ev.ParentID] = append(st.logs[ev.ParentID], logEntry{id: ev.NodeID, message: ev.Message})
		st.logCount++
	}
	return nil
}

// Abort terminates a test early, as if KindAbort had been submitted.
func (m *Merger) Abort(testID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	st, ok := m.states[testID]
	if !ok {
		return ErrUnknownTest
	}
	if !st.done {
		st.done = true
		st.aborted = true
		m.advanceLocked()
	}
	return nil
}

// Ready pops the trees that have become emittable since the last call, in
// test declaration order.
func (m *Merger) Ready() []*Tree {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.drainReadyLocked()
}

// Pending returns the number of events currently held in the buffer.
func (m *Merger) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending
}

// Close terminates the merger early: every unfinished test is finished and
// all remaining trees are emitted in declaration order, with gap nodes for
// parents that never arrived. It returns the remaining trees (equivalent to
// calling Ready afterwards).
func (m *Merger) Close() []*Tree {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		for _, id := range m.order {
			m.states[id].done = true
		}
		m.advanceLocked()
	}
	return m.drainReadyLocked()
}

func (m *Merger) growLocked(st *testState) error {
	if m.max > 0 && m.pending+1 > m.max {
		return ErrBufferFull
	}
	m.pending++
	st.buffered++
	return nil
}

// advanceLocked emits trees in declaration order for as long as the next
// declared test is finished.
func (m *Merger) advanceLocked() {
	for m.nextEmit < len(m.order) {
		st := m.states[m.order[m.nextEmit]]
		if !st.done || st.emitted {
			if st.emitted {
				m.nextEmit++
				continue
			}
			break
		}
		m.ready = append(m.ready, buildTree(m.order[m.nextEmit], st))
		m.pending -= st.buffered
		st.emitted = true
		st.nodes = nil
		st.logs = nil
		st.seenLogs = nil
		m.nextEmit++
	}
}

func (m *Merger) drainReadyLocked() []*Tree {
	out := m.ready
	m.ready = nil
	return out
}

// buildTree links buffered nodes into a tree. Nodes whose parent never
// arrived are attached to synthetic gap nodes (one per missing ID) promoted
// to roots, so no submitted child is ever dropped. Logs attach to their
// target node, or to a gap node if the target never arrived.
func buildTree(testID string, st *testState) *Tree {
	all := make(map[string]*Node, len(st.nodes))
	for id, n := range st.nodes {
		all[id] = n
	}
	gaps := 0
	gap := func(id string) *Node {
		if n, ok := all[id]; ok {
			return n
		}
		n := &Node{ID: id, Gap: true}
		all[id] = n
		gaps++
		return n
	}
	for _, n := range st.nodes {
		if n.ParentID != "" {
			gap(n.ParentID)
		}
	}
	for target := range st.logs {
		gap(target)
	}
	for target, entries := range st.logs {
		sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
		n := all[target]
		for _, e := range entries {
			n.Logs = append(n.Logs, e.message)
		}
	}
	var roots []*Node
	for _, n := range all {
		p, ok := all[n.ParentID]
		if n.ParentID == "" || !ok || p == n {
			roots = append(roots, n)
			continue
		}
		p.Children = append(p.Children, n)
	}
	byID := func(s []*Node) {
		sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
	}
	byID(roots)
	for _, n := range all {
		byID(n.Children)
	}
	return &Tree{
		TestID:  testID,
		Roots:   roots,
		Aborted: st.aborted,
		Stats: Stats{
			Nodes:      len(st.nodes),
			Logs:       st.logCount,
			Gaps:       gaps,
			Duplicates: st.dups,
		},
	}
}
