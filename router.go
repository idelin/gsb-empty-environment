package causalrouter

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDuplicate is returned when a message's causal past is already
// covered by the router's delivered clock.
var ErrDuplicate = errors.New("duplicate or already-delivered message")

// Dependency identifies a single causal predecessor event that a
// pending message is still waiting for.
type Dependency struct {
	Node string
	Seq  uint64
}

func (d Dependency) String() string {
	return fmt.Sprintf("%s:%d", d.Node, d.Seq)
}

// Evidence is the unreachable-proof generated when a set of buffered
// messages forms a causal cycle: every member waits on an event that
// only another member of the same cycle can produce, so none of them
// can ever become deliverable.
type Evidence struct {
	Kind    string
	Cycle   []string // message IDs forming the cycle, in dependency order
	Blocked []Dependency
	Detail  string
}

func (e Evidence) String() string {
	return fmt.Sprintf("%s: cycle=[%s] blocked-on=%v",
		e.Kind, strings.Join(e.Cycle, " -> "), e.Blocked)
}

// QuarantineRecord pairs an isolated message with its evidence.
type QuarantineRecord struct {
	Message  *Message
	Evidence Evidence
}

// Router checks causal dependencies of incoming messages, delivers
// them in causal order, buffers messages whose predecessors have not
// arrived yet, and quarantines messages involved in causal cycles.
type Router struct {
	delivered   VectorClock
	pending     []*Message
	quarantined []QuarantineRecord
	log         []*Message
	onDeliver   func(*Message)
}

func NewRouter() *Router {
	return &Router{delivered: VectorClock{}}
}

// OnDeliver registers a hook invoked for every delivered message.
func (r *Router) OnDeliver(fn func(*Message)) {
	r.onDeliver = fn
}

// Submit ingests one message. It returns ErrDuplicate for messages
// whose causal past is already delivered. Otherwise the message is
// either delivered immediately (causing any newly unblocked buffered
// messages to be delivered as well) or buffered until its causal
// predecessors arrive.
func (r *Router) Submit(m *Message) error {
	if m == nil || m.Sender == "" {
		return errors.New("invalid message: missing sender")
	}
	if m.Clock[m.Sender] == 0 {
		return fmt.Errorf("invalid message %q: sender clock entry missing", m.ID)
	}
	if m.Clock[m.Sender] <= r.delivered[m.Sender] {
		return ErrDuplicate
	}
	if r.deliverable(m) {
		r.deliver(m)
		r.drain()
	} else {
		r.pending = append(r.pending, m)
	}
	r.detectCycles()
	return nil
}

// deliverable reports whether every causal predecessor of m has been
// delivered: m must be the next event from its sender, and every other
// clock entry must already be covered.
func (r *Router) deliverable(m *Message) bool {
	if m.Clock[m.Sender] != r.delivered[m.Sender]+1 {
		return false
	}
	for node, seq := range m.Clock {
		if node == m.Sender {
			continue
		}
		if seq > r.delivered[node] {
			return false
		}
	}
	return true
}

func (r *Router) deliver(m *Message) {
	r.delivered.Merge(m.Clock)
	r.log = append(r.log, m)
	if r.onDeliver != nil {
		r.onDeliver(m)
	}
}

// drain repeatedly scans the buffer and delivers any message that has
// become deliverable, until no further progress can be made.
func (r *Router) drain() {
	for {
		progress := false
		remaining := r.pending[:0]
		for _, p := range r.pending {
			if r.deliverable(p) {
				r.deliver(p)
				progress = true
			} else {
				remaining = append(remaining, p)
			}
		}
		r.pending = remaining
		if !progress {
			return
		}
	}
}

// missingDeps returns the immediate causal predecessors of m that have
// not been delivered yet: the next expected event of every node whose
// clock entry in m exceeds the delivered clock.
func (r *Router) missingDeps(m *Message) []Dependency {
	var deps []Dependency
	if m.Clock[m.Sender] > r.delivered[m.Sender]+1 {
		deps = append(deps, Dependency{m.Sender, r.delivered[m.Sender] + 1})
	}
	for node, seq := range m.Clock {
		if node == m.Sender {
			continue
		}
		if seq > r.delivered[node] {
			deps = append(deps, Dependency{node, r.delivered[node] + 1})
		}
	}
	return deps
}

// detectCycles builds the waiting graph among buffered messages — an
// edge m -> p means p is the buffered message that must be delivered to
// satisfy one of m's missing predecessors — and quarantines every
// strongly connected component of size > 1. Because each event is
// produced by exactly one message, a cycle in this graph can never be
// resolved from the outside: the evidence is a proof of unreachability.
func (r *Router) detectCycles() {
	if len(r.pending) < 2 {
		return
	}
	byEvent := map[Dependency]*Message{}
	for _, p := range r.pending {
		byEvent[Dependency{p.Sender, p.Clock[p.Sender]}] = p
	}
	edges := map[*Message][]*Message{}
	for _, m := range r.pending {
		for _, dep := range r.missingDeps(m) {
			if provider, ok := byEvent[dep]; ok && provider != m {
				edges[m] = append(edges[m], provider)
			}
		}
	}
	for _, scc := range stronglyConnected(r.pending, edges) {
		if len(scc) < 2 {
			continue
		}
		r.quarantine(scc)
	}
}

// quarantine removes the given messages from the buffer and records
// them together with cycle evidence.
func (r *Router) quarantine(cycle []*Message) {
	inCycle := map[*Message]bool{}
	ids := make([]string, 0, len(cycle))
	for _, m := range cycle {
		inCycle[m] = true
		ids = append(ids, m.ID)
	}
	var blocked []Dependency
	for _, m := range cycle {
		blocked = append(blocked, r.missingDeps(m)...)
	}
	evidence := Evidence{
		Kind:    "causal-cycle",
		Cycle:   ids,
		Blocked: blocked,
		Detail: fmt.Sprintf("messages %v mutually depend on each other; "+
			"their causal predecessors can only be produced inside the cycle "+
			"and are therefore unreachable", ids),
	}
	remaining := r.pending[:0]
	for _, p := range r.pending {
		if !inCycle[p] {
			remaining = append(remaining, p)
		}
	}
	r.pending = remaining
	for _, m := range cycle {
		r.quarantined = append(r.quarantined, QuarantineRecord{Message: m, Evidence: evidence})
	}
}

// stronglyConnected returns the strongly connected components of the
// graph (Tarjan's algorithm).
func stronglyConnected(nodes []*Message, edges map[*Message][]*Message) [][]*Message {
	index := map[*Message]int{}
	lowlink := map[*Message]int{}
	onStack := map[*Message]bool{}
	var stack []*Message
	var sccs [][]*Message
	counter := 0

	var visit func(m *Message)
	visit = func(m *Message) {
		index[m] = counter
		lowlink[m] = counter
		counter++
		stack = append(stack, m)
		onStack[m] = true
		for _, next := range edges[m] {
			if _, seen := index[next]; !seen {
				visit(next)
				if lowlink[next] < lowlink[m] {
					lowlink[m] = lowlink[next]
				}
			} else if onStack[next] && index[next] < lowlink[m] {
				lowlink[m] = index[next]
			}
		}
		if lowlink[m] == index[m] {
			var scc []*Message
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				scc = append(scc, top)
				if top == m {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	for _, m := range nodes {
		if _, seen := index[m]; !seen {
			visit(m)
		}
	}
	return sccs
}

// Pending returns the messages currently buffered, waiting for causal
// predecessors.
func (r *Router) Pending() []*Message {
	return append([]*Message(nil), r.pending...)
}

// Quarantined returns all quarantine records produced so far.
func (r *Router) Quarantined() []QuarantineRecord {
	return append([]QuarantineRecord(nil), r.quarantined...)
}

// Delivered returns the delivery log in causal order.
func (r *Router) Delivered() []*Message {
	return append([]*Message(nil), r.log...)
}

// DeliveredClock returns a copy of the router's delivered version vector.
func (r *Router) DeliveredClock() VectorClock {
	return r.delivered.Copy()
}
