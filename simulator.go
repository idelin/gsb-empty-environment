package main

import (
	"container/heap"
	"fmt"
	"sort"
)

// This file is a single-process event simulator: communication (message
// delays, drops), faults (replica crashes) and time (a virtual clock)
// are all controlled explicitly by the scenario under test.

type simEvent struct {
	t   int64
	seq int
	fn  func()
}

type eventHeap []simEvent

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].t != h[j].t {
		return h[i].t < h[j].t
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(simEvent)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Faults describes the fault injection plan, keyed by message tag.
type Faults struct {
	Drop map[string]bool  // message tag -> dropped silently
	Hold map[string]int64 // message tag -> extra delivery delay
}

// Sim is the event simulator with a virtual clock.
type Sim struct {
	now    int64
	seq    int
	ev     eventHeap
	faults Faults
}

func NewSim(f Faults) *Sim {
	s := &Sim{faults: f}
	if s.faults.Drop == nil {
		s.faults.Drop = map[string]bool{}
	}
	if s.faults.Hold == nil {
		s.faults.Hold = map[string]int64{}
	}
	return s
}

// send models a network message: delivered after delay plus any injected
// hold, unless injected as dropped.
func (s *Sim) send(delay int64, tag string, fn func()) {
	if s.faults.Drop[tag] {
		return
	}
	s.schedule(delay+s.faults.Hold[tag], fn)
}

// schedule models a local timer on the virtual clock.
func (s *Sim) schedule(delay int64, fn func()) {
	heap.Push(&s.ev, simEvent{t: s.now + delay, seq: s.seq, fn: fn})
	s.seq++
}

// Run processes events in virtual-time order up to maxT.
func (s *Sim) Run(maxT int64) {
	for len(s.ev) > 0 {
		e := heap.Pop(&s.ev).(simEvent)
		if e.t > maxT {
			break
		}
		s.now = e.t
		e.fn()
	}
}

// stamp is a lexicographic version timestamp (counter, client id).
type stamp struct {
	n   int
	cid int
}

func tsAfter(a, b stamp) bool {
	return a.n > b.n || (a.n == b.n && a.cid > b.cid)
}

// Replica stores the single register cell.
type Replica struct {
	name    string
	val     int
	ts      stamp
	crashed bool
}

func quorum(n int) int { return n/2 + 1 }

// Client issues its script strictly sequentially (one outstanding op).
// If buggy is set, reads consult a single replica without quorum or
// write-back (a classic stale-read bug).
type Client struct {
	id     int
	sim    *Sim
	reps   []*Replica
	buggy  bool
	script []Op // planned ops; only Kind and Val are used
	next   int
	hist   []Op
	cur    *Op
	ctr    int
	nextID *int // shared op id allocator
}

func (c *Client) start(at int64) {
	c.sim.schedule(at, c.issue)
}

func (c *Client) issue() {
	if c.next >= len(c.script) {
		return
	}
	op := c.script[c.next]
	op.ID = *c.nextID
	*c.nextID++
	op.Invoke = c.sim.now
	c.cur = &op
	msgID := fmt.Sprintf("c%do%d", c.id, c.next)
	if op.Kind == OpWrite {
		c.doWrite(msgID, op.Val)
	} else if c.buggy {
		c.doReadBuggy(msgID)
	} else {
		c.doRead(msgID)
	}
}

func (c *Client) complete(readVal int) {
	op := *c.cur
	op.Respond = c.sim.now
	if op.Kind == OpRead {
		op.Val = readVal
	}
	c.hist = append(c.hist, op)
	c.next++
	c.sim.schedule(1, c.issue) // next op strictly after this response
}

// doWrite is the ABD-style write: store (val, ts) at a quorum.
func (c *Client) doWrite(msgID string, val int) {
	c.ctr++
	ts := stamp{n: c.ctr, cid: c.id}
	acks := 0
	done := false
	for _, r := range c.reps {
		r := r
		tag := msgID + ">" + r.name
		c.sim.send(1, tag, func() {
			if r.crashed {
				return
			}
			if tsAfter(ts, r.ts) {
				r.val, r.ts = val, ts
			}
			c.sim.send(1, tag+"/ack", func() {
				if done {
					return
				}
				acks++
				if acks >= quorum(len(c.reps)) {
					done = true
					c.complete(0)
				}
			})
		})
	}
}

// doRead is the ABD-style read: fetch a quorum, take the newest value,
// write it back to a quorum, then respond.
func (c *Client) doRead(msgID string) {
	type resp struct {
		val int
		ts  stamp
	}
	var resps []resp
	phase2 := false
	for _, r := range c.reps {
		r := r
		tag := msgID + ">" + r.name
		c.sim.send(1, tag, func() {
			if r.crashed {
				return
			}
			v, t := r.val, r.ts
			c.sim.send(1, tag+"/r", func() {
				if phase2 {
					return
				}
				resps = append(resps, resp{v, t})
				if len(resps) == quorum(len(c.reps)) {
					phase2 = true
					best := resps[0]
					for _, x := range resps[1:] {
						if tsAfter(x.ts, best.ts) {
							best = x
						}
					}
					c.writeBack(msgID, best.val, best.ts)
				}
			})
		})
	}
}

func (c *Client) writeBack(msgID string, val int, ts stamp) {
	acks := 0
	done := false
	for _, r := range c.reps {
		r := r
		tag := msgID + ">" + r.name + "/wb"
		c.sim.send(1, tag, func() {
			if r.crashed {
				return
			}
			if tsAfter(ts, r.ts) {
				r.val, r.ts = val, ts
			}
			c.sim.send(1, tag+"/ack", func() {
				if done {
					return
				}
				acks++
				if acks >= quorum(len(c.reps)) {
					done = true
					c.complete(val)
				}
			})
		})
	}
}

// doReadBuggy reads from one replica only: no quorum, no write-back.
func (c *Client) doReadBuggy(msgID string) {
	r := c.reps[0]
	tag := msgID + ">" + r.name
	c.sim.send(1, tag, func() {
		if r.crashed {
			return
		}
		v := r.val
		c.sim.send(1, tag+"/r", func() { c.complete(v) })
	})
}

// SimReport is the evidence bundle produced by a simulated run.
type SimReport struct {
	Invoked   int
	Completed int
	Now       int64
	History   []Op
}

func runClients(sim *Sim, clients []*Client, maxT int64) SimReport {
	sim.Run(maxT)
	rep := SimReport{Now: sim.now}
	for _, c := range clients {
		rep.Invoked += len(c.script)
		rep.Completed += len(c.hist)
		rep.History = append(rep.History, c.hist...)
	}
	sort.SliceStable(rep.History, func(a, b int) bool { return rep.History[a].ID < rep.History[b].ID })
	return rep
}
