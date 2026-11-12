package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// frame is one entry on a thread's identity stack. Base frames are installed
// by SetIdentity; borrow frames are pushed by Borrow and must be popped by the
// matching Return before the base frame is touched.
type frame struct {
	identity Identity
	tag      string
	borrow   bool
	chain    []Provenance
}

// ThreadEvent is one entry in a thread's lifecycle sequence, exposed so tests
// can assert on thread-reuse ordering.
type ThreadEvent struct {
	Seq       uint64
	Kind      EventKind
	RequestID string
	Detail    string
}

// threadState is the per-thread bookkeeping held by a Monitor.
type threadState struct {
	id      string
	stack   []frame
	current string
	log     []ThreadEvent
}

// Monitor ingests instrumentation events from a Go host and tracks the identity
// frame stack of every observed thread. It is safe for concurrent use.
type Monitor struct {
	mu       sync.Mutex
	seq      uint64
	threads  map[string]*threadState
	findings []Finding
}

// New creates an empty Monitor.
func New() *Monitor {
	return &Monitor{threads: make(map[string]*threadState)}
}

// Handle ingests one event. Malformed events return an error; policy violations
// do not interrupt processing and are collected as findings instead.
func (m *Monitor) Handle(ev Event) error {
	if err := validate(ev); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	seq := m.nextSeq()
	ts := m.threadOf(ev.ThreadID)

	switch ev.Kind {
	case EventSwitch:
		m.handleSwitch(ev, ts, seq)
	case EventSetIdentity:
		m.handleSetIdentity(ev, ts, seq)
	case EventBorrow:
		m.handleBorrow(ev, ts, seq)
	case EventReturn:
		m.handleReturn(ev, ts, seq)
	case EventSpawn:
		m.handleSpawn(ev, seq)
	case EventExit:
		m.handleExit(ev, ts, seq)
	default:
		return fmt.Errorf("netsec: event kind %s must not be ingested directly", ev.Kind)
	}
	return nil
}

func validate(ev Event) error {
	if ev.ThreadID == "" {
		return errors.New("netsec: event missing ThreadID")
	}
	switch ev.Kind {
	case EventSwitch:
	case EventSetIdentity, EventBorrow:
		if ev.Identity.RequestID == "" {
			return fmt.Errorf("netsec: %s event missing Identity.RequestID", ev.Kind)
		}
	case EventReturn, EventExit:
	case EventSpawn:
		if ev.ChildID == "" {
			return errors.New("netsec: spawn event missing ChildID")
		}
	default:
		return fmt.Errorf("netsec: unsupported event kind %d", ev.Kind)
	}
	return nil
}

func (m *Monitor) nextSeq() uint64 {
	m.seq++
	return m.seq
}

func (m *Monitor) threadOf(id string) *threadState {
	ts, ok := m.threads[id]
	if !ok {
		ts = &threadState{id: id}
		m.threads[id] = ts
	}
	return ts
}

func (m *Monitor) appendLog(ts *threadState, seq uint64, kind EventKind, requestID, detail string) {
	ts.log = append(ts.log, ThreadEvent{Seq: seq, Kind: kind, RequestID: requestID, Detail: detail})
}

func provenance(seq uint64, ev Event, id Identity, detail string) Provenance {
	return Provenance{
		Seq:       seq,
		Kind:      ev.Kind,
		ThreadID:  ev.ThreadID,
		RequestID: id.RequestID,
		Principal: id.Principal,
		Tag:       ev.Tag,
		Detail:    detail,
	}
}

func extendChain(base []Provenance, hop Provenance) []Provenance {
	out := make([]Provenance, 0, len(base)+1)
	out = append(out, base...)
	out = append(out, hop)
	return out
}

// report records a finding, copying the chain so later stack mutation can never
// alter evidence already captured.
func (m *Monitor) report(kind FindingKind, threadID, requestID, msg string, chain []Provenance, seq uint64) {
	cp := make([]Provenance, len(chain))
	copy(cp, chain)
	m.findings = append(m.findings, Finding{
		Kind:      kind,
		Seq:       seq,
		ThreadID:  threadID,
		RequestID: requestID,
		Message:   msg,
		Chain:     cp,
	})
}

func (m *Monitor) handleSwitch(ev Event, ts *threadState, seq uint64) {
	m.appendLog(ts, seq, EventSwitch, ev.RequestID, "thread runnable")
	if ev.RequestID != "" {
		ts.current = ev.RequestID
	}
	if len(ts.stack) == 0 {
		return
	}
	top := ts.stack[len(ts.stack)-1]
	if top.identity.RequestID != ev.RequestID {
		m.report(
			StaleIdentityRead,
			ts.id,
			ev.RequestID,
			fmt.Sprintf("thread %s reused by request %q while still holding identity from request %q at depth %d",
				ts.id, ev.RequestID, top.identity.RequestID, len(ts.stack)),
			top.chain,
			seq,
		)
	}
}

func (m *Monitor) handleSetIdentity(ev Event, ts *threadState, seq uint64) {
	if len(ts.stack) > 0 {
		top := ts.stack[len(ts.stack)-1]
		if top.identity.RequestID != ev.Identity.RequestID {
			m.report(
				StaleIdentityRead,
				ts.id,
				ev.Identity.RequestID,
				fmt.Sprintf("set_identity for request %q overwrites un-cleared identity from request %q at depth %d",
					ev.Identity.RequestID, top.identity.RequestID, len(ts.stack)),
				top.chain,
				seq,
			)
		}
		ts.stack = ts.stack[:0]
	}
	hop := provenance(seq, ev, ev.Identity, "base identity")
	ts.stack = append(ts.stack, frame{
		identity: ev.Identity,
		chain:    []Provenance{hop},
	})
}

func (m *Monitor) handleBorrow(ev Event, ts *threadState, seq uint64) {
	hop := provenance(seq, ev, ev.Identity, "borrowed identity")
	chain := []Provenance{hop}
	if len(ts.stack) > 0 {
		chain = extendChain(ts.stack[len(ts.stack)-1].chain, hop)
	}
	ts.stack = append(ts.stack, frame{
		identity: ev.Identity,
		tag:      ev.Tag,
		borrow:   true,
		chain:    chain,
	})
}

func (m *Monitor) handleReturn(ev Event, ts *threadState, seq uint64) {
	if len(ts.stack) == 0 {
		m.report(ReentrancyViolation, ts.id, ts.current,
			fmt.Sprintf("return %q on thread %s with empty stack", ev.Tag, ts.id), nil, seq)
		return
	}
	top := ts.stack[len(ts.stack)-1]
	if !top.borrow {
		m.report(ReentrancyViolation, ts.id, ts.current,
			fmt.Sprintf("return %q would pop base identity frame (request %q); borrow scopes must unwind first",
				ev.Tag, top.identity.RequestID), top.chain, seq)
		return
	}
	if ev.Tag != "" && top.tag != ev.Tag {
		m.report(ReentrancyViolation, ts.id, ts.current,
			fmt.Sprintf("return %q does not match innermost borrow %q (request %q); LIFO order violated",
				ev.Tag, top.tag, top.identity.RequestID), top.chain, seq)
		return
	}
	ts.stack = ts.stack[:len(ts.stack)-1]
}

func (m *Monitor) handleExit(ev Event, ts *threadState, seq uint64) {
	m.appendLog(ts, seq, EventExit, ts.current, "request exit")
	openBorrows := 0
	for _, fr := range ts.stack {
		if fr.borrow {
			openBorrows++
		}
	}
	if openBorrows > 0 {
		top := ts.stack[len(ts.stack)-1]
		m.report(
			DirtyExit,
			ts.id,
			top.identity.RequestID,
			fmt.Sprintf("exit on thread %s with %d un-returned borrow frame(s); forcing stack to zero", ts.id, openBorrows),
			top.chain,
			seq,
		)
	}
	// A clean exit still discards the base identity frame: the stack must read
	// zero whatever state the request leaves behind.
	ts.stack = ts.stack[:0]
	ts.current = ""
}

func (m *Monitor) handleSpawn(ev Event, seq uint64) {
	parent := m.threadOf(ev.ThreadID)
	child := m.threadOf(ev.ChildID)

	if len(child.stack) > 0 {
		top := child.stack[len(child.stack)-1]
		m.report(
			StaleIdentityRead,
			child.id,
			ev.requestID(),
			fmt.Sprintf("spawn onto thread %s that still holds identity from request %q; resetting before handoff",
				child.id, top.identity.RequestID),
			top.chain,
			seq,
		)
		child.stack = child.stack[:0]
	}
	m.appendLog(child, seq, EventSpawn, ev.requestID(), fmt.Sprintf("spawned by %s", parent.id))

	switch {
	case ev.Snapshot != nil:
		hop := ev.Snapshot.provenance()
		for _, fr := range ev.Snapshot.frames {
			child.stack = append(child.stack, frame{
				identity: fr.identity,
				tag:      fr.tag,
				borrow:   fr.borrow,
				chain:    extendChain(fr.chain, hop),
			})
		}
		child.current = ev.Snapshot.requestID
	case ev.Inherit:
		var chain []Provenance
		requestID := ev.requestID()
		if len(parent.stack) > 0 {
			top := parent.stack[len(parent.stack)-1]
			chain = top.chain
			if requestID == "" {
				requestID = top.identity.RequestID
			}
		}
		m.report(
			ImplicitInheritance,
			child.id,
			requestID,
			fmt.Sprintf("spawn of %s from %s requested implicit inheritance; only explicit snapshots may carry identity",
				child.id, parent.id),
			chain,
			seq,
		)
	}
}

func (ev Event) requestID() string {
	if ev.Identity.RequestID != "" {
		return ev.Identity.RequestID
	}
	return ev.RequestID
}

// Snapshot captures an immutable copy of the identity state visible on a thread.
// The returned token is the only legal vehicle for identity across EventSpawn.
func (m *Monitor) Snapshot(threadID string) *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	seq := m.nextSeq()
	ts := m.threadOf(threadID)
	snap := &Snapshot{
		seq:       seq,
		threadID:  threadID,
		requestID: ts.current,
		frames:    make([]frame, 0, len(ts.stack)),
	}
	for _, fr := range ts.stack {
		copied := frame{
			identity: Identity{
				RequestID: fr.identity.RequestID,
				Principal: fr.identity.Principal,
			},
			tag:    fr.tag,
			borrow: fr.borrow,
			chain:  append([]Provenance(nil), fr.chain...),
		}
		if len(fr.identity.Attrs) > 0 {
			copied.identity.Attrs = make(map[string]string, len(fr.identity.Attrs))
			for k, v := range fr.identity.Attrs {
				copied.identity.Attrs[k] = v
			}
		}
		snap.frames = append(snap.frames, copied)
	}
	return snap
}

// ReadResult is the outcome of Monitor.Read: either an empty result or the
// innermost identity together with its full source chain.
type ReadResult struct {
	ThreadID string
	Identity Identity
	Found    bool
	Chain    []Provenance
}

// Read observes the identity currently effective on a thread. When the thread's
// running request differs from the request owning the top frame, a
// StaleIdentityRead finding is raised and its source chain returned.
func (m *Monitor) Read(threadID string) ReadResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts := m.threadOf(threadID)
	if len(ts.stack) == 0 {
		return ReadResult{ThreadID: threadID}
	}
	top := ts.stack[len(ts.stack)-1]
	chain := append([]Provenance(nil), top.chain...)
	if ts.current != "" && top.identity.RequestID != ts.current {
		m.report(
			StaleIdentityRead,
			ts.id,
			ts.current,
			fmt.Sprintf("read on thread %s for request %q returned stale identity from request %q",
				ts.id, ts.current, top.identity.RequestID),
			chain,
			m.nextSeq(),
		)
	}
	return ReadResult{
		ThreadID: threadID,
		Identity: top.identity,
		Found:    true,
		Chain:    chain,
	}
}

// StackDepth returns the number of live identity frames on a thread.
func (m *Monitor) StackDepth(threadID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.threadOf(threadID).stack)
}

// ThreadLog returns a copy of a thread's lifecycle sequence.
func (m *Monitor) ThreadLog(threadID string) []ThreadEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.threadOf(threadID)
	out := make([]ThreadEvent, len(ts.log))
	copy(out, ts.log)
	return out
}

// Findings returns a copy of every violation recorded so far.
func (m *Monitor) Findings() []Finding {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Finding, len(m.findings))
	copy(out, m.findings)
	return out
}

// FindingsByKind returns a copy of the findings matching kind.
func (m *Monitor) FindingsByKind(kind FindingKind) []Finding {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Finding
	for _, f := range m.findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}
