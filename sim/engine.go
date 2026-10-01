package sim

import (
	"bytes"
	"container/heap"
	"encoding/gob"
	"fmt"
)

// Engine is the discrete-event simulation engine. All packets, queues and
// the link live here; time advances in integer ticks and no real network
// traffic is generated.
type Engine struct {
	Tick  int
	Sched *Scheduler

	events eventHeap
	seq    int

	linkBusy bool
	serving  *Packet

	Trace []TraceRecord
	Stats Stats
}

// TraceRecord describes one completed packet transmission.
type TraceRecord struct {
	Start    int
	End      int
	PacketID int
	FlowID   int
	TenantID int
	Bytes    int
	VStart   float64
	VFinish  float64
}

// Stats are cumulative counters. Invariant, checkable at any tick:
//
//	Arrived == Served + Cancelled + Backpressured + Withdrawn + still queued
type Stats struct {
	Arrived       int
	Served        int
	ServedBytes   int
	Cancelled     int
	Backpressured int
	Withdrawn     int
}

func NewEngine(rate int) *Engine {
	return &Engine{Sched: NewScheduler(rate)}
}

func (e *Engine) AddTenant(id int, weight float64) { e.Sched.AddTenant(id, weight) }

func (e *Engine) AddFlow(id, tenantID int, weight float64, capacity int) {
	e.Sched.AddFlow(id, tenantID, weight, capacity)
}

// Submit queues an event. Seq numbers are assigned in submission order so
// same-tick, same-type events are processed in the order they were submitted.
func (e *Engine) Submit(ev Event) {
	ev.Seq = e.seq
	e.seq++
	heap.Push(&e.events, ev)
}

// Arrive is a convenience wrapper to submit a packet arrival.
func (e *Engine) Arrive(tick, packetID, flowID, bytes int) {
	e.Submit(Event{Tick: tick, Type: EvArrive, Packet: &Packet{
		ID: packetID, FlowID: flowID, Bytes: bytes, ArriveTick: tick,
	}})
}

// Run processes all events with Tick <= until. All events sharing a tick are
// processed first (in Type/Seq order); the scheduling decision for that tick
// is made once, afterwards, so same-tick arrivals compete fairly.
func (e *Engine) Run(until int) {
	for len(e.events) > 0 && e.events[0].Tick <= until {
		tick := e.events[0].Tick
		e.Tick = tick
		for len(e.events) > 0 && e.events[0].Tick == tick {
			e.process(heap.Pop(&e.events).(Event))
		}
		e.maybeSchedule()
	}
}

func (e *Engine) process(ev Event) {
	switch ev.Type {
	case EvArrive:
		e.Stats.Arrived++
		e.admit(ev.Packet)
	case EvTxComplete:
		e.txComplete(ev.Packet)
	case EvFlowWeight:
		e.Sched.SetFlowWeight(ev.FlowID, ev.Weight, e.Tick)
	case EvTenantWeight:
		e.Sched.SetTenantWeight(ev.TenantID, ev.Weight, e.Tick)
	case EvWithdraw:
		e.Stats.Withdrawn += e.Sched.WithdrawFlow(ev.FlowID, e.Tick)
	case EvCancel:
		if e.Sched.CancelPacket(ev.PacketID, e.Tick) {
			e.Stats.Cancelled++
		}
	}
}

// admit applies backpressure: a packet is only enqueued if its flow is not
// withdrawn and has queue capacity left. Otherwise it is counted once as
// backpressured and never enters any queue.
func (e *Engine) admit(p *Packet) {
	f := e.Sched.Flows[p.FlowID]
	if f == nil || f.Withdrawn || (f.Capacity > 0 && len(f.Queue) >= f.Capacity) {
		p.Dropped = true
		e.Stats.Backpressured++
		return
	}
	e.Sched.Enqueue(p, e.Tick)
}

func (e *Engine) txComplete(p *Packet) {
	if p.Served {
		panic(fmt.Sprintf("packet %d completed twice", p.ID))
	}
	p.Served = true
	p.ServeEnd = e.Tick
	f := e.Sched.Flows[p.FlowID]
	e.Trace = append(e.Trace, TraceRecord{
		Start: p.ServeStart, End: p.ServeEnd, PacketID: p.ID,
		FlowID: p.FlowID, TenantID: f.TenantID, Bytes: p.Bytes,
		VStart: p.VStart, VFinish: p.VFinish,
	})
	e.Stats.Served++
	e.Stats.ServedBytes += p.Bytes
	f.ServedBytes += p.Bytes
	e.linkBusy = false
	e.serving = nil
}

// maybeSchedule starts a transmission if the link is free and work exists.
// The link transmits Rate bytes per tick; a packet occupies the link for
// ceil(Bytes/Rate) ticks (at least 1).
func (e *Engine) maybeSchedule() {
	if e.linkBusy {
		return
	}
	p := e.Sched.Select(e.Tick)
	if p == nil {
		return
	}
	e.linkBusy = true
	e.serving = p
	p.ServeStart = e.Tick
	dur := (p.Bytes + e.Sched.Rate - 1) / e.Sched.Rate
	if dur < 1 {
		dur = 1
	}
	e.Submit(Event{Tick: e.Tick + dur, Type: EvTxComplete, Packet: p})
}

// snapshot is the gob-serializable form of the full engine state.
type snapshot struct {
	Tick     int
	Sched    *Scheduler
	Events   []Event
	Seq      int
	LinkBusy bool
	Serving  *Packet
	Trace    []TraceRecord
	Stats    Stats
}

// Snapshot serializes the complete engine state (event queue, scheduler,
// link, trace, stats) so the simulation can be resumed later or elsewhere.
func (e *Engine) Snapshot() ([]byte, error) {
	var buf bytes.Buffer
	snap := snapshot{
		Tick: e.Tick, Sched: e.Sched, Events: e.events, Seq: e.seq,
		LinkBusy: e.linkBusy, Serving: e.serving, Trace: e.Trace, Stats: e.Stats,
	}
	if err := gob.NewEncoder(&buf).Encode(snap); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore rebuilds an engine from a snapshot. Pointer identity between the
// event queue, the scheduler queues and the in-service packet is preserved
// by gob, so a restored engine continues exactly where it left off.
func Restore(data []byte) (*Engine, error) {
	var snap snapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return nil, err
	}
	e := &Engine{
		Tick: snap.Tick, Sched: snap.Sched, seq: snap.Seq,
		linkBusy: snap.LinkBusy, serving: snap.Serving,
		Trace: snap.Trace, Stats: snap.Stats,
	}
	e.events = eventHeap(snap.Events)
	heap.Init(&e.events)
	return e, nil
}
