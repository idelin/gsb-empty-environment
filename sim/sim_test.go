package sim

import (
	"strings"
	"testing"
)

// handler draws randomness only from the target entity's own stream.
func handler(eng *Engine, ev Event) error {
	e := eng.Entity(ev.EntityID)
	if e == nil {
		return nil
	}
	switch ev.Kind {
	case "accrue":
		e.Add("balance", ev.Amount+e.Drawn(10))
	case "settle":
		e.Add("balance", -int64(e.Draw()%5))
	}
	return nil
}

func buildEngine(t *testing.T, ids ...string) *Engine {
	t.Helper()
	eng := NewEngine(42, handler)
	for _, id := range ids {
		if _, err := eng.AddEntity(id); err != nil {
			t.Fatalf("AddEntity(%s): %v", id, err)
		}
	}
	return eng
}

func runEvents(t *testing.T, eng *Engine, evs []Event) {
	t.Helper()
	for _, ev := range evs {
		if err := eng.Schedule(ev); err != nil {
			t.Fatalf("Schedule(%+v): %v", ev, err)
		}
	}
	if err := eng.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// After a checkpoint fork, feeding both branches the same events must
// produce identical state and identical random-call counts.
func TestForkReplayDeterminism(t *testing.T) {
	base := buildEngine(t, "alice", "bob")
	runEvents(t, base, []Event{
		{Time: 1, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 5},
		{Time: 2, Seq: 1, EntityID: "bob", Kind: "accrue", Amount: 7},
	})

	blob, err := base.Save()
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	branchA, err := Load(blob, handler)
	if err != nil {
		t.Fatalf("Load A: %v", err)
	}
	branchB, err := Load(blob, handler)
	if err != nil {
		t.Fatalf("Load B: %v", err)
	}

	post := []Event{
		{Time: 3, Seq: 1, EntityID: "alice", Kind: "settle"},
		{Time: 3, Seq: 2, EntityID: "bob", Kind: "accrue", Amount: 3},
		{Time: 4, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 1},
	}
	runEvents(t, branchA, post)
	runEvents(t, branchB, post)

	if diffs := Diff(branchA, branchB); len(diffs) != 0 {
		t.Fatalf("forked branches diverged:\n%s", strings.Join(diffs, "\n"))
	}
	for _, id := range branchA.EntityIDs() {
		if branchA.Entity(id).RandCalls() == 0 {
			t.Fatalf("entity %s consumed no randomness; test is vacuous", id)
		}
	}
}

// Adding an unrelated entity must not change existing entities' random
// sequences or resulting state.
func TestUnrelatedEntityDoesNotPerturbStreams(t *testing.T) {
	small := buildEngine(t, "alice", "bob")
	large := buildEngine(t, "alice", "bob", "mallory", "zed")

	evs := []Event{
		{Time: 1, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 5},
		{Time: 1, Seq: 2, EntityID: "bob", Kind: "settle"},
		{Time: 2, Seq: 1, EntityID: "alice", Kind: "settle"},
	}
	runEvents(t, small, evs)
	runEvents(t, large, evs)

	for _, id := range []string{"alice", "bob"} {
		a, b := small.Entity(id), large.Entity(id)
		if diffs := diffEntity(id, a, b); len(diffs) != 0 {
			t.Fatalf("entity %s changed after unrelated adds:\n%s", id, strings.Join(diffs, "\n"))
		}
	}
}

// Event order is explicit: Seq breaks ties at equal Time, and duplicate
// or past keys are rejected.
func TestExplicitEventOrdering(t *testing.T) {
	eng := buildEngine(t, "alice")
	// Delivered out of order; Seq must decide execution order.
	runEvents(t, eng, []Event{
		{Time: 5, Seq: 2, EntityID: "alice", Kind: "accrue", Amount: 100},
		{Time: 5, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 1},
	})

	ref := buildEngine(t, "alice")
	runEvents(t, ref, []Event{
		{Time: 5, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 1},
		{Time: 5, Seq: 2, EntityID: "alice", Kind: "accrue", Amount: 100},
	})
	if diffs := Diff(eng, ref); len(diffs) != 0 {
		t.Fatalf("same (Time, Seq) set replayed differently:\n%s", strings.Join(diffs, "\n"))
	}

	dup := buildEngine(t, "alice")
	if err := dup.Schedule(Event{Time: 1, Seq: 1, EntityID: "alice"}); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	if err := dup.Schedule(Event{Time: 1, Seq: 1, EntityID: "bob"}); err == nil {
		t.Fatal("duplicate (Time, Seq) accepted")
	}
	if err := dup.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := dup.Schedule(Event{Time: 0, Seq: 9, EntityID: "alice"}); err == nil {
		t.Fatal("past event accepted")
	}
}

// Two branches loaded from the same checkpoint and replayed with the
// same script must match on both state and per-entity rand call counts;
// a divergent script must be detected.
func TestTwoBranchReplayComparison(t *testing.T) {
	base := buildEngine(t, "alice", "bob", "carol")
	runEvents(t, base, []Event{
		{Time: 1, Seq: 1, EntityID: "carol", Kind: "accrue", Amount: 9},
	})
	blob, err := base.Save()
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	load := func(t *testing.T) *Engine {
		eng, err := Load(blob, handler)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return eng
	}

	script := []Event{
		{Time: 2, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 4},
		{Time: 2, Seq: 2, EntityID: "bob", Kind: "settle"},
		{Time: 3, Seq: 1, EntityID: "carol", Kind: "settle"},
	}
	left, right := load(t), load(t)
	runEvents(t, left, script)
	runEvents(t, right, script)
	if diffs := Diff(left, right); len(diffs) != 0 {
		t.Fatalf("identical replay diverged:\n%s", strings.Join(diffs, "\n"))
	}

	// Sanity: a changed script must be caught, including rand counts.
	divergent := load(t)
	runEvents(t, divergent, []Event{
		{Time: 2, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 4},
		{Time: 2, Seq: 2, EntityID: "bob", Kind: "settle"},
		{Time: 3, Seq: 1, EntityID: "carol", Kind: "noop"},
	})
	diffs := Diff(left, divergent)
	if len(diffs) == 0 {
		t.Fatal("divergent script not detected")
	}
	foundCallDiff := false
	for _, d := range diffs {
		if strings.Contains(d, "rand calls") {
			foundCallDiff = true
		}
	}
	if !foundCallDiff {
		t.Fatalf("expected a rand-call-count diff, got: %v", diffs)
	}
}

// Checkpoints from unknown format versions must be rejected.
func TestUnknownCheckpointVersionRejected(t *testing.T) {
	eng := buildEngine(t, "alice")
	blob, err := eng.Save()
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := Load(blob, handler); err != nil {
		t.Fatalf("current-version checkpoint rejected: %v", err)
	}

	for _, raw := range []string{
		`{"version":0,"seed":42}`,
		`{"version":2,"seed":42}`,
		`{"version":-1,"seed":42}`,
		`{"seed":42}`,
	} {
		if _, err := Load([]byte(raw), handler); err == nil {
			t.Fatalf("checkpoint %s accepted, want version error", raw)
		}
	}
	if _, err := Load([]byte(`not json`), handler); err == nil {
		t.Fatal("garbage checkpoint accepted")
	}
}
