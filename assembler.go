package traceasm

import (
	"fmt"
	"time"
)

type nodeState int

const (
	statePending nodeState = iota // orphan waiting for its parent
	stateRoot                     // attached as a trace root
	stateAttached                 // attached under a parent
	stateExpired                  // parent never arrived before the timeout
	stateCyclic                   // attaching would create a structural cycle
)

func (s nodeState) String() string {
	switch s {
	case statePending:
		return "pending"
	case stateRoot:
		return "root"
	case stateAttached:
		return "attached"
	case stateExpired:
		return "expired"
	case stateCyclic:
		return "cyclic"
	}
	return "unknown"
}

type node struct {
	span     Span
	parent   *node
	children []*node
	state    nodeState
	deadline time.Time
}

func (n *node) merge(s Span) {
	if s.Name != "" {
		n.span.Name = s.Name
	}
	if !s.Start.IsZero() {
		n.span.Start = s.Start
	}
	if !s.End.IsZero() {
		n.span.End = s.End
	}
}

type traceState struct {
	id      string
	nodes   map[string]*node
	waiting map[string][]*node // parentSpanID -> orphans waiting for it
}

// Assembler builds parent-child trees from spans that may arrive out of
// order, duplicated, or with missing/invalid parents.
type Assembler struct {
	timeout time.Duration
	traces  map[string]*traceState
	index   map[string]map[string]struct{} // spanID -> set of traceIDs that contain it
}

func New(orphanTimeout time.Duration) *Assembler {
	return &Assembler{
		timeout: orphanTimeout,
		traces:  make(map[string]*traceState),
		index:   make(map[string]map[string]struct{}),
	}
}

func (a *Assembler) trace(id string) *traceState {
	tr, ok := a.traces[id]
	if !ok {
		tr = &traceState{
			id:      id,
			nodes:   make(map[string]*node),
			waiting: make(map[string][]*node),
		}
		a.traces[id] = tr
	}
	return tr
}

// Ingest feeds one span observed at time at, and returns the events it caused.
func (a *Assembler) Ingest(s Span, at time.Time) []Event {
	tr := a.trace(s.TraceID)
	if a.index[s.SpanID] == nil {
		a.index[s.SpanID] = make(map[string]struct{})
	}
	a.index[s.SpanID][s.TraceID] = struct{}{}

	if old, ok := tr.nodes[s.SpanID]; ok {
		return a.handleDuplicate(tr, old, s, at)
	}

	n := &node{span: s, state: statePending, deadline: at.Add(a.timeout)}
	tr.nodes[s.SpanID] = n

	var evs []Event
	switch {
	case s.ParentSpanID == "":
		n.state = stateRoot
		n.deadline = time.Time{}
		evs = append(evs, Event{Type: EventRootRegistered, TraceID: s.TraceID, SpanID: s.SpanID, At: at})
	default:
		if p, ok := tr.nodes[s.ParentSpanID]; ok {
			evs = append(evs, a.tryAttach(tr, n, p, at, false)...)
		} else {
			if a.parentInOtherTrace(s) {
				evs = append(evs, Event{
					Type: EventCrossTraceRef, TraceID: s.TraceID, SpanID: s.SpanID,
					ParentID: s.ParentSpanID, At: at,
					Detail: fmt.Sprintf("parent %q lives in a different trace; kept pending, never re-rooted", s.ParentSpanID),
				})
			}
			tr.waiting[s.ParentSpanID] = append(tr.waiting[s.ParentSpanID], n)
			evs = append(evs, Event{
				Type: EventOrphanPending, TraceID: s.TraceID, SpanID: s.SpanID,
				ParentID: s.ParentSpanID, At: at,
				Detail: fmt.Sprintf("waiting for parent, deadline in %s", a.timeout),
			})
		}
	}
	evs = append(evs, a.resolve(tr, s.SpanID, at)...)
	return evs
}

func (a *Assembler) handleDuplicate(tr *traceState, old *node, s Span, at time.Time) []Event {
	if old.span.ParentSpanID == s.ParentSpanID {
		old.merge(s)
		return []Event{{
			Type: EventDuplicateUpdated, TraceID: s.TraceID, SpanID: s.SpanID,
			ParentID: s.ParentSpanID, At: at,
			Detail: "same parent; mutable fields merged",
		}}
	}
	return []Event{{
		Type: EventConflict, TraceID: s.TraceID, SpanID: s.SpanID,
		ParentID: s.ParentSpanID, At: at,
		Detail: fmt.Sprintf("conflicting parent: kept %q, rejected %q", old.span.ParentSpanID, s.ParentSpanID),
	}}
}

func (a *Assembler) parentInOtherTrace(s Span) bool {
	for traceID := range a.index[s.ParentSpanID] {
		if traceID != s.TraceID {
			return true
		}
	}
	return false
}

// tryAttach links n under p unless that would create a structural cycle.
func (a *Assembler) tryAttach(tr *traceState, n, p *node, at time.Time, resolved bool) []Event {
	if createsCycle(n, p) {
		n.state = stateCyclic
		n.deadline = time.Time{}
		return []Event{{
			Type: EventCycle, TraceID: n.span.TraceID, SpanID: n.span.SpanID,
			ParentID: p.span.SpanID, At: at,
			Detail: "attaching would create a structural cycle; span left unattached",
		}}
	}
	n.parent = p
	p.children = append(p.children, n)
	n.state = stateAttached
	n.deadline = time.Time{}
	typ := EventSpanAttached
	if resolved {
		typ = EventOrphanResolved
	}
	return []Event{{
		Type: typ, TraceID: n.span.TraceID, SpanID: n.span.SpanID,
		ParentID: p.span.SpanID, At: at,
	}}
}

// createsCycle reports whether making n a child of p closes a loop.
func createsCycle(n, p *node) bool {
	for q := p; q != nil; q = q.parent {
		if q == n {
			return true
		}
	}
	return false
}

// resolve attaches any orphans that were waiting for parentID, recursively.
func (a *Assembler) resolve(tr *traceState, parentID string, at time.Time) []Event {
	orphans := tr.waiting[parentID]
	if len(orphans) == 0 {
		return nil
	}
	delete(tr.waiting, parentID)
	p := tr.nodes[parentID]
	var evs []Event
	for _, o := range orphans {
		if o.state != statePending {
			continue
		}
		evs = append(evs, a.tryAttach(tr, o, p, at, true)...)
		if o.state == stateAttached {
			evs = append(evs, a.resolve(tr, o.span.SpanID, at)...)
		}
	}
	return evs
}

// Expire fails all orphans whose parent did not arrive before their deadline.
// Expired orphans are reported but never attached to an arbitrary root.
func (a *Assembler) Expire(at time.Time) []Event {
	var evs []Event
	for _, tr := range a.traces {
		for parentID, list := range tr.waiting {
			kept := list[:0]
			for _, n := range list {
				if n.state == statePending && !n.deadline.After(at) {
					n.state = stateExpired
					n.deadline = time.Time{}
					evs = append(evs, Event{
						Type: EventOrphanExpired, TraceID: tr.id, SpanID: n.span.SpanID,
						ParentID: parentID, At: at,
						Detail: "parent never arrived; left unattached",
					})
				} else {
					kept = append(kept, n)
				}
			}
			if len(kept) == 0 {
				delete(tr.waiting, parentID)
			} else {
				tr.waiting[parentID] = kept
			}
		}
	}
	return evs
}

// TreeNode is a read-only view of one assembled span and its children.
type TreeNode struct {
	Span     Span
	State    string
	Children []*TreeNode
}

// Forest is the assembled view of one trace.
type Forest struct {
	TraceID    string
	Roots      []*TreeNode
	Unattached []*TreeNode // expired or cyclic spans, deliberately not re-rooted
}

func buildTree(n *node) *TreeNode {
	t := &TreeNode{Span: n.span, State: n.state.String()}
	for _, c := range n.children {
		t.Children = append(t.Children, buildTree(c))
	}
	return t
}

// Tree returns the current forest for a trace.
func (a *Assembler) Tree(traceID string) Forest {
	f := Forest{TraceID: traceID}
	tr, ok := a.traces[traceID]
	if !ok {
		return f
	}
	for _, n := range tr.nodes {
		switch n.state {
		case stateRoot:
			f.Roots = append(f.Roots, buildTree(n))
		case stateExpired, stateCyclic, statePending:
			f.Unattached = append(f.Unattached, buildTree(n))
		}
	}
	return f
}

// Traces lists all known trace IDs.
func (a *Assembler) Traces() []string {
	ids := make([]string, 0, len(a.traces))
	for id := range a.traces {
		ids = append(ids, id)
	}
	return ids
}
