// Package sim implements a single-process deterministic discrete-event
// simulator used to drive a two-phase-commit cluster.
//
// Everything that is normally nondeterministic in a distributed system is
// controlled here:
//
//   - communication: the Network decides every send's delay, whether the
//     message is dropped and whether it is duplicated;
//   - failures: Fail/Recover (plus scheduled FailAt/RecoverAt) take nodes
//     down and bring them back, replaying the on-disk log;
//   - time: the scheduler owns a virtual clock. Node code never calls the
//     real clock, it only observes Sched.Now().
//
// The simulation is single-threaded: each event runs to completion before
// the next one is considered, so node code needs no locks and identical
// inputs always produce identical runs.
package sim

import (
	"container/heap"
	"fmt"
)

// Msg is the body of a message exchanged between nodes. Concrete message
// types live in the twopc package.
type Msg any

// Envelope wraps a message with its delivery metadata.
type Envelope struct {
	From string
	To   string
	Body Msg
	Seq  int64 // sequence number assigned at send time; duplicates share it
}

func (e Envelope) String() string {
	return fmt.Sprintf("%s -> %s %v (#%d)", e.From, e.To, e.Body, e.Seq)
}

// Node is implemented by coordinator and participant processes.
//
// Start is called on every (re)boot, including the very first one. Node code
// rebuilds its volatile state from the persistent log inside Start, so the
// same Start hook implements normal boot and crash recovery.
type Node interface {
	Start(ctx *NodeContext)
	Handle(ctx *NodeContext, env Envelope)
}

// CrashHook is invoked when a node fails or recovers. A nil hook is allowed.
type CrashHook func(ctx *NodeContext, crashing bool)

// NodeContext is the API surface exposed to node code.
type NodeContext struct {
	Sched  *Scheduler
	NodeID string

	node *nodeRT
}

// Now returns the simulator's virtual time.
func (c *NodeContext) Now() int64 { return c.Sched.Now() }

// Alive reports whether the node is currently up.
func (c *NodeContext) Alive() bool { return c.node.alive }

// Send hands a message to the network. Sending while down is ignored: a
// crashed process cannot perform any I/O.
func (c *NodeContext) Send(to string, body Msg) {
	if !c.node.alive {
		return
	}
	c.Sched.net.route(c.Sched, Envelope{From: c.NodeID, To: to, Body: body})
}

// Timer represents a pending timeout. Call Stop to cancel it; a stopped or
// fired timer never invokes its callback. Each After call returns a distinct
// timer, so replacing a stored timer never accidentally cancels or re-arms
// another outstanding one.
type Timer struct {
	gen    int64 // identity of this timer
	killed bool
}

// Stop cancels the timer. It is safe to call multiple times.
func (t *Timer) Stop() {
	if t == nil {
		return
	}
	t.killed = true
}

func (t *Timer) active() bool { return t != nil && !t.killed }

// After schedules fn to run after at least d virtual nanoseconds, provided
// the node is still alive at that time and the timer has not been stopped.
func (c *NodeContext) After(d int64, fn func()) *Timer {
	t := &Timer{}
	if !c.node.alive {
		t.killed = true
		return t
	}
	t.gen = c.Sched.plan(c.NodeID, c.Sched.Now()+d, func(s *Scheduler) {
		if t.active() && c.node.alive {
			t.killed = true
			fn()
		}
	})
	return t
}

// Stable returns the node's durable storage. Storage survives crashes but not
// an explicit Wipe; each node id has its own independent address space.
func (c *NodeContext) Stable() *StableStorage {
	return c.Sched.Stable(c.NodeID)
}

type event struct {
	when   int64
	seq    int64
	target string
	kind   byte // 1 = timer/action, 2 = message delivery
	env    Envelope
	action func(*Scheduler)
	index  int
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].when != h[j].when {
		return h[i].when < h[j].when
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *eventHeap) Push(x any) {
	ev := x.(*event)
	ev.index = len(*h)
	*h = append(*h, ev)
}
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return ev
}

type nodeRT struct {
	id    string
	impl  Node
	hook  CrashHook
	alive bool
}

// Scheduler is the deterministic event loop and owner of the clock.
type Scheduler struct {
	now    int64
	evs    eventHeap
	evSeq  int64
	timers map[int64]bool // pending timer ids
	next   int64

	nodes   map[string]*nodeRT
	net     *Network
	storage map[string]*StableStorage

	maxEvents int
}

// NewScheduler builds a scheduler. net may be nil, in which case a default
// network with a fixed delay and no failures is used.
func NewScheduler(net *Network) *Scheduler {
	if net == nil {
		net = NewNetwork()
	}
	return &Scheduler{
		net:       net,
		nodes:     map[string]*nodeRT{},
		storage:   map[string]*StableStorage{},
		timers:    map[int64]bool{},
		maxEvents: 1_000_000,
	}
}

// Network exposes the network for reconfiguration between runs.
func (s *Scheduler) Network() *Network { return s.net }

// SetMaxEvents bounds the number of events a Run/Step may process (defence in
// depth against livelock bugs).
func (s *Scheduler) SetMaxEvents(n int) { s.maxEvents = n }

// Now is the current virtual time in nanoseconds.
func (s *Scheduler) Now() int64 { return s.now }

// Stable returns the durable storage belonging to id, creating empty storage
// on first use. Storage persists across Fail/Recover.
func (s *Scheduler) Stable(id string) *StableStorage {
	st := s.storage[id]
	if st == nil {
		st = NewStableStorage()
		s.storage[id] = st
	}
	return st
}

// WipeStorage erases a node's durable storage (simulating disk loss /
// replacement). It must not be called while the node is running.
func (s *Scheduler) WipeStorage(id string) {
	if rt := s.nodes[id]; rt != nil && rt.alive {
		panic(fmt.Sprintf("cannot wipe storage of live node %q", id))
	}
	s.storage[id] = NewStableStorage()
}

// Deploy registers a node. The node stays down until Recover/Start is called.
func (s *Scheduler) Deploy(id string, n Node, hook CrashHook) {
	if _, dup := s.nodes[id]; dup {
		panic(fmt.Sprintf("duplicate node id %q", id))
	}
	s.nodes[id] = &nodeRT{id: id, impl: n, hook: hook}
}

// StartNode boots a freshly deployed node for the first time.
func (s *Scheduler) StartNode(id string) {
	rt := s.nodes[id]
	if rt == nil {
		panic(fmt.Sprintf("unknown node %q", id))
	}
	s.boot(rt)
}

// Recover reboots a failed node, replaying its durable log via Start.
func (s *Scheduler) Recover(id string) {
	rt := s.nodes[id]
	if rt == nil {
		panic(fmt.Sprintf("unknown node %q", id))
	}
	if rt.alive {
		return
	}
	s.boot(rt)
}

func (s *Scheduler) boot(rt *nodeRT) {
	rt.alive = true
	ctx := &NodeContext{Sched: s, NodeID: rt.id, node: rt}
	if rt.hook != nil {
		rt.hook(ctx, false)
	}
	rt.impl.Start(ctx)
}

// Fail immediately crashes the node: it is marked down (messages already
// queued for it will be discarded at delivery) and its crash hook runs.
func (s *Scheduler) Fail(id string) {
	rt := s.nodes[id]
	if rt == nil || !rt.alive {
		return
	}
	rt.alive = false
	ctx := &NodeContext{Sched: s, NodeID: rt.id, node: rt}
	if rt.hook != nil {
		rt.hook(ctx, true)
	}
}

// IsAlive reports node liveness.
func (s *Scheduler) IsAlive(id string) bool {
	rt := s.nodes[id]
	return rt != nil && rt.alive
}

// Plan runs fn when virtual time reaches t. The action is skipped if its node
// is down at that moment (a crashed node's pure callbacks cannot fire;
// network-level FailAt/RecoverAt are scheduled separately).
func (s *Scheduler) Plan(t int64, id string, fn func()) {
	s.planNode(t, id, fn)
}

func (s *Scheduler) planNode(t int64, id string, fn func()) {
	s.next++
	heap.Push(&s.evs, &event{
		when:   t,
		seq:    s.next,
		target: id,
		kind:   1,
		action: func(*Scheduler) {
			if id != "" {
				if rt := s.nodes[id]; rt == nil || !rt.alive {
					return
				}
			}
			fn()
		},
	})
}

// plan is used by NodeContext.After.
func (s *Scheduler) plan(id string, t int64, fn func(s *Scheduler)) int64 {
	s.next++
	n := s.next
	s.timers[n] = true
	heap.Push(&s.evs, &event{
		when:   t,
		seq:    s.next,
		target: id,
		kind:   1,
		action: func(*Scheduler) {
			if s.timers[n] {
				delete(s.timers, n)
				fn(s)
			}
		},
	})
	return n
}

// FailAt schedules a node crash at virtual time t.
func (s *Scheduler) FailAt(t int64, id string) {
	s.planNode(t, "", func() { s.Fail(id) })
}

// RecoverAt schedules a node reboot at virtual time t.
func (s *Scheduler) RecoverAt(t int64, id string) {
	s.planNode(t, "", func() { s.Recover(id) })
}

// deliver queues a message-delivery event.
func (s *Scheduler) deliver(at int64, env Envelope) {
	s.next++
	heap.Push(&s.evs, &event{
		when:   at,
		seq:    s.next,
		target: env.To,
		kind:   2,
		env:    env,
	})
}

// Pending reports the number of queued events.
func (s *Scheduler) Pending() int { return s.evs.Len() }

// Step processes at most one event and returns false when no event remained.
func (s *Scheduler) Step() bool {
	if s.evs.Len() == 0 {
		return false
	}
	ev := heap.Pop(&s.evs).(*event)
	s.now = ev.when
	if ev.kind == 2 {
		if rt := s.nodes[ev.target]; rt != nil && rt.alive {
			ctx := &NodeContext{Sched: s, NodeID: rt.id, node: rt}
			rt.impl.Handle(ctx, ev.env)
		}
		// Messages to dead/unknown nodes are simply discarded: the
		// network cannot deliver into a crashed machine.
	} else if ev.action != nil {
		ev.action(s)
	}
	return true
}

// Run processes events strictly before horizon, stopping earlier when the
// queue drains. It returns the number of events processed.
func (s *Scheduler) Run(horizon int64) int {
	n := 0
	for n < s.maxEvents && s.evs.Len() > 0 && s.evs[0].when < horizon {
		if !s.Step() {
			break
		}
		n++
	}
	if n >= s.maxEvents {
		panic("sim: event budget exhausted (possible livelock)")
	}
	return n
}
