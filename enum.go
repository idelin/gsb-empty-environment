// Command schedenum is a cooperative scheduling enumerator.
//
// Up to MaxCoroutines coroutines run a fixed list of operations. A
// coroutine only yields at explicit yield points: shared-memory reads,
// shared-memory writes, waits and asserts. Local operations never yield.
// The enumerator explores, within a bounded number of steps, every
// distinct schedule, merging duplicate branches whose shared state (and
// full execution state) coincides, and reports the shortest event
// sequence that violates an assertion.
//
// Merge safety: the merge key contains the shared memory AND, for every
// coroutine, its program counter, its local registers and its wait tag.
// Two branches with identical memory but different wait states (hence
// different runnability) are therefore never merged.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// MaxCoroutines is the hard limit on cooperating coroutines.
const MaxCoroutines = 4

// Mem is the shared memory visible to every coroutine.
type Mem map[string]int

// Locals are per-coroutine registers. They can only observe shared
// memory through an explicit Read operation.
type Locals map[string]int

// Kind classifies operations. Read, Write, Wait and Assert are explicit
// yield points; Local is not.
type Kind int

const (
	Read   Kind = iota // shared -> local
	Write              // local -> shared
	Wait               // block until Cond holds
	Assert             // fail if Cond does not hold
	Local              // local computation, never a yield point
)

// Op is a single operation of a coroutine.
type Op struct {
	Kind Kind
	Desc string                 // human-readable description (Wait/Assert/Local)
	Var  string                 // Read/Write: shared variable
	Dst  string                 // Read: local destination register
	Val  func(Locals) int       // Write: value computed from locals
	Cond func(Mem, Locals) bool // Wait/Assert: predicate
	Tag  string                 // Wait: wait-state tag, part of the merge key
	Do   func(Locals)           // Local: register update, no yield
}

// ReadOp reads shared variable v into local register dst.
func ReadOp(v, dst string) Op {
	return Op{Kind: Read, Var: v, Dst: dst}
}

// WriteOp writes val(locals) into shared variable v.
func WriteOp(v string, val func(Locals) int) Op {
	return Op{Kind: Write, Var: v, Val: val}
}

// WriteConst writes a constant into shared variable v.
func WriteConst(v string, n int) Op {
	return Op{Kind: Write, Var: v, Val: func(Locals) int { return n }}
}

// WaitOp blocks the coroutine until cond holds. The tag identifies the
// wait state in the merge key; if empty, desc is used.
func WaitOp(desc, tag string, cond func(Mem, Locals) bool) Op {
	if tag == "" {
		tag = desc
	}
	return Op{Kind: Wait, Desc: desc, Tag: tag, Cond: cond}
}

// AssertOp reports a violation if cond does not hold.
func AssertOp(desc string, cond func(Mem, Locals) bool) Op {
	return Op{Kind: Assert, Desc: desc, Cond: cond}
}

// LocalOp updates local registers without yielding.
func LocalOp(do func(Locals)) Op {
	return Op{Kind: Local, Do: do}
}

// Thread is a coroutine: a fixed sequence of operations.
type Thread struct {
	Name string
	Ops  []Op
}

// Event is one executed step of a schedule.
type Event struct {
	Step   int
	Thread string
	Text   string
}

func (e Event) String() string {
	return fmt.Sprintf("%2d. %-3s %s", e.Step, e.Thread, e.Text)
}

// Result summarizes an exhaustive exploration.
type Result struct {
	Violation   []Event // shortest assertion-violating schedule, nil if none
	Deadlock    []Event // shortest deadlocked schedule, nil if none
	States      int     // distinct merged states visited
	Transitions int     // executed steps (before merging)
	Truncated   bool    // true if the step bound cut off live schedules
}

// threadState is the full execution state of one coroutine. The wait
// tag is part of it: a blocked coroutine is not runnable even if shared
// memory matches some other branch where it was runnable.
type threadState struct {
	pc      int
	waiting string // "" means not blocked
	locals  Locals
}

type state struct {
	mem     Mem
	threads []threadState
}

func cloneMap(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (s *state) clone() *state {
	ns := &state{mem: Mem(cloneMap(s.mem)), threads: make([]threadState, len(s.threads))}
	for i, t := range s.threads {
		ns.threads[i] = threadState{pc: t.pc, waiting: t.waiting, locals: Locals(cloneMap(t.locals))}
	}
	return ns
}

// key encodes the full merged identity of a state: shared memory plus,
// per coroutine, pc, wait tag and locals. Branches are merged only when
// all of these coincide, so branches with equal memory but different
// runnability stay distinct.
func (s *state) key() string {
	var b strings.Builder
	writeMap(&b, s.mem)
	for _, t := range s.threads {
		fmt.Fprintf(&b, "|pc=%d;wait=%s;", t.pc, t.waiting)
		writeMap(&b, t.locals)
	}
	return b.String()
}

func writeMap(b *strings.Builder, m map[string]int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s=%d,", k, m[k])
	}
}

// Explorer enumerates schedules of a fixed set of coroutines.
type Explorer struct {
	Threads  []Thread
	InitMem  Mem
	MaxSteps int // bound on schedule length; <= 0 means 100
}

type node struct {
	st     *state
	parent *node
	event  Event
	depth  int
}

// Explore runs a breadth-first enumeration. The first assertion
// violation found is guaranteed to be a shortest one.
func (e *Explorer) Explore() Result {
	if len(e.Threads) == 0 {
		panic("schedenum: need at least one coroutine")
	}
	if len(e.Threads) > MaxCoroutines {
		panic(fmt.Sprintf("schedenum: at most %d coroutines, got %d", MaxCoroutines, len(e.Threads)))
	}
	maxSteps := e.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}

	var res Result
	st := &state{mem: Mem(cloneMap(e.InitMem)), threads: make([]threadState, len(e.Threads))}
	for i := range e.Threads {
		st.threads[i].locals = Locals{}
		e.skipLocals(st, i)
	}

	seen := map[string]bool{st.key(): true}
	queue := []*node{{st: st}}
	var deadlock *node

	for head := 0; head < len(queue); head++ {
		n := queue[head]
		if n.depth >= maxSteps {
			// Only live states are ever enqueued, so this cuts a schedule.
			res.Truncated = true
			continue
		}
		for i := range e.Threads {
			if !e.runnable(n.st, i) {
				continue
			}
			ns, ev, failed := e.step(n.st, i)
			res.Transitions++
			child := &node{st: ns, parent: n, event: ev, depth: n.depth + 1}
			if failed {
				// BFS order: no shorter violation can exist.
				res.Violation = trace(child)
				res.States = len(seen)
				if deadlock != nil {
					res.Deadlock = trace(deadlock)
				}
				return res
			}
			k := ns.key()
			if seen[k] {
				continue // duplicate branch, merged
			}
			seen[k] = true
			if e.done(ns) {
				continue // successful terminal state
			}
			if !e.anyRunnable(ns) {
				if deadlock == nil {
					deadlock = child
				}
				continue
			}
			queue = append(queue, child)
		}
	}
	res.States = len(seen)
	if deadlock != nil {
		res.Deadlock = trace(deadlock)
	}
	return res
}

// skipLocals runs consecutive Local operations; they never yield.
func (e *Explorer) skipLocals(s *state, i int) {
	ops := e.Threads[i].Ops
	t := &s.threads[i]
	for t.pc < len(ops) && ops[t.pc].Kind == Local {
		if ops[t.pc].Do != nil {
			ops[t.pc].Do(t.locals)
		}
		t.pc++
	}
}

// runnable reports whether coroutine i may be scheduled. A coroutine
// blocked in a Wait only becomes runnable once its condition holds.
func (e *Explorer) runnable(s *state, i int) bool {
	t := &s.threads[i]
	ops := e.Threads[i].Ops
	if t.pc >= len(ops) {
		return false
	}
	if op := ops[t.pc]; op.Kind == Wait && t.waiting != "" {
		return op.Cond(s.mem, t.locals)
	}
	return true
}

func (e *Explorer) done(s *state) bool {
	for i := range e.Threads {
		if s.threads[i].pc < len(e.Threads[i].Ops) {
			return false
		}
	}
	return true
}

func (e *Explorer) anyRunnable(s *state) bool {
	for i := range e.Threads {
		if e.runnable(s, i) {
			return true
		}
	}
	return false
}

// step executes exactly one yield-point operation of coroutine i on a
// fresh copy of s, then collapses any following Local operations.
func (e *Explorer) step(s *state, i int) (*state, Event, bool) {
	ns := s.clone()
	t := &ns.threads[i]
	th := e.Threads[i]
	op := th.Ops[t.pc]
	ev := Event{Thread: th.Name}
	failed := false

	switch op.Kind {
	case Read:
		v := ns.mem[op.Var]
		t.locals[op.Dst] = v
		ev.Text = fmt.Sprintf("read %s -> %s=%d", op.Var, op.Dst, v)
		t.pc++
	case Write:
		v := op.Val(t.locals)
		ns.mem[op.Var] = v
		ev.Text = fmt.Sprintf("write %s=%d", op.Var, v)
		t.pc++
	case Wait:
		if op.Cond(ns.mem, t.locals) {
			ev.Text = fmt.Sprintf("wait %s: ok", op.Desc)
			t.waiting = ""
			t.pc++
		} else {
			ev.Text = fmt.Sprintf("wait %s: blocked", op.Desc)
			t.waiting = op.Tag // pc stays at the Wait op
		}
	case Assert:
		if op.Cond(ns.mem, t.locals) {
			ev.Text = fmt.Sprintf("assert %s: ok", op.Desc)
		} else {
			ev.Text = fmt.Sprintf("assert %s: FAILED", op.Desc)
			failed = true
		}
		t.pc++
	default:
		panic(fmt.Sprintf("schedenum: op kind %d at pc %d is not a yield point", op.Kind, t.pc))
	}
	e.skipLocals(ns, i)
	return ns, ev, failed
}

// trace reconstructs the event sequence leading to n.
func trace(n *node) []Event {
	var rev []Event
	for cur := n; cur.parent != nil; cur = cur.parent {
		rev = append(rev, cur.event)
	}
	evs := make([]Event, len(rev))
	for i, ev := range rev {
		ev.Step = len(rev) - i
		evs[len(rev)-1-i] = ev
	}
	return evs
}
