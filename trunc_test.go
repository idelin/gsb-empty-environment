package logtrunc

import (
	"reflect"
	"testing"
)

func mustAddSegment(t *testing.T, p *Planner, id string, base, end uint64) {
	t.Helper()
	if err := p.AddSegment(Segment{ID: id, Base: base, End: end}); err != nil {
		t.Fatalf("AddSegment(%s): %v", id, err)
	}
}

func deletableIDs(plan Plan) []string {
	var out []string
	for _, s := range plan.Deletable {
		out = append(out, s.ID)
	}
	return out
}

// A gap in confirmations must not be masked by the maximum confirmed
// sequence number: only the contiguous prefix counts.
func TestGapNotMaskedByMaxSeqno(t *testing.T) {
	p := NewPlanner()
	mustAddSegment(t, p, "s0", 0, 4)
	mustAddSegment(t, p, "s1", 4, 8)
	p.AddConsumer("c", 0)

	for _, off := range []uint64{0, 1, 2, 5} {
		if err := p.Ack("c", off); err != nil {
			t.Fatalf("Ack(%d): %v", off, err)
		}
	}
	// Max confirmed seqno is 5, but offset 3 is missing.
	if w, _ := p.Watermark("c"); w != 3 {
		t.Fatalf("watermark = %d, want 3 (gap at 3 must not be masked by max seqno 5)", w)
	}
	plan := p.Plan()
	if got := deletableIDs(plan); len(got) != 0 {
		t.Fatalf("deletable = %v, want none: s0 end 4 exceeds contiguous watermark 3", got)
	}

	// Fill the gap at 3: prefix becomes [0,4), offset 5 still blocked by 4.
	if err := p.Ack("c", 3); err != nil {
		t.Fatal(err)
	}
	if w, _ := p.Watermark("c"); w != 4 {
		t.Fatalf("watermark = %d, want 4", w)
	}
	if got := deletableIDs(p.Plan()); !reflect.DeepEqual(got, []string{"s0"}) {
		t.Fatalf("deletable = %v, want [s0]", got)
	}

	// Fill offset 4: prefix extends across 5 as well.
	if err := p.Ack("c", 4); err != nil {
		t.Fatal(err)
	}
	if w, _ := p.Watermark("c"); w != 6 {
		t.Fatalf("watermark = %d, want 6", w)
	}
}

// Moving a consumer's watermark backwards is an ack rollback and must
// be rejected; duplicate acks below the watermark are harmless no-ops.
func TestAckRollbackRejected(t *testing.T) {
	p := NewPlanner()
	p.AddConsumer("c", 0)
	if err := p.SetWatermark("c", 6); err != nil {
		t.Fatalf("SetWatermark(6): %v", err)
	}
	err := p.SetWatermark("c", 4)
	if err == nil {
		t.Fatal("SetWatermark(4) after 6: want rollback error, got nil")
	}
	rb, ok := err.(*RollbackError)
	if !ok {
		t.Fatalf("error type = %T, want *RollbackError", err)
	}
	if rb.Current != 6 || rb.Requested != 4 {
		t.Fatalf("RollbackError = %+v, want current 6 requested 4", rb)
	}
	if w, _ := p.Watermark("c"); w != 6 {
		t.Fatalf("watermark = %d after rejected rollback, want 6", w)
	}

	// Re-acking an already confirmed offset must not move anything.
	if err := p.Ack("c", 2); err != nil {
		t.Fatalf("duplicate Ack: %v", err)
	}
	if w, _ := p.Watermark("c"); w != 6 {
		t.Fatalf("watermark = %d after duplicate ack, want 6", w)
	}
}

// When a consumer exits, its confirmations stop blocking truncation.
func TestConsumerExitUnblocks(t *testing.T) {
	p := NewPlanner()
	mustAddSegment(t, p, "s0", 0, 4)
	mustAddSegment(t, p, "s1", 4, 8)
	p.AddConsumer("fast", 8)
	p.AddConsumer("slow", 2)

	if got := deletableIDs(p.Plan()); len(got) != 0 {
		t.Fatalf("deletable = %v, want none while slow consumer is at 2", got)
	}
	plan := p.Plan()
	if len(plan.Retained) != 2 || len(plan.Retained[0].Evidence) != 1 ||
		plan.Retained[0].Evidence[0].Consumer != "slow" {
		t.Fatalf("retained evidence = %+v, want single blocker slow", plan.Retained)
	}

	p.RemoveConsumer("slow")
	if got := deletableIDs(p.Plan()); !reflect.DeepEqual(got, []string{"s0", "s1"}) {
		t.Fatalf("deletable after consumer exit = %v, want [s0 s1]", got)
	}
}

// Old snapshots pin historical segments even when every consumer has
// confirmed past them.
func TestSnapshotPinsHistoricalSegment(t *testing.T) {
	p := NewPlanner()
	mustAddSegment(t, p, "s0", 0, 4)
	mustAddSegment(t, p, "s1", 4, 8)
	p.AddConsumer("c", 8)
	p.AddSnapshot(Snapshot{ID: "snap1", Segments: map[string]struct{}{"s0": {}}})

	plan := p.Plan()
	if got := deletableIDs(plan); !reflect.DeepEqual(got, []string{"s1"}) {
		t.Fatalf("deletable = %v, want [s1]: snapshot pins s0", got)
	}
	if len(plan.Retained) != 1 || len(plan.Retained[0].Evidence) != 1 ||
		plan.Retained[0].Evidence[0].Snapshot != "snap1" {
		t.Fatalf("retained evidence = %+v, want single blocker snap1", plan.Retained)
	}

	p.RemoveSnapshot("snap1")
	if got := deletableIDs(p.Plan()); !reflect.DeepEqual(got, []string{"s0", "s1"}) {
		t.Fatalf("deletable after snapshot removal = %v, want [s0 s1]", got)
	}
}

// The minimal blocking evidence is exactly one blocker, chosen
// deterministically, even when several blockers exist.
func TestMinimalBlockingEvidence(t *testing.T) {
	p := NewPlanner()
	mustAddSegment(t, p, "s0", 0, 4)
	p.AddConsumer("b", 1)
	p.AddConsumer("a", 1) // same watermark as b: tie broken by name
	p.AddConsumer("z", 3)
	p.AddSnapshot(Snapshot{ID: "snap2", Segments: map[string]struct{}{"s0": {}}})
	p.AddSnapshot(Snapshot{ID: "snap1", Segments: map[string]struct{}{"s0": {}}})

	plan := p.Plan()
	if len(plan.Retained) != 1 {
		t.Fatalf("retained = %+v, want exactly one segment", plan.Retained)
	}
	ev := plan.Retained[0].Evidence
	if len(ev) != 1 || ev[0].Snapshot != "snap1" {
		t.Fatalf("evidence = %+v, want single snapshot blocker snap1 (smallest ID)", ev)
	}

	// Without snapshots, the blocker is the lowest-watermark consumer,
	// ties broken by name.
	p.RemoveSnapshot("snap1")
	p.RemoveSnapshot("snap2")
	ev = p.Plan().Retained[0].Evidence
	if len(ev) != 1 || ev[0].Consumer != "a" {
		t.Fatalf("evidence = %+v, want single consumer blocker a", ev)
	}

	// AllBlockers still reports the full set for diagnostics.
	consumers, snapshots := p.AllBlockers("s0")
	if !reflect.DeepEqual(consumers, []string{"a", "b", "z"}) {
		t.Fatalf("AllBlockers consumers = %v, want [a b z]", consumers)
	}
	if len(snapshots) != 0 {
		t.Fatalf("AllBlockers snapshots = %v, want none", snapshots)
	}
}

// With no consumers registered there is no consumer dependency, so
// only snapshots can retain a segment.
func TestNoConsumersOnlySnapshotsBlock(t *testing.T) {
	p := NewPlanner()
	mustAddSegment(t, p, "s0", 0, 4)
	mustAddSegment(t, p, "s1", 4, 8)
	p.AddSnapshot(Snapshot{ID: "snap1", Segments: map[string]struct{}{"s1": {}}})
	plan := p.Plan()
	if plan.HasConsumers {
		t.Fatal("HasConsumers = true, want false")
	}
	if got := deletableIDs(plan); !reflect.DeepEqual(got, []string{"s0"}) {
		t.Fatalf("deletable = %v, want [s0]: no consumer dependency remains", got)
	}
	if len(plan.Retained) != 1 || len(plan.Retained[0].Evidence) != 1 ||
		plan.Retained[0].Evidence[0].Snapshot != "snap1" {
		t.Fatalf("retained = %+v, want s1 retained by snap1", plan.Retained)
	}
}
