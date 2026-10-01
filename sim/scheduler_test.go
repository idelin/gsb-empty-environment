package sim

import (
	"math"
	"testing"
)

// bytesServedInWindow sums bytes of trace records whose completion tick
// falls in [from, to].
func bytesServedInWindow(trace []TraceRecord, flowID, from, to int) int {
	total := 0
	for _, r := range trace {
		if r.FlowID == flowID && r.End >= from && r.End <= to {
			total += r.Bytes
		}
	}
	return total
}

func burst(e *Engine, tick, flowID, firstID, n, size int) {
	for i := 0; i < n; i++ {
		e.Arrive(tick, firstID+i, flowID, size)
	}
}

// Two equal-weight flows with different packet sizes must receive equal
// service in BYTES, tracked against an independent fluid reference.
func TestByteFairnessDifferentPacketSizes(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0) // 10-byte packets
	e.AddFlow(2, 1, 1, 0) // 4-byte packets
	burst(e, 0, 1, 1, 30, 10)
	burst(e, 0, 2, 101, 30, 4)
	e.Run(420)

	// While both flows are backlogged (first 240 ticks) byte service must
	// be equal up to one max packet.
	b1 := bytesServedInWindow(e.Trace, 1, 0, 240)
	b2 := bytesServedInWindow(e.Trace, 2, 0, 240)
	if math.Abs(float64(b1-b2)) > 10 {
		t.Fatalf("byte service diverged: flow1=%d flow2=%d", b1, b2)
	}

	fluid := FluidServe([]FluidFlow{
		{FlowID: 1, TenantID: 1, Weight: 1, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 300}}},
		{FlowID: 2, TenantID: 1, Weight: 1, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 120}}},
	}, 1, 420)
	rep := ByteDeviation(e.Trace, fluid, 420)
	t.Logf("max byte deviation vs fluid: %.3f bytes (flow %d @ tick %d)", rep.MaxBytes, rep.WorstFlow, rep.WorstTick)
	if rep.MaxBytes > 10 {
		t.Fatalf("byte deviation %.3f exceeds max packet size 10", rep.MaxBytes)
	}
}

// An idle flow must not bank unlimited advantage: when it reactivates, its
// virtual start is the current virtual time, so it only gets its fair share.
func TestIdleFlowReactivationNoAdvantage(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0)
	e.AddFlow(2, 1, 1, 0)
	burst(e, 0, 1, 1, 200, 5)    // flow 1: always backlogged
	burst(e, 0, 2, 201, 10, 5)   // flow 2: drains around tick 100
	burst(e, 400, 2, 211, 10, 5) // flow 2: reactivates after a long idle gap
	e.Run(520)

	a := bytesServedInWindow(e.Trace, 1, 400, 500)
	b := bytesServedInWindow(e.Trace, 2, 400, 500)
	t.Logf("post-reactivation window [400,500]: flow1=%d bytes, flow2=%d bytes", a, b)
	if b > a+5 {
		t.Fatalf("reactivated flow gained unfair advantage: flow1=%d flow2=%d", a, b)
	}
	if a > b+5 {
		t.Fatalf("reactivated flow was starved: flow1=%d flow2=%d", a, b)
	}
}

// Equal virtual finish times are broken by packet ID.
func TestSameFinishTieBreakByPacketID(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0)
	e.AddFlow(2, 1, 1, 0)
	e.Arrive(0, 7, 1, 4) // submitted first, higher ID
	e.Arrive(0, 3, 2, 4) // same finish time, lower ID wins
	e.Run(20)
	if len(e.Trace) != 2 {
		t.Fatalf("expected 2 trace records, got %d", len(e.Trace))
	}
	if e.Trace[0].PacketID != 3 || e.Trace[1].PacketID != 7 {
		t.Fatalf("tie not broken by packet ID: got order %d,%d",
			e.Trace[0].PacketID, e.Trace[1].PacketID)
	}
}

// A dynamic weight change must shift the service ratio to the new weights.
func TestDynamicWeightChange(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0)
	e.AddFlow(2, 1, 1, 0)
	burst(e, 0, 1, 1, 100, 5)
	burst(e, 0, 2, 101, 100, 5)
	e.Submit(Event{Tick: 200, Type: EvFlowWeight, FlowID: 1, Weight: 3})
	e.Run(320)

	b1 := bytesServedInWindow(e.Trace, 1, 220, 320)
	b2 := bytesServedInWindow(e.Trace, 2, 220, 320)
	ratio := float64(b1) / float64(b2)
	t.Logf("post-change window [220,320]: flow1=%d flow2=%d ratio=%.2f (want ~3)", b1, b2, ratio)
	if ratio < 2.2 || ratio > 3.8 {
		t.Fatalf("weight change not reflected in byte service: ratio %.2f", ratio)
	}
}

// A withdrawn flow stops receiving service; its queued packets are accounted
// exactly once as withdrawn.
func TestFlowWithdrawal(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0)
	e.AddFlow(2, 1, 1, 0)
	burst(e, 0, 1, 1, 50, 5)
	burst(e, 0, 2, 51, 20, 5)
	e.Submit(Event{Tick: 30, Type: EvWithdraw, FlowID: 2})
	e.Run(400)

	served2 := 0
	for _, r := range e.Trace {
		if r.FlowID == 2 {
			served2++
			if r.Start >= 30 {
				t.Fatalf("packet %d of withdrawn flow started service at tick %d", r.PacketID, r.Start)
			}
		}
	}
	if served2+e.Stats.Withdrawn != 20 {
		t.Fatalf("flow2 packets not accounted: served=%d withdrawn=%d, total should be 20",
			served2, e.Stats.Withdrawn)
	}
	// Global conservation: nothing lost, nothing double counted.
	if e.Stats.Arrived != e.Stats.Served+e.Stats.Cancelled+e.Stats.Backpressured+e.Stats.Withdrawn+e.Sched.QueuedPackets() {
		t.Fatalf("conservation violated: %+v queued=%d", e.Stats, e.Sched.QueuedPackets())
	}
}

// A cancelled packet is never served and never counted twice; cancelling an
// in-service, already-cancelled, or unknown packet is a no-op.
func TestCancelNoDoubleCount(t *testing.T) {
	e := NewEngine(1)
	e.AddTenant(1, 1)
	e.AddFlow(1, 1, 1, 0)
	e.Arrive(0, 1, 1, 5)
	e.Arrive(0, 2, 1, 5)
	e.Arrive(0, 3, 1, 5)
	e.Submit(Event{Tick: 1, Type: EvCancel, PacketID: 2})   // queued: cancelled
	e.Submit(Event{Tick: 2, Type: EvCancel, PacketID: 2})   // already cancelled: no-op
	e.Submit(Event{Tick: 2, Type: EvCancel, PacketID: 1})   // in service: no-op
	e.Submit(Event{Tick: 3, Type: EvCancel, PacketID: 999}) // unknown: no-op
	e.Run(30)

	if e.Stats.Cancelled != 1 {
		t.Fatalf("cancelled count = %d, want exactly 1", e.Stats.Cancelled)
	}
	if e.Stats.Served != 2 || e.Stats.ServedBytes != 10 {
		t.Fatalf("served = %d (%d bytes), want 2 (10 bytes)", e.Stats.Served, e.Stats.ServedBytes)
	}
	for _, r := range e.Trace {
		if r.PacketID == 2 {
			t.Fatalf("cancelled packet 2 appears in service trace")
		}
	}
	if e.Stats.Arrived != e.Stats.Served+e.Stats.Cancelled+e.Stats.Backpressured+e.Stats.Withdrawn+e.Sched.QueuedPackets() {
		t.Fatalf("conservation violated: %+v", e.Stats)
	}
}
