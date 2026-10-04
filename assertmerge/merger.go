package assertmerge

import (
	"sort"
	"sync"
)

type intake struct {
	ev     Event
	finish bool // marks ev.TestID as finished (no more assertions expected)
}

type pendingLog struct {
	targetID string
	message  string
	seq      int
}

type testState struct {
	id       string
	nodes    map[string]*Node // assertion id -> node (Node is the build template)
	arrival  []*Node          // assertions in arrival order
	logs     map[string]pendingLog
	finished bool
	failed   bool
	emitted  bool
}

// Merger collects out-of-order assertion events and emits trees in test
// declaration order. It is safe for concurrent use.
type Merger struct {
	in      chan intake
	out     chan Tree
	abortCh chan struct{}
	done    chan struct{}

	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	abortOnce sync.Once

	st *mergeState
}

// NewMerger creates a merger whose input buffer holds at most capacity
// events (capacity < 0 is treated as 0, i.e. fully synchronous hand-off).
// declared lists test ids in declaration order; trees are always emitted in
// that order. Events for undeclared tests are accepted and emitted after the
// declared ones, in first-seen order.
func NewMerger(capacity int, declared []string) *Merger {
	if capacity < 0 {
		capacity = 0
	}
	m := &Merger{
		in:      make(chan intake, capacity),
		out:     make(chan Tree, 8),
		abortCh: make(chan struct{}),
		done:    make(chan struct{}),
		st:      newMergeState(declared),
	}
	go m.loop()
	return m
}

// Submit enqueues an event, blocking while the bounded buffer is full.
func (m *Merger) Submit(ev Event) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	m.in <- intake{ev: ev}
	return nil
}

// TrySubmit enqueues an event without blocking; it returns ErrBufferFull
// when the bounded buffer is full.
func (m *Merger) TrySubmit(ev Event) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	select {
	case m.in <- intake{ev: ev}:
		return nil
	default:
		return ErrBufferFull
	}
}

// Finish marks a test as finished: no more assertion events are expected for
// it. Supplementary log events may still arrive afterwards and will be merged.
func (m *Merger) Finish(testID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	m.in <- intake{ev: Event{TestID: testID}, finish: true}
	return nil
}

// Close stops accepting events, drains the buffer, flushes every pending
// tree (inserting gap nodes for missing parents) and closes the Output
// channel. It returns immediately; use Wait to block until the flush is done.
// The caller must keep consuming Output to let the flush proceed.
func (m *Merger) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		close(m.in)
		m.mu.Unlock()
	})
}

// Abort stops accepting events and flushes immediately, discarding events
// still sitting in the buffer. Pending trees are emitted with gap nodes for
// missing parents.
func (m *Merger) Abort() {
	m.abortOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		close(m.abortCh)
		m.mu.Unlock()
	})
}

// Wait blocks until the merger loop has flushed and closed Output.
func (m *Merger) Wait() { <-m.done }

// Output returns the channel of emitted trees, in test declaration order.
func (m *Merger) Output() <-chan Tree { return m.out }

// Stats returns the merge counters. It must be called only after Wait has
// returned or the Output channel was closed.
func (m *Merger) Stats() Stats { return m.st.stats }

func (m *Merger) loop() {
	defer close(m.done)
	defer close(m.out)
	for {
		select {
		case <-m.abortCh:
			m.st.flush(m.out)
			return
		default:
		}
		select {
		case it, ok := <-m.in:
			if !ok {
				m.st.flush(m.out)
				return
			}
			if it.finish {
				m.st.finishTest(it.ev.TestID)
			} else {
				m.st.addEvent(it.ev)
			}
			m.st.emitReady(m.out)
		case <-m.abortCh:
			m.st.flush(m.out)
			return
		}
	}
}

type mergeState struct {
	order    []string // test ids in emission order
	known    map[string]bool
	tests    map[string]*testState
	nextEmit int
	seq      int
	stats    Stats
}

func newMergeState(declared []string) *mergeState {
	s := &mergeState{
		known: make(map[string]bool),
		tests: make(map[string]*testState),
	}
	for _, id := range declared {
		if !s.known[id] {
			s.known[id] = true
			s.order = append(s.order, id)
		}
	}
	return s
}

func (s *mergeState) test(id string) *testState {
	ts, ok := s.tests[id]
	if !ok {
		ts = &testState{
			id:    id,
			nodes: make(map[string]*Node),
			logs:  make(map[string]pendingLog),
		}
		s.tests[id] = ts
	}
	if !s.known[id] {
		s.known[id] = true
		s.order = append(s.order, id)
	}
	return ts
}

func (s *mergeState) addEvent(ev Event) {
	ts := s.test(ev.TestID)
	if ts.emitted {
		s.stats.DroppedLate++
		return
	}
	if ev.Kind == KindLog {
		if _, dup := ts.logs[ev.ID]; dup {
			s.stats.Duplicates++
			return
		}
		s.seq++
		ts.logs[ev.ID] = pendingLog{targetID: ev.ParentID, message: ev.Message, seq: s.seq}
		s.stats.Received++
		return
	}
	if _, dup := ts.nodes[ev.ID]; dup {
		s.stats.Duplicates++
		return
	}
	s.seq++
	n := &Node{
		ID:       ev.ID,
		Label:    ev.Label,
		Status:   ev.Status,
		parentID: ev.ParentID,
		seq:      s.seq,
	}
	ts.nodes[ev.ID] = n
	ts.arrival = append(ts.arrival, n)
	if ev.Status == StatusFail {
		ts.failed = true
	}
	s.stats.Received++
}

func (s *mergeState) finishTest(id string) {
	s.test(id).finished = true
}

// streamable reports whether a test tree can be emitted before the final
// flush: finished, not failed (failed tests wait for supplementary logs),
// fully resolved and rooted at exactly one node.
func (ts *testState) streamable() bool {
	if !ts.finished || ts.failed || ts.emitted {
		return false
	}
	roots := 0
	for _, n := range ts.arrival {
		if n.parentID == "" {
			roots++
			continue
		}
		if _, ok := ts.nodes[n.parentID]; !ok {
			return false
		}
	}
	if roots != 1 {
		return false
	}
	for _, lg := range ts.logs {
		if _, ok := ts.nodes[lg.targetID]; !ok {
			return false
		}
	}
	return true
}

func (s *mergeState) emitReady(out chan<- Tree) {
	for s.nextEmit < len(s.order) {
		ts := s.tests[s.order[s.nextEmit]]
		if ts == nil || !ts.streamable() {
			return
		}
		tr := ts.buildTree(false, &s.stats)
		tr.Complete = true
		ts.emitted = true
		out <- *tr
		s.nextEmit++
	}
}

func (s *mergeState) flush(out chan<- Tree) {
	for ; s.nextEmit < len(s.order); s.nextEmit++ {
		id := s.order[s.nextEmit]
		ts := s.tests[id]
		if ts == nil {
			out <- Tree{TestID: id}
			continue
		}
		if ts.emitted {
			continue
		}
		tr := ts.buildTree(true, &s.stats)
		ts.emitted = true
		out <- *tr
	}
}

// buildTree assembles the current state into a tree. With withGaps, missing
// parents (and missing log targets) are replaced by synthetic gap nodes so no
// arrived event is dropped.
func (ts *testState) buildTree(withGaps bool, stats *Stats) *Tree {
	tr := &Tree{TestID: ts.id, Failed: ts.failed}
	if len(ts.arrival) == 0 && len(ts.logs) == 0 {
		return tr
	}

	nodes := make(map[string]*Node, len(ts.arrival))
	arrival := make([]*Node, 0, len(ts.arrival))
	for _, src := range ts.arrival {
		cp := *src
		cp.Logs = nil
		cp.Children = nil
		nodes[cp.ID] = &cp
		arrival = append(arrival, &cp)
	}
	sort.Slice(arrival, func(i, j int) bool { return arrival[i].seq < arrival[j].seq })

	gaps := 0
	var gapList []*Node
	gapFor := func(missingID, label string) *Node {
		if g, ok := nodes[missingID]; ok {
			return g
		}
		g := &Node{
			ID:    "gap:" + missingID,
			Label: label,
			Gap:   true,
			seq:   int(^uint(0) >> 1), // sort after real siblings
		}
		nodes[missingID] = g
		gapList = append(gapList, g)
		gaps++
		return g
	}

	var roots []*Node
	for _, n := range arrival {
		if n.parentID == "" {
			roots = append(roots, n)
			continue
		}
		p, ok := nodes[n.parentID]
		if !ok {
			if !withGaps {
				continue // unreachable: streamable() guarantees resolution
			}
			p = gapFor(n.parentID, "missing parent: "+n.parentID)
		}
		p.Children = append(p.Children, n)
	}

	logs := make([]pendingLog, 0, len(ts.logs))
	for _, lg := range ts.logs {
		logs = append(logs, lg)
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].seq < logs[j].seq })
	for _, lg := range logs {
		tgt, ok := nodes[lg.targetID]
		if !ok {
			if !withGaps {
				continue
			}
			tgt = gapFor(lg.targetID, "missing log target: "+lg.targetID)
		}
		tgt.Logs = append(tgt.Logs, lg.message)
	}

	switch {
	case len(roots) == 1 && len(gapList) == 0:
		tr.Root = roots[0]
	case len(roots) == 1:
		root := roots[0]
		for _, g := range gapList {
			root.Children = append(root.Children, g)
		}
		tr.Root = root
	default:
		// No root (or several): gather everything under a synthetic gap root.
		root := &Node{ID: "gap:root", Label: "missing root", Gap: true, seq: int(^uint(0) >> 1)}
		root.Children = append(root.Children, roots...)
		root.Children = append(root.Children, gapList...)
		tr.Root = root
		gaps++
	}

	sortChildren(tr.Root)
	if stats != nil {
		stats.Gaps += gaps
	}
	tr.Complete = ts.finished && gaps == 0
	return tr
}

func sortChildren(n *Node) {
	if n == nil {
		return
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		return n.Children[i].seq < n.Children[j].seq
	})
	for _, c := range n.Children {
		sortChildren(c)
	}
}
