package main

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// SimConfig controls the single-process event simulation: communication
// delays, faults, and (logical) time are all decided here.
type SimConfig struct {
	Seed         int64
	NumClients   int
	OpsPerClient int
	MaxDelay     int64   // max one-way network delay (logical time units)
	CrashProb    float64 // client crashes right after invoking (call stays incomplete)
	LossProb     float64 // request message is lost (call stays incomplete)
	StaleRead    bool    // server bug: reads may return the previous value
}

// SimResult is the simulated history plus liveness evidence.
type SimResult struct {
	History         *History
	Completed       int
	Incomplete      int
	EventsProcessed int
	Quiescent       bool // event queue fully drained: nothing is pending
	Unanswered      int  // non-crashed, non-lost requests without a response (must be 0)
}

// event kinds
const (
	evInvoke      = iota // client issues its next request
	evDeliverReq         // request arrives at the server
	evDeliverResp        // response arrives at the client
)

type event struct {
	time   int64
	kind   int
	client int
	opID   int
	seq    int64 // tie-breaker for deterministic ordering
}

type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].time != q[j].time {
		return q[i].time < q[j].time
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x interface{}) { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	e := old[n-1]
	*q = old[:n-1]
	return e
}

const maxEvents = 1000000 // liveness guard: simulation must quiesce below this

type clientState struct {
	crashed     bool
	outstanding int // opID currently in flight, -1 if none
	issued      int // ops invoked so far
}

// Simulate runs the closed-loop simulation and returns the recorded history.
// The server is a correct single-key register unless StaleRead is set.
func Simulate(cfg SimConfig) *SimResult {
	rnd := rand.New(rand.NewSource(cfg.Seed))
	q := &eventQueue{}
	heap.Init(q)
	var seq int64
	push := func(t int64, kind, client, opID int) {
		seq++
		heap.Push(q, event{time: t, kind: kind, client: client, opID: opID, seq: seq})
	}

	clients := make([]clientState, cfg.NumClients)
	for i := range clients {
		clients[i].outstanding = -1
		push(0, evInvoke, i, -1)
	}

	history := &History{}
	regValue := 0 // server-side register
	nextOpID := 0
	var now int64
	res := &SimResult{History: history}
	lost := make(map[int]bool) // opIDs whose request message was lost

	for q.Len() > 0 {
		if res.EventsProcessed >= maxEvents {
			return res // Quiescent stays false: liveness violation evidence
		}
		e := heap.Pop(q).(event)
		res.EventsProcessed++
		now = e.time
		cl := &clients[e.client]

		switch e.kind {
		case evInvoke:
			if cl.crashed || cl.issued >= cfg.OpsPerClient {
				continue
			}
			op := Op{ID: nextOpID, Client: e.client, Invoke: now, Respond: -1}
			nextOpID++
			if rnd.Float64() < 0.5 {
				op.Kind = Read
			} else {
				op.Kind = Write
				op.Value = rnd.Intn(4) + 1
			}
			history.Ops = append(history.Ops, op)
			cl.issued++
			cl.outstanding = op.ID
			// Fault injection, decided at invoke time.
			if rnd.Float64() < cfg.CrashProb {
				cl.crashed = true // response will never be recorded
				continue
			}
			if rnd.Float64() < cfg.LossProb {
				lost[op.ID] = true
				continue // request lost in the network
			}
			push(now+1+rnd.Int63n(cfg.MaxDelay+1), evDeliverReq, e.client, op.ID)

		case evDeliverReq:
			op := &history.Ops[e.opID]
			if op.Kind == Write {
				regValue = op.Value
			} else {
				op.Value = regValue
				if cfg.StaleRead && regValue != 0 && rnd.Float64() < 0.5 {
					op.Value = regValue - 1 // injected safety bug
				}
			}
			push(now+1+rnd.Int63n(cfg.MaxDelay+1), evDeliverResp, e.client, op.ID)

		case evDeliverResp:
			if cl.crashed || cl.outstanding != e.opID {
				continue
			}
			history.Ops[e.opID].Respond = now
			cl.outstanding = -1
			push(now+1+rnd.Int63n(cfg.MaxDelay+1), evInvoke, e.client, -1)
		}
	}

	res.Quiescent = true
	for i := range history.Ops {
		if history.Ops[i].Complete() {
			res.Completed++
		} else {
			res.Incomplete++
		}
	}
	// Liveness evidence: every request that was neither crashed nor lost
	// must have been answered. We re-derive it from client state: a client
	// that is not crashed and still has an outstanding op while the queue
	// is empty means a response was dropped.
	for i := range clients {
		if !clients[i].crashed && clients[i].outstanding >= 0 && !lost[clients[i].outstanding] {
			res.Unanswered++
		}
	}
	return res
}

func (r *SimResult) String() string {
	return fmt.Sprintf("events=%d quiescent=%v completed=%d incomplete=%d unanswered=%d",
		r.EventsProcessed, r.Quiescent, r.Completed, r.Incomplete, r.Unanswered)
}
