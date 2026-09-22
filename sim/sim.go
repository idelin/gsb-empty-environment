// Package sim is a single-process discrete-event simulator for a set of
// replicas holding causal versions. Communication, failures and time are all
// simulated: no goroutines, no real network and no wall clock are involved.
//
// Replicas gossip version digests, fetch missing versions and evaluate
// consistent-slice requests. A request whose slice cannot yet be formed
// registers an independent waiter that is re-evaluated after every delivery.
package sim

import (
	"container/heap"
	"fmt"
	"sort"

	"causalslice/causal"
)

// Message kinds exchanged between replicas.
const (
	MsgDigest  = "digest"  // advertisement: "I hold these versions"
	MsgRequest = "request" // "please send me these versions"
	MsgPush    = "push"    // delivery of versions
)

// Message is one simulated network message.
type Message struct {
	Kind  string
	From  string
	To    string
	Items []causal.Ref
}

// DropRule decides, at send time, whether one message is lost. Rules model
// selective packet loss on top of whole-link partitions.
type DropRule func(now int, m Message) bool

// Event is an observable simulator occurrence, used for traces.
type Event struct {
	Time   int
	Kind   string
	Detail string
}

type replica struct {
	id      string
	up      bool
	present map[causal.Ref]bool
	known   map[causal.Ref]bool
}

type waiter struct {
	id      string
	repID   string
	req     causal.Request
	pending bool
	last    causal.Selection
	notify  func(causal.Selection)
	recheck int
}

// Simulator is the event loop and all controlled state.
type Simulator struct {
	reg  *causal.Registry
	now  int
	reps map[string]*replica

	linksUp map[string]bool // unordered "a|b"
	rules   []DropRule

	h       eventHeap
	seq     int
	Latency int // fixed per-message delay
	Gossip  int // gossip tick interval
	Recheck int // waiter re-evaluation interval
	OnEvent func(Event)

	// FilterPush may drop individual versions from an outgoing push, letting
	// a test deliver B while withholding its cause A.
	FilterPush func(now int, from, to string, items []causal.Ref) []causal.Ref

	waiters map[string]*waiter
}

// New creates a simulator with the given replicas, all connected.
func New(reg *causal.Registry, ids []string) *Simulator {
	s := &Simulator{
		reg:     reg,
		reps:    map[string]*replica{},
		linksUp: map[string]bool{},
		Latency: 5,
		Gossip:  20,
		Recheck: 10,
		waiters: map[string]*waiter{},
		OnEvent: func(Event) {},
	}
	for _, id := range ids {
		s.reps[id] = &replica{id: id, up: true, present: map[causal.Ref]bool{}, known: map[causal.Ref]bool{}}
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			s.linksUp[linkKey(ids[i], ids[j])] = true
		}
	}
	for _, id := range ids {
		s.schedule(s.Gossip, func() { s.gossipTick(id) })
	}
	return s
}

// Now returns the simulated time.
func (s *Simulator) Now() int { return s.now }

func linkKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// ---- failure control -------------------------------------------------------

// Down crashes a replica: it stops sending, receiving and gossiping.
func (s *Simulator) Down(id string) {
	if r, ok := s.reps[id]; ok {
		r.up = false
		s.log("replica-down", id)
	}
}

// Up restarts a crashed replica.
func (s *Simulator) Up(id string) {
	if r, ok := s.reps[id]; ok {
		r.up = true
		s.log("replica-up", id)
		s.schedule(0, func() { s.gossipTick(id) })
	}
}

// Partition severs the bidirectional link between a and b.
func (s *Simulator) Partition(a, b string) {
	s.linksUp[linkKey(a, b)] = false
	s.log("partition", fmt.Sprintf("%s <X> %s", a, b))
}

// Heal restores the bidirectional link between a and b and triggers gossip.
func (s *Simulator) Heal(a, b string) {
	s.linksUp[linkKey(a, b)] = true
	s.log("heal", fmt.Sprintf("%s <-> %s", a, b))
	s.schedule(0, func() { s.gossipTick(a) })
	s.schedule(0, func() { s.gossipTick(b) })
}

// AddRule installs an extra packet-loss rule.
func (s *Simulator) AddRule(r DropRule) { s.rules = append(s.rules, r) }

// ClearRules removes all packet-loss rules (failures stop; used for liveness
// convergence checks).
func (s *Simulator) ClearRules() { s.rules = nil }

// ---- writes ----------------------------------------------------------------

// Write stamps a new causal version of object at replica rep and stores it
// there immediately. deps are cross-object causal predecessors; the
// same-object predecessor is linked automatically.
func (s *Simulator) Write(rep, object string, deps []causal.Ref) (causal.Ref, error) {
	r, ok := s.reps[rep]
	if !ok {
		return causal.Ref{}, fmt.Errorf("unknown replica %q", rep)
	}
	if !r.up {
		return causal.Ref{}, fmt.Errorf("replica %q is down", rep)
	}
	ref, err := s.reg.Stamp(object, deps)
	if err != nil {
		s.log("stamp-error", err.Error())
		return causal.Ref{}, err
	}
	r.present[ref] = true
	r.known[ref] = true
	s.log("write", fmt.Sprintf("%s at %s deps=%s", ref, rep, joinRefs(depsOf(s.reg, ref))))
	s.evaluateWaiters()
	return ref, nil
}

func depsOf(reg *causal.Registry, ref causal.Ref) []causal.Ref {
	v, _ := reg.Lookup(ref)
	return v.Deps
}

// Schedule enqueues an arbitrary action at simulated time now+after.
func (s *Simulator) Schedule(after int, action func()) { s.schedule(after, action) }

// ---- reads -----------------------------------------------------------------

// View returns an independent, immutable copy of a replica's current versions.
func (s *Simulator) View(rep string) causal.View {
	r := s.reps[rep]
	return causal.BuildView(r.present, r.known)
}

// Read evaluates one request against a fresh private snapshot. The replica's
// state is copied, so the returned selection never shares mutable state with
// anything else.
func (s *Simulator) Read(rep string, req causal.Request) causal.Selection {
	return causal.Select(s.reg, s.View(rep), req)
}

// Wait registers a request that is evaluated repeatedly until complete. The
// waiter keeps its own last result; different waiters never share state.
// notify is invoked exactly once when the slice becomes available.
func (s *Simulator) Wait(rep string, req causal.Request, notify func(causal.Selection)) *Waiter {
	r, ok := s.reps[rep]
	if !ok {
		panic(fmt.Sprintf("sim: unknown replica %q", rep))
	}
	w := &waiter{
		id:      req.ID,
		repID:   rep,
		req:     req,
		pending: true,
		notify:  notify,
		recheck: s.Recheck,
	}
	s.waiters[w.id] = w
	s.log("wait", fmt.Sprintf("%s on %s wants latest of %v / %s",
		req.ID, rep, req.Latest, req.Target))
	sel := causal.Select(s.reg, causal.BuildView(r.present, r.known), req)
	w.last = sel
	if sel.Complete {
		w.pending = false
		s.log("wait-satisfied", fmt.Sprintf("%s slice=%s", w.id, sel.Closure))
		if w.notify != nil {
			w.notify(cloneSelection(sel))
		}
	} else {
		s.schedule(0, func() { s.recheckWaiter(w.id) })
	}
	return (*Waiter)(w)
}

// Waiter is a handle on a registered pending request.
type Waiter waiter

func (w *Waiter) ID() string { return w.id }

// Pending reports whether the request is still waiting for versions.
func (w *Waiter) Pending() bool { return w.pending }

// Last returns the most recent evaluation (its own private copy).
func (w *Waiter) Last() causal.Selection { return cloneSelection(w.last) }

func cloneSelection(in causal.Selection) causal.Selection {
	cloneVV := func(v causal.VV) causal.VV {
		c := make(causal.VV, len(v))
		for o, n := range v {
			c[o] = n
		}
		return c
	}
	return causal.Selection{
		Selected: cloneVV(in.Selected),
		Closure:  cloneVV(in.Closure),
		Blocking: append([]causal.Block(nil), in.Blocking...),
		Complete: in.Complete,
	}
}

func (s *Simulator) evaluateWaiters() {
	for _, w := range s.waiters {
		if !w.pending {
			continue
		}
		r := s.reps[w.repID]
		sel := causal.Select(s.reg, causal.BuildView(r.present, r.known), w.req)
		w.last = sel
		if sel.Complete {
			w.pending = false
			s.log("wait-satisfied", fmt.Sprintf("%s slice=%s", w.id, sel.Closure))
			if w.notify != nil {
				w.notify(cloneSelection(sel))
			}
		}
	}
}

func (s *Simulator) recheckWaiter(id string) {
	w, ok := s.waiters[id]
	if !ok || !w.pending {
		return
	}
	s.evaluateWaiters()
	if w.pending {
		s.schedule(w.recheck, func() { s.recheckWaiter(id) })
	}
}

// ---- gossip / messaging ----------------------------------------------------

func (s *Simulator) gossipTick(from string) {
	r, ok := s.reps[from]
	if ok && r.up {
		peers := make([]string, 0, len(s.reps))
		for id := range s.reps {
			if id != from {
				peers = append(peers, id)
			}
		}
		sort.Strings(peers)
		for _, p := range peers {
			if !s.linkUp(from, p) {
				continue
			}
			refs := make([]causal.Ref, 0, len(r.present))
			for ref := range r.present {
				refs = append(refs, ref)
			}
			sortRefs(refs)
			s.send(Message{Kind: MsgDigest, From: from, To: p, Items: refs})
		}
	}
	s.schedule(s.Gossip, func() { s.gossipTick(from) })
}

func (s *Simulator) linkUp(a, b string) bool {
	return s.reps[a].up && s.reps[b].up && s.linksUp[linkKey(a, b)]
}

func (s *Simulator) send(m Message) {
	if !s.linkUp(m.From, m.To) {
		return // partitions drop silently
	}
	for _, rule := range s.rules {
		if rule(s.now, m) {
			s.log("drop", fmt.Sprintf("%s %s->%s items=%s",
				m.Kind, m.From, m.To, joinRefs(m.Items)))
			return
		}
	}
	delay := s.Latency
	s.schedule(delay, func() { s.deliver(m) })
}

func (s *Simulator) deliver(m Message) {
	if !s.linkUp(m.From, m.To) {
		return
	}
	r := s.reps[m.To]
	switch m.Kind {
	case MsgDigest:
		for _, ref := range m.Items {
			r.known[ref] = true
		}
		missing := []causal.Ref{}
		for _, ref := range m.Items {
			if !r.present[ref] {
				missing = append(missing, ref)
			}
		}
		s.evaluateWaiters()
		if len(missing) > 0 {
			s.send(Message{Kind: MsgRequest, From: m.To, To: m.From, Items: missing})
		}
	case MsgRequest:
		have := []causal.Ref{}
		for _, ref := range m.Items {
			if r.present[ref] {
				have = append(have, ref)
			}
		}
		if len(have) > 0 {
			s.send(Message{Kind: MsgPush, From: m.To, To: m.From, Items: have})
		}
	case MsgPush:
		items := append([]causal.Ref(nil), m.Items...)
		if s.FilterPush != nil {
			items = s.FilterPush(s.now, m.From, m.To, items)
		}
		added := 0
		for _, ref := range items {
			r.known[ref] = true
			if !r.present[ref] {
				r.present[ref] = true
				added++
			}
		}
		if added > 0 {
			kept := []causal.Ref{}
			for _, ref := range items {
				if r.present[ref] {
					kept = append(kept, ref)
				}
			}
			s.log("deliver", fmt.Sprintf("%s <= %s items=%s",
				m.To, m.From, joinRefs(kept)))
			s.evaluateWaiters()
		}
	}
}

// ---- event loop ------------------------------------------------------------

type event struct {
	at  int
	seq int
	fn  func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func (s *Simulator) schedule(after int, fn func()) {
	s.seq++
	heap.Push(&s.h, event{at: s.now + after, seq: s.seq, fn: fn})
}

// RunUntil processes all events scheduled at or before deadline.
func (s *Simulator) RunUntil(deadline int) {
	for s.h.Len() > 0 && s.h[0].at <= deadline {
		ev := heap.Pop(&s.h).(event)
		s.now = ev.at
		ev.fn()
	}
	if s.now < deadline {
		s.now = deadline
	}
}

// PendingEvents reports the number of queued events (for tests/inspection).
func (s *Simulator) PendingEvents() int { return s.h.Len() }

func (s *Simulator) log(kind, detail string) {
	s.OnEvent(Event{Time: s.now, Kind: kind, Detail: detail})
}

func sortRefs(refs []causal.Ref) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Object != refs[j].Object {
			return refs[i].Object < refs[j].Object
		}
		return refs[i].Seq < refs[j].Seq
	})
}

func joinRefs(refs []causal.Ref) string {
	out := "["
	for i, r := range refs {
		if i > 0 {
			out += ","
		}
		out += r.String()
	}
	return out + "]"
}
