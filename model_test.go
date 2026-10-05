package fairlock

// This file proves the safety properties of the algorithm by
// exhaustive deterministic scheduling. The algorithm's state machine
// is re-expressed as a pure model (modelState) whose transitions
// mirror rwmutex.go step for step:
//
//	rwmutex.go                    model
//	--------------------------    ------------------------------
//	acquire fast path             opAcquireR / opAcquireW
//	enqueue waiter                opAcquireR / opAcquireW
//	<-w.ch (grant noticed)        opAwait, "notice" branch
//	<-ctx.Done() (cancel)         opAwait, "cancel" branch
//	Unlock / RUnlock              opRelease
//	Downgrade                     opDowngrade
//	grantLocked                   closure()
//
// The explorer enumerates every interleaving of these atomic steps for
// several goroutine scenarios, including both outcomes of the
// grant-vs-cancel race at every await point, and asserts the
// invariants in checkInvariants after every single step:
//
//	1. never two writers,
//	2. never a writer concurrent with readers,
//	3. no lost wakeup: whenever the front waiter is grantable, its
//	   grant has been committed,
//	4. FIFO fairness: no reader is granted over an earlier-queued
//	   writer,
//	5. queue/grant-set consistency (no waiter queued twice, none
//	   both queued and granted).

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

type mKind int8

const (
	mReader mKind = iota
	mWriter
)

type mOp int8

const (
	opAcquireR mOp = iota
	opAcquireW
	opAwait
	opRelease
	opDowngrade
)

type mWaiter struct {
	id   int
	kind mKind
	seq  int
}

type mGoroutine struct {
	id       int
	ops      []mOp
	pc       int
	holds    int // 0 = none, 1 = read, 2 = write
	waiting  bool
	waitKind mKind
}

type modelState struct {
	readers int
	writers int // 0 or 1; kept as int so the explorer can detect 2
	queue   []mWaiter
	granted map[int]mWaiter // handoff committed, not yet consumed
	seq     int
	gs      []*mGoroutine
}

func (m *modelState) clone() *modelState {
	c := &modelState{
		readers: m.readers,
		writers: m.writers,
		seq:     m.seq,
		queue:   append([]mWaiter(nil), m.queue...),
		granted: make(map[int]mWaiter, len(m.granted)),
		gs:      make([]*mGoroutine, len(m.gs)),
	}
	for id, w := range m.granted {
		c.granted[id] = w
	}
	for i, g := range m.gs {
		gc := *g
		c.gs[i] = &gc
	}
	return c
}

// closure mirrors grantLocked in rwmutex.go.
func closure(m *modelState) {
	for len(m.queue) > 0 {
		w := m.queue[0]
		if w.kind == mReader {
			if m.writers > 0 {
				return
			}
			m.queue = m.queue[1:]
			m.readers++
			m.granted[w.id] = w
		} else {
			if m.writers > 0 || m.readers > 0 {
				return
			}
			m.queue = m.queue[1:]
			m.writers++
			m.granted[w.id] = w
			return
		}
	}
}

type step struct {
	desc  string
	state *modelState
}

// nextStates enumerates every atomic step every goroutine can take
// from m. Each returned state is an independent copy.
func nextStates(m *modelState) []step {
	var out []step
	for _, g := range m.gs {
		if g.pc >= len(g.ops) {
			continue
		}
		switch g.ops[g.pc] {
		case opAcquireR, opAcquireW:
			kind := mReader
			if g.ops[g.pc] == opAcquireW {
				kind = mWriter
			}
			c := m.clone()
			cg := c.gs[g.id]
			cg.pc++
			grantable := c.writers == 0 && (kind == mReader || c.readers == 0)
			if len(c.queue) == 0 && grantable {
				if kind == mReader {
					c.readers++
					cg.holds = 1
				} else {
					c.writers++
					cg.holds = 2
				}
			} else {
				c.seq++
				c.queue = append(c.queue, mWaiter{id: g.id, kind: kind, seq: c.seq})
				cg.waiting = true
				cg.waitKind = kind
			}
			out = append(out, step{fmt.Sprintf("g%d acquire", g.id), c})

		case opAwait:
			// Branch 1: the grant is noticed (only possible if committed).
			if w, ok := m.granted[g.id]; ok {
				c := m.clone()
				cg := c.gs[g.id]
				delete(c.granted, g.id)
				cg.pc++
				cg.waiting = false
				if w.kind == mReader {
					cg.holds = 1
				} else {
					cg.holds = 2
				}
				out = append(out, step{fmt.Sprintf("g%d notice grant", g.id), c})
			}
			// Branch 2: the context fires. The outcome is decided
			// atomically: if the grant was committed first, the grant
			// wins; otherwise the waiter is dequeued and the closure
			// runs. This is exactly the cancel path of acquire().
			c := m.clone()
			cg := c.gs[g.id]
			cg.pc++
			cg.waiting = false
			if w, ok := c.granted[g.id]; ok {
				delete(c.granted, g.id)
				if w.kind == mReader {
					cg.holds = 1
				} else {
					cg.holds = 2
				}
			} else {
				for i, qw := range c.queue {
					if qw.id == g.id {
						c.queue = append(c.queue[:i], c.queue[i+1:]...)
						break
					}
				}
				closure(c)
			}
			out = append(out, step{fmt.Sprintf("g%d cancel", g.id), c})

		case opRelease:
			c := m.clone()
			cg := c.gs[g.id]
			cg.pc++
			// A goroutine whose wait was cancelled owns nothing and
			// does not release; only an actual holder releases.
			if cg.holds != 0 {
				if cg.holds == 2 {
					c.writers--
				} else {
					c.readers--
				}
				cg.holds = 0
				closure(c)
			}
			out = append(out, step{fmt.Sprintf("g%d release", g.id), c})

		case opDowngrade:
			c := m.clone()
			cg := c.gs[g.id]
			cg.pc++
			// Only a write holder can downgrade; a cancelled waiter
			// skips it, exactly like real caller code would.
			if cg.holds == 2 {
				c.writers--
				c.readers++
				cg.holds = 1
				closure(c)
			}
			out = append(out, step{fmt.Sprintf("g%d downgrade", g.id), c})
		}
	}
	return out
}

func checkInvariants(m *modelState) error {
	if m.writers > 1 {
		return fmt.Errorf("two writers hold the lock")
	}
	if m.writers == 1 && m.readers > 0 {
		return fmt.Errorf("writer active concurrently with %d readers", m.readers)
	}
	if m.readers < 0 || m.writers < 0 {
		return fmt.Errorf("negative holder count")
	}
	seen := map[int]bool{}
	for _, w := range m.queue {
		if seen[w.id] {
			return fmt.Errorf("g%d queued twice", w.id)
		}
		seen[w.id] = true
		if _, ok := m.granted[w.id]; ok {
			return fmt.Errorf("g%d both queued and granted", w.id)
		}
	}
	// No lost wakeup: a grantable front waiter must already be granted.
	if m.writers == 0 && len(m.queue) > 0 {
		front := m.queue[0]
		_, ok := m.granted[front.id]
		if front.kind == mReader && !ok {
			return fmt.Errorf("lost wakeup: reader g%d at queue front, no writer active, grant not committed", front.id)
		}
		if front.kind == mWriter && m.readers == 0 && !ok {
			return fmt.Errorf("lost wakeup: writer g%d at queue front, lock free, grant not committed", front.id)
		}
	}
	// FIFO fairness: no reader granted over an earlier-queued writer.
	for _, w := range m.queue {
		if w.kind != mWriter {
			continue
		}
		for id, g := range m.granted {
			if g.kind == mReader && g.seq > w.seq {
				return fmt.Errorf("reader g%d (seq %d) granted over earlier queued writer g%d (seq %d)", id, g.seq, w.id, w.seq)
			}
		}
	}
	return nil
}

func key(m *modelState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "r%d w%d s%d|", m.readers, m.writers, m.seq)
	for _, w := range m.queue {
		fmt.Fprintf(&b, "q%d:%d:%d;", w.id, w.kind, w.seq)
	}
	var ids []int
	for id := range m.granted {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "g%d:%d;", id, m.granted[id].kind)
	}
	for _, g := range m.gs {
		fmt.Fprintf(&b, "|p%d h%d w%v k%d", g.pc, g.holds, g.waiting, g.waitKind)
	}
	return b.String()
}

func describe(m *modelState) string {
	return key(m)
}

func explore(t *testing.T, name string, scripts [][]mOp, limit int) {
	root := &modelState{granted: map[int]mWaiter{}}
	for i, ops := range scripts {
		root.gs = append(root.gs, &mGoroutine{id: i, ops: ops})
	}
	visited := map[string]bool{key(root): true}
	stack := []*modelState{root}
	count := 0
	for len(stack) > 0 {
		m := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		if count > limit {
			t.Fatalf("%s: state space explosion: > %d states", name, limit)
		}
		if err := checkInvariants(m); err != nil {
			t.Fatalf("%s: invariant violated: %v\nstate: %s", name, err, describe(m))
		}
		for _, s := range nextStates(m) {
			k := key(s.state)
			if !visited[k] {
				visited[k] = true
				stack = append(stack, s.state)
			}
		}
	}
	t.Logf("%s: explored %d states, all invariants held", name, count)
}

func TestModelExhaustive(t *testing.T) {
	R := []mOp{opAcquireR, opAwait, opRelease}
	W := []mOp{opAcquireW, opAwait, opRelease}
	WD := []mOp{opAcquireW, opAwait, opDowngrade, opRelease}

	scenarios := []struct {
		name    string
		scripts [][]mOp
	}{
		{"writer-reader-reader", [][]mOp{W, R, R}},
		{"reader-writer-reader", [][]mOp{R, W, R}},
		{"writers-only", [][]mOp{W, W, W}},
		{"downgrade-reader-reader", [][]mOp{WD, R, R}},
		{"downgrade-reader-writer", [][]mOp{WD, R, W}},
		{"writer-writer-reader", [][]mOp{W, W, R}},
		{"mixed-four", [][]mOp{W, R, WD, R}},
		{"readers-writer-downgrade", [][]mOp{R, R, W, WD}},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			explore(t, s.name, s.scripts, 20000000)
		})
	}
}
