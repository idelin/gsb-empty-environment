package streamagg

import (
	"reflect"
	"testing"
)

func mustGlobal(t *testing.T, a *Aggregator, want int64) {
	t.Helper()
	got, ok := a.GlobalWatermark()
	if !ok {
		t.Fatalf("global watermark not established, want %d", want)
	}
	if got != want {
		t.Fatalf("global watermark = %d, want %d", got, want)
	}
}

func TestGlobalIsMinOfActivePartitions(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p1", Timestamp: 50})
	mustGlobal(t, a, 50)

	// A new partition above the current global does not change the min.
	a.OnEvent(Event{Partition: "p2", Timestamp: 100})
	mustGlobal(t, a, 50)

	// Advancing the lagging partition lifts the global watermark to the
	// new minimum of the active partitions.
	a.OnEvent(Event{Partition: "p1", Timestamp: 120})
	mustGlobal(t, a, 100)

	// Out-of-order but on-time events never move a partition watermark
	// backwards, so the global min is unaffected.
	a.OnEvent(Event{Partition: "p1", Timestamp: 115})
	mustGlobal(t, a, 100)
}

func TestIdlePartitionExcludedFromMin(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p2", Timestamp: 50})
	a.OnEvent(Event{Partition: "p1", Timestamp: 100})
	mustGlobal(t, a, 50)

	a.MarkIdle("p2")
	mustGlobal(t, a, 100)
	if !a.IsIdle("p2") {
		t.Fatal("p2 should be marked idle")
	}
}

func TestAllPartitionsIdleKeepsGlobalValue(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p2", Timestamp: 80})
	a.OnEvent(Event{Partition: "p1", Timestamp: 100})
	mustGlobal(t, a, 80)

	a.MarkIdle("p1")
	a.MarkIdle("p2")
	mustGlobal(t, a, 80)
}

func TestResumedPartitionLateEventsGoToLateChannel(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p2", Timestamp: 50})
	a.OnEvent(Event{Partition: "p1", Timestamp: 100})
	a.MarkIdle("p2")
	mustGlobal(t, a, 100)

	a.MarkActive("p2")
	// Resuming a lagging partition must not drag the global watermark down.
	mustGlobal(t, a, 100)

	// Events behind the global watermark are diverted to the late channel.
	a.OnEvent(Event{Partition: "p2", Timestamp: 70, Value: 1})
	a.OnEvent(Event{Partition: "p2", Timestamp: 99, Value: 2})
	mustGlobal(t, a, 100)

	late := a.DrainLate()
	want := []Event{
		{Partition: "p2", Timestamp: 70, Value: 1},
		{Partition: "p2", Timestamp: 99, Value: 2},
	}
	if !reflect.DeepEqual(late, want) {
		t.Fatalf("late events = %+v, want %+v", late, want)
	}

	// An event at or beyond the global watermark is on time and lets the
	// partition catch up.
	a.OnEvent(Event{Partition: "p2", Timestamp: 130})
	if got := a.DrainLate(); len(got) != 0 {
		t.Fatalf("unexpected late events: %+v", got)
	}
	a.OnEvent(Event{Partition: "p1", Timestamp: 140})
	mustGlobal(t, a, 130)
}

func TestGlobalNeverRegresses(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p1", Timestamp: 200})
	mustGlobal(t, a, 200)

	// A brand-new partition joining behind the global watermark cannot
	// pull it back; its lagging events go to the late channel.
	a.OnEvent(Event{Partition: "p2", Timestamp: 10})
	mustGlobal(t, a, 200)
	if got := a.DrainLate(); len(got) != 1 || got[0].Timestamp != 10 {
		t.Fatalf("late events = %+v, want the ts=10 event", got)
	}
}

func TestCheckpointRoundTripPreservesIdleAndWatermarks(t *testing.T) {
	a := NewAggregator()
	a.OnEvent(Event{Partition: "p2", Timestamp: 60})
	a.OnEvent(Event{Partition: "p1", Timestamp: 100})
	a.MarkIdle("p2")
	a.OnEvent(Event{Partition: "p1", Timestamp: 150})

	data, err := EncodeCheckpoint(a.Snapshot())
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	cp, err := DecodeCheckpoint(data)
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}

	if !cp.Partitions["p2"].Idle {
		t.Fatal("checkpoint lost the idle flag for p2")
	}
	if cp.Partitions["p1"].Watermark != 150 || cp.Partitions["p2"].Watermark != 60 {
		t.Fatalf("checkpoint watermarks wrong: %+v", cp.Partitions)
	}

	b := Restore(cp)
	mustGlobal(t, b, 150)
	if !b.IsIdle("p2") {
		t.Fatal("restored aggregator lost idle state for p2")
	}

	// Behaviour continues identically after restore: resuming p2 keeps the
	// global watermark and diverts its lagging events to the late channel.
	b.MarkActive("p2")
	mustGlobal(t, b, 150)
	b.OnEvent(Event{Partition: "p2", Timestamp: 120})
	if got := b.DrainLate(); len(got) != 1 || got[0].Timestamp != 120 {
		t.Fatalf("late events after restore = %+v", got)
	}
}

// replayScenario exercises pause, resume and the all-partitions-idle case.
func replayScenario() []Op {
	return []Op{
		{Kind: OpEvent, Partition: "p2", Timestamp: 60},
		{Kind: OpEvent, Partition: "p3", Timestamp: 90},
		{Kind: OpEvent, Partition: "p1", Timestamp: 100},
		{Kind: OpIdle, Partition: "p2"},                  // pause lagging partition
		{Kind: OpEvent, Partition: "p1", Timestamp: 130}, // global advances past p2
		{Kind: OpIdle, Partition: "p1"},                  // pause another
		{Kind: OpIdle, Partition: "p3"},                  // all idle: keep value
		{Kind: OpActive, Partition: "p2"},                // resume lagging partition
		{Kind: OpEvent, Partition: "p2", Timestamp: 70},  // late -> late channel
		{Kind: OpActive, Partition: "p1"},                // resume
		{Kind: OpActive, Partition: "p3"},                // resume
		{Kind: OpEvent, Partition: "p2", Timestamp: 200}, // catch up
		{Kind: OpEvent, Partition: "p1", Timestamp: 210},
		{Kind: OpEvent, Partition: "p3", Timestamp: 220},
	}
}

func TestReplayFromCheckpointMatchesFullReplay(t *testing.T) {
	ops := replayScenario()

	full := Replay(ops)
	wantGlobal, wantOK := full.GlobalWatermark()
	wantLate := full.DrainLate()
	wantCp := full.Snapshot()

	// Every split point: run the prefix live, snapshot, restore, replay the
	// rest — the final state must match the uninterrupted run. Late events
	// emitted before the snapshot belong to the prefix run, so they are
	// collected before restoring.
	for split := 0; split <= len(ops); split++ {
		live := Replay(ops[:split])
		prefixLate := live.DrainLate()
		cp := live.Snapshot()
		data, err := EncodeCheckpoint(cp)
		if err != nil {
			t.Fatalf("split %d: encode: %v", split, err)
		}
		decoded, err := DecodeCheckpoint(data)
		if err != nil {
			t.Fatalf("split %d: decode: %v", split, err)
		}
		replayed := ReplayFrom(decoded, ops[split:])

		gotGlobal, gotOK := replayed.GlobalWatermark()
		gotLate := append(prefixLate, replayed.DrainLate()...)
		gotCp := replayed.Snapshot()

		if gotGlobal != wantGlobal || gotOK != wantOK {
			t.Fatalf("split %d: global = (%d, %v), want (%d, %v)", split, gotGlobal, gotOK, wantGlobal, wantOK)
		}
		if !reflect.DeepEqual(gotLate, wantLate) {
			t.Fatalf("split %d: late = %+v, want %+v", split, gotLate, wantLate)
		}
		if !reflect.DeepEqual(gotCp, wantCp) {
			t.Fatalf("split %d: checkpoint = %+v, want %+v", split, gotCp, wantCp)
		}
	}
}

func TestReplayPauseResumeAndAllIdle(t *testing.T) {
	ops := replayScenario()

	// Step through the scenario asserting the global watermark at the
	// interesting points: pause excludes, all-idle holds, resume keeps.
	a := NewAggregator()
	for i, op := range ops {
		a.Apply(op)
		switch i {
		case 2: // p2=60, p3=90, p1=100
			mustGlobal(t, a, 60)
		case 3: // p2 paused
			mustGlobal(t, a, 90)
		case 4: // p1 advances to 130
			mustGlobal(t, a, 90)
		case 6: // all idle: value kept
			mustGlobal(t, a, 90)
		case 7: // p2 resumed, still lagging: no regression
			mustGlobal(t, a, 90)
		case 8: // p2's ts=70 event is late
			mustGlobal(t, a, 90)
			if late := a.DrainLate(); len(late) != 1 || late[0].Timestamp != 70 {
				t.Fatalf("late = %+v, want ts=70 event", late)
			}
		case 13: // everyone caught up
			mustGlobal(t, a, 200)
		}
	}
}
