package sim

import (
	"math/big"
	"testing"
)

func mustApply(t *testing.T, s *Simulator, ev Event) {
	t.Helper()
	dup, err := s.Apply(ev)
	if err != nil {
		t.Fatalf("apply %+v: %v", ev, err)
	}
	if dup {
		t.Fatalf("apply %+v: unexpected duplicate", ev)
	}
}

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func TestSplitConservesAndAllocatesIntegers(t *testing.T) {
	s := New()
	mustApply(t, s, Event{ID: "r", Type: EventReceive, Source: "ore", Quantity: 10})
	mustApply(t, s, Event{ID: "sp", Type: EventSplit, Entity: "r/0", Parts: []int64{3, 7}})

	if _, gone := s.Entities["r/0"]; gone {
		t.Fatal("input entity should be consumed by split")
	}
	for id, want := range map[string]string{"sp/0": "3", "sp/1": "7"} {
		e := s.Entities[id]
		if e == nil {
			t.Fatalf("missing output %s", id)
		}
		if got := e.Sources["ore"]; got.Cmp(rat(t, want)) != 0 {
			t.Fatalf("%s share = %s, want %s", id, got, want)
		}
		if err := s.VerifyTrace(id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSplitRejectsNonConservingParts(t *testing.T) {
	s := New()
	mustApply(t, s, Event{ID: "r", Type: EventReceive, Source: "ore", Quantity: 10})
	before := len(s.Entities)
	if _, err := s.Apply(Event{ID: "bad", Type: EventSplit, Entity: "r/0", Parts: []int64{4, 4}}); err == nil {
		t.Fatal("expected conservation error")
	}
	if len(s.Entities) != before {
		t.Fatal("rejected event must not mutate state")
	}
	if len(s.Log) != 1 {
		t.Fatal("rejected event must not be logged")
	}
}

func TestMergePreservesSourceVector(t *testing.T) {
	s := New()
	mustApply(t, s, Event{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 5})
	mustApply(t, s, Event{ID: "b", Type: EventReceive, Source: "oreB", Quantity: 6})
	mustApply(t, s, Event{ID: "m", Type: EventMerge, Entities: []string{"a/0", "b/0"}})

	e := s.Entities["m/0"]
	if e.Quantity != 11 {
		t.Fatalf("merged quantity = %d, want 11", e.Quantity)
	}
	if e.Sources["oreA"].Cmp(rat(t, "5")) != 0 || e.Sources["oreB"].Cmp(rat(t, "6")) != 0 {
		t.Fatalf("merged vector = %s", e.Sources)
	}
}

func TestLossDeductsProportionallyWithRationals(t *testing.T) {
	s := New()
	mustApply(t, s, Event{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 1})
	mustApply(t, s, Event{ID: "b", Type: EventReceive, Source: "oreB", Quantity: 2})
	mustApply(t, s, Event{ID: "m", Type: EventMerge, Entities: []string{"a/0", "b/0"}})
	mustApply(t, s, Event{ID: "l", Type: EventLoss, Entity: "m/0", Quantity: 1})

	e := s.Entities["m/0"]
	if e.Quantity != 2 {
		t.Fatalf("quantity after loss = %d, want 2", e.Quantity)
	}
	// Shares 1*oreA and 2*oreB scaled by remaining/original = 2/3.
	if got := e.Sources["oreA"]; got.Cmp(rat(t, "2/3")) != 0 {
		t.Fatalf("oreA share = %s, want 2/3", got)
	}
	if got := e.Sources["oreB"]; got.Cmp(rat(t, "4/3")) != 0 {
		t.Fatalf("oreB share = %s, want 4/3", got)
	}
	if s.TotalLoss() != 1 || len(s.Losses) != 1 || s.Losses[0].EventID != "l" {
		t.Fatalf("loss ledger = %+v", s.Losses)
	}
	if err := s.VerifyTrace("m/0"); err != nil {
		t.Fatal(err)
	}
}

func TestTraceLinearCombination(t *testing.T) {
	s := New()
	mustApply(t, s, Event{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 10})
	mustApply(t, s, Event{ID: "b", Type: EventReceive, Source: "oreB", Quantity: 6})
	mustApply(t, s, Event{ID: "m", Type: EventMerge, Entities: []string{"a/0", "b/0"}})
	mustApply(t, s, Event{ID: "l", Type: EventLoss, Entity: "m/0", Quantity: 2})
	mustApply(t, s, Event{ID: "sp", Type: EventSplit, Entity: "m/0", Parts: []int64{9, 5}})

	line, err := s.Trace("sp/0")
	if err != nil {
		t.Fatal(err)
	}
	want := "sp/0 (qty 9) = 45/8*oreA + 27/8*oreB"
	if line != want {
		t.Fatalf("trace = %q, want %q", line, want)
	}
	for _, id := range []string{"sp/0", "sp/1"} {
		if err := s.VerifyTrace(id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

func TestReplayReproducesState(t *testing.T) {
	s := New()
	events := []Event{
		{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 10},
		{ID: "b", Type: EventReceive, Source: "oreB", Quantity: 6},
		{ID: "m", Type: EventMerge, Entities: []string{"a/0", "b/0"}},
		{ID: "l", Type: EventLoss, Entity: "m/0", Quantity: 2},
		{ID: "sp", Type: EventSplit, Entity: "m/0", Parts: []int64{9, 5}},
	}
	for _, ev := range events {
		mustApply(t, s, ev)
	}
	if err := s.VerifyReplay(); err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(s.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !stateEqual(s, replayed) {
		t.Fatal("replayed state differs")
	}
}

func TestDuplicateEventIsIdempotent(t *testing.T) {
	s := New()
	ev := Event{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 10}
	mustApply(t, s, ev)
	mustApply(t, s, Event{ID: "sp", Type: EventSplit, Entity: "a/0", Parts: []int64{4, 6}})

	snapshot := len(s.Entities)
	dup, err := s.Apply(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !dup {
		t.Fatal("second delivery should be reported as duplicate")
	}
	if len(s.Entities) != snapshot || len(s.Log) != 2 {
		t.Fatal("duplicate event mutated state or log")
	}
	// A duplicate split must not recreate or clobber its outputs either.
	if dup, err := s.Apply(Event{ID: "sp", Type: EventSplit, Entity: "a/0", Parts: []int64{4, 6}}); err != nil || !dup {
		t.Fatalf("dup=%v err=%v", dup, err)
	}
	if err := s.VerifyReplay(); err != nil {
		t.Fatal(err)
	}
}

func TestLogWithDuplicatesReplaysIdempotently(t *testing.T) {
	log := []Event{
		{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 10},
		{ID: "a", Type: EventReceive, Source: "oreA", Quantity: 10}, // redelivery
		{ID: "sp", Type: EventSplit, Entity: "a/0", Parts: []int64{4, 6}},
		{ID: "sp", Type: EventSplit, Entity: "a/0", Parts: []int64{4, 6}}, // redelivery
	}
	s, err := Replay(log)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Log) != 2 {
		t.Fatalf("log length = %d, want 2", len(s.Log))
	}
	if _, ok := s.Entities["a/0"]; ok {
		t.Fatal("duplicate receive should not resurrect consumed entity")
	}
	if s.Entities["sp/0"].Quantity != 4 || s.Entities["sp/1"].Quantity != 6 {
		t.Fatal("duplicate split should not double-apply")
	}
}
