package sim

import (
	"reflect"
	"testing"
)

// buildScriptedEngine builds an engine with a mixed script: two tenants,
// three flows, arrivals at several ticks, a flow weight change, a tenant
// weight change and a cancellation.
func buildScriptedEngine() *Engine {
	e := NewEngine(1)
	e.AddTenant(1, 2)
	e.AddTenant(2, 1)
	e.AddFlow(1, 1, 1, 0)
	e.AddFlow(2, 1, 2, 0)
	e.AddFlow(3, 2, 1, 0)
	burst(e, 0, 1, 1, 10, 7)
	burst(e, 0, 2, 101, 10, 3)
	burst(e, 0, 3, 201, 10, 5)
	burst(e, 50, 1, 11, 5, 7)
	e.Submit(Event{Tick: 40, Type: EvCancel, PacketID: 203})
	e.Submit(Event{Tick: 80, Type: EvFlowWeight, FlowID: 2, Weight: 1})
	e.Submit(Event{Tick: 120, Type: EvTenantWeight, TenantID: 2, Weight: 2})
	return e
}

// A snapshot taken mid-run must restore to a state that continues
// bit-identically to an uninterrupted run, and must be reusable.
func TestSnapshotRestoreIdenticalTrace(t *testing.T) {
	full := buildScriptedEngine()
	full.Run(300)

	part := buildScriptedEngine()
	part.Run(100)
	data, err := part.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored, err := Restore(data)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored.Run(300)

	if !reflect.DeepEqual(full.Trace, restored.Trace) {
		t.Fatalf("trace diverged after restore\nfull:     %v\nrestored: %v", full.Trace, restored.Trace)
	}
	if full.Stats != restored.Stats {
		t.Fatalf("stats diverged after restore: %+v vs %+v", full.Stats, restored.Stats)
	}

	// The same snapshot must be restorable more than once.
	again, err := Restore(data)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	again.Run(300)
	if !reflect.DeepEqual(full.Trace, again.Trace) {
		t.Fatalf("second restore diverged")
	}
}

// Events at the same tick are processed in a fixed order: weight changes
// before arrivals. A packet arriving at the same tick as a weight change on
// its flow must be timed with the NEW weight.
func TestSameTickOrdering(t *testing.T) {
	build := func() *Engine {
		e := NewEngine(1)
		e.AddTenant(1, 1)
		e.AddFlow(1, 1, 1, 0)
		e.AddFlow(2, 1, 1, 0)
		e.Submit(Event{Tick: 10, Type: EvFlowWeight, FlowID: 1, Weight: 2})
		e.Arrive(10, 10, 1, 4) // with new weight 2: finish = 2
		e.Arrive(10, 5, 2, 3)  // weight 1: finish = 3
		return e
	}
	e1 := build()
	e1.Run(30)
	e2 := build()
	e2.Run(30)

	if !reflect.DeepEqual(e1.Trace, e2.Trace) {
		t.Fatalf("same script produced different traces")
	}
	if len(e1.Trace) != 2 || e1.Trace[0].PacketID != 10 {
		t.Fatalf("weight change at same tick not applied before arrival: trace=%v", e1.Trace)
	}
}

// Backpressure, same-tick ordering and snapshot recovery exercised together:
// a full queue rejects arrivals; a same-tick cancel frees a slot BEFORE the
// next arrival is admitted; and a mid-run snapshot restores identically.
func TestBackpressureOrderingRecovery(t *testing.T) {
	build := func() *Engine {
		e := NewEngine(1)
		e.AddTenant(1, 1)
		e.AddFlow(1, 1, 1, 2) // queue capacity: 2 packets
		e.Arrive(0, 1, 1, 10)
		e.Arrive(1, 2, 1, 10)
		e.Arrive(2, 3, 1, 10)
		e.Arrive(3, 4, 1, 10) // queue full ([2,3]): backpressured
		// Same tick: the cancel is processed before the arrival, freeing a
		// slot so packet 5 is admitted instead of backpressured.
		e.Submit(Event{Tick: 5, Type: EvCancel, PacketID: 3})
		e.Arrive(5, 5, 1, 10)
		return e
	}

	full := build()
	full.Run(100)

	if full.Stats.Arrived != 5 || full.Stats.Served != 3 ||
		full.Stats.Cancelled != 1 || full.Stats.Backpressured != 1 {
		t.Fatalf("unexpected stats: %+v", full.Stats)
	}
	served := map[int]bool{}
	for _, r := range full.Trace {
		served[r.PacketID] = true
	}
	if !served[1] || !served[2] || !served[5] || served[3] || served[4] {
		t.Fatalf("served set = %v, want {1,2,5}", served)
	}

	// Recover mid-run (packet 1 still in service at tick 6) and compare.
	part := build()
	part.Run(6)
	data, err := part.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := Restore(data)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored.Run(100)
	if !reflect.DeepEqual(full.Trace, restored.Trace) || full.Stats != restored.Stats {
		t.Fatalf("restored run diverged: trace %v vs %v, stats %+v vs %+v",
			full.Trace, restored.Trace, full.Stats, restored.Stats)
	}
}
