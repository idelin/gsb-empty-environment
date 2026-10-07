package streamagg

import (
	"bytes"
	"encoding/json"
	"testing"
)

func mustGlobal(t *testing.T, a *Aggregator, want int64) {
	t.Helper()
	got, ok := a.Global()
	if !ok {
		t.Fatalf("global watermark not established, want %d", want)
	}
	if got != want {
		t.Fatalf("global watermark = %d, want %d", got, want)
	}
}

func drainLate(a *Aggregator) []Event {
	var out []Event
	for {
		select {
		case ev := <-a.Late():
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestGlobalIsMinOfActivePartitions(t *testing.T) {
	a := New(16)
	a.AddPartition("p1")
	a.AddPartition("p2")
	a.AddPartition("p3")

	// p3 has not reported yet: no global watermark.
	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 12}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Global(); ok {
		t.Fatal("global watermark established before all active partitions reported")
	}

	if _, err := a.OnEvent(Event{Partition: "p3", Timestamp: 8}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 8)

	if _, err := a.OnEvent(Event{Partition: "p3", Timestamp: 15}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 10) // min(10, 12, 15)
}

func TestIdlePartitionExcludedFromMin(t *testing.T) {
	a := New(16)
	a.AddPartition("p1")
	a.AddPartition("p2")

	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 50}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 5)

	// p1 stalls but is NOT marked idle: global watermark must not advance.
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 60}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 5)

	// Explicitly idling p1 excludes it: global jumps to p2's watermark.
	if err := a.MarkIdle("p1"); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 60)
}

func TestResumedPartitionLateEventsDoNotRegress(t *testing.T) {
	a := New(16)
	a.AddPartition("p1")
	a.AddPartition("p2")

	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 7}); err != nil {
		t.Fatal(err)
	}
	if err := a.MarkIdle("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 100}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 100)

	// Resume p1; its backlog events below the global watermark are late.
	if err := a.MarkActive("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 99}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 100)

	late := drainLate(a)
	if len(late) != 2 || late[0].Timestamp != 6 || late[1].Timestamp != 99 {
		t.Fatalf("late events = %+v, want timestamps [6 99]", late)
	}

	// Catching up past the global watermark resumes normal advancement.
	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 120}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 100) // min(120, 100)
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 130}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 120)
}

func TestAllIdleKeepsGlobalWatermark(t *testing.T) {
	a := New(16)
	a.AddPartition("p1")
	a.AddPartition("p2")

	if _, err := a.OnEvent(Event{Partition: "p1", Timestamp: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OnEvent(Event{Partition: "p2", Timestamp: 20}); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 10)

	if err := a.MarkIdle("p1"); err != nil {
		t.Fatal(err)
	}
	mustGlobal(t, a, 20)
	if err := a.MarkIdle("p2"); err != nil {
		t.Fatal(err)
	}
	// Every partition idle: the global watermark holds its previous value.
	mustGlobal(t, a, 20)
}

func TestUnknownPartitionRejected(t *testing.T) {
	a := New(16)
	if _, err := a.OnEvent(Event{Partition: "ghost", Timestamp: 1}); err != ErrUnknownPartition {
		t.Fatalf("err = %v, want ErrUnknownPartition", err)
	}
	if err := a.MarkIdle("ghost"); err != ErrUnknownPartition {
		t.Fatalf("err = %v, want ErrUnknownPartition", err)
	}
}

// step is one recorded action in an event log, replayable against any
// aggregator instance.
type step struct {
	kind string // "event", "idle", "active"
	ev   Event
}

func replay(t *testing.T, a *Aggregator, steps []step) []int64 {
	t.Helper()
	globals := make([]int64, 0, len(steps))
	for i, s := range steps {
		switch s.kind {
		case "event":
			if _, err := a.OnEvent(s.ev); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "idle":
			if err := a.MarkIdle(s.ev.Partition); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "active":
			if err := a.MarkActive(s.ev.Partition); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
		g, _ := a.Global()
		globals = append(globals, g)
	}
	return globals
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCheckpointReplay runs an event log covering pause, resume and the
// all-idle quiesce, checkpoints midway, restores into a fresh aggregator,
// and verifies the remaining replay reproduces the exact global watermark
// sequence and late-event stream.
func TestCheckpointReplay(t *testing.T) {
	setup := func() *Aggregator {
		a := New(64)
		a.AddPartition("p1")
		a.AddPartition("p2")
		a.AddPartition("p3")
		return a
	}

	before := []step{
		{kind: "event", ev: Event{Partition: "p1", Timestamp: 10}},
		{kind: "event", ev: Event{Partition: "p2", Timestamp: 30}},
		{kind: "event", ev: Event{Partition: "p3", Timestamp: 20}},
		{kind: "idle", ev: Event{Partition: "p2"}}, // pause p2
		{kind: "event", ev: Event{Partition: "p1", Timestamp: 40}},
		{kind: "event", ev: Event{Partition: "p3", Timestamp: 50}},
	}
	after := []step{
		{kind: "active", ev: Event{Partition: "p2"}},               // resume p2
		{kind: "event", ev: Event{Partition: "p2", Timestamp: 35}}, // late: below global 40
		{kind: "event", ev: Event{Partition: "p2", Timestamp: 60}},
		{kind: "idle", ev: Event{Partition: "p1"}},
		{kind: "idle", ev: Event{Partition: "p2"}},
		{kind: "idle", ev: Event{Partition: "p3"}}, // all idle: global holds
		{kind: "active", ev: Event{Partition: "p1"}},
		{kind: "event", ev: Event{Partition: "p1", Timestamp: 70}},
	}

	// Reference run: no checkpointing.
	ref := setup()
	wantBefore := replay(t, ref, before)
	cp := ref.Snapshot()
	var buf bytes.Buffer
	if err := ref.SaveCheckpoint(&buf); err != nil {
		t.Fatal(err)
	}
	wantAfter := replay(t, ref, after)
	wantLate := drainLate(ref)

	// Restored run: replay the same log across the checkpoint boundary.
	got := setup()
	gotBefore := replay(t, got, before)
	var buf2 bytes.Buffer
	if err := got.SaveCheckpoint(&buf2); err != nil {
		t.Fatal(err)
	}
	if buf.String() != buf2.String() {
		t.Fatalf("checkpoints diverge:\n%s\n---\n%s", buf.String(), buf2.String())
	}

	restored := New(64)
	if err := restored.LoadCheckpoint(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if g, ok := restored.Global(); !ok || g != 40 {
		t.Fatalf("restored global = %d, %v; want 40, true", g, ok)
	}
	idle, err := restored.IsIdle("p2")
	if err != nil || !idle {
		t.Fatalf("restored p2 idle = %v, %v; want true, nil", idle, err)
	}
	gotAfter := replay(t, restored, after)
	gotLate := drainLate(restored)

	if !equalInts(gotBefore, wantBefore) {
		t.Fatalf("pre-checkpoint globals = %v, want %v", gotBefore, wantBefore)
	}
	if !equalInts(gotAfter, wantAfter) {
		t.Fatalf("post-restore globals = %v, want %v", gotAfter, wantAfter)
	}
	if len(gotLate) != len(wantLate) {
		t.Fatalf("late events = %+v, want %+v", gotLate, wantLate)
	}
	for i := range wantLate {
		if gotLate[i] != wantLate[i] {
			t.Fatalf("late event %d = %+v, want %+v", i, gotLate[i], wantLate[i])
		}
	}

	// Checkpoint content: idle flags and watermarks survive the round trip.
	if len(cp.Partitions) != 3 {
		t.Fatalf("checkpoint partitions = %d, want 3", len(cp.Partitions))
	}
	if !cp.Partitions["p2"].Idle {
		t.Fatal("checkpoint lost p2 idle flag")
	}
	if cp.Partitions["p1"].Watermark != 40 || cp.Partitions["p3"].Watermark != 50 {
		t.Fatalf("checkpoint watermarks wrong: %+v", cp.Partitions)
	}
	// Deterministic encoding: checkpoint JSON is stable across snapshots.
	var raw Checkpoint
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Global != 40 || !raw.HasGlobal {
		t.Fatalf("decoded checkpoint global = %d, %v; want 40, true", raw.Global, raw.HasGlobal)
	}
}
