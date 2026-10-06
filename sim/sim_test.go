package sim

import (
	"strings"
	"testing"
)

func ob(at, seq, value int) Observation {
	return Observation{Key: ObsKey{Time: at, Seq: seq}, Value: value}
}

func TestFirstTouchFreezesConclusion(t *testing.T) {
	e := NewEngine(-10, 10)
	if e.Ingest(ob(1, 1, 4)) {
		t.Fatal("unexpected stop at step 1")
	}
	if !e.Ingest(ob(2, 1, 7)) {
		t.Fatal("expected stop at step 2, sum 11 >= 10")
	}
	c := e.Conclusion()
	if !c.Stopped || c.Boundary != BoundaryUpper || c.Sum != 11 || c.Steps != 2 {
		t.Fatalf("bad conclusion %+v", c)
	}
	if c.Evidence != (ObsKey{Time: 2, Seq: 1}) {
		t.Fatalf("bad stop evidence %+v", c.Evidence)
	}
	// Data arriving after the stop must not change the conclusion.
	for _, o := range []Observation{ob(3, 1, -100), ob(4, 1, 50)} {
		if e.Ingest(o) {
			t.Fatal("stop fired twice")
		}
	}
	if got := e.Conclusion(); !got.Equal(c) {
		t.Fatalf("conclusion changed after stop: %+v -> %+v", c, got)
	}
	if e.Sum() != 11 || e.Steps() != 2 {
		t.Fatalf("state mutated after stop: sum=%d steps=%d", e.Sum(), e.Steps())
	}
}

func TestLowerEdgeTouch(t *testing.T) {
	c := Recompute(-8, 8, []Observation{ob(1, 1, -3), ob(2, 1, -6)})
	if !c.Stopped || c.Boundary != BoundaryLower || c.Sum != -9 {
		t.Fatalf("bad conclusion %+v", c)
	}
}

func TestSameTimeOrderedBySeq(t *testing.T) {
	store := NewFactStore()
	// Arrive out of order; processing must follow (Time, Seq).
	store.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 2}, Value: 20})
	store.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: -12})
	latest := store.Latest()
	if latest[0].Key.Seq != 1 || latest[1].Key.Seq != 2 {
		t.Fatalf("wrong order %+v", latest)
	}
	c := Recompute(-10, 10, latest)
	// seq 1 first: sum -12 touches the lower edge immediately.
	if !c.Stopped || c.Boundary != BoundaryLower || c.Evidence.Seq != 1 {
		t.Fatalf("bad conclusion %+v", c)
	}
}

func TestDuplicateObservationIdempotent(t *testing.T) {
	s := NewSimulator(-10, 10)
	ev := Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: 6}
	s.Apply(ev)
	s.Apply(ev) // duplicate delivery
	s.Apply(ev)
	if s.Live.Sum() != 6 || s.Live.Steps() != 1 {
		t.Fatalf("duplicate mutated state: sum=%d steps=%d", s.Live.Sum(), s.Live.Steps())
	}
	if versions := s.Store.Versions(ev.Key); len(versions) != 1 {
		t.Fatalf("duplicate created fact versions %+v", versions)
	}
}

func TestPrefixExhaustiveAndExactlyOnce(t *testing.T) {
	// Deterministic pseudo-random stream crossing both edges' vicinity.
	var obs []Observation
	seed := 12345
	for i := 0; i < 300; i++ {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		obs = append(obs, ob(i, 1, seed%17-8))
	}
	if err := VerifyPrefixExhaustive(-25, 25, obs); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRecovery(t *testing.T) {
	var obs []Observation
	seed := 999
	for i := 0; i < 120; i++ {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		obs = append(obs, ob(i, 1, seed%11-5))
	}
	if err := VerifyCheckpointRecovery(-15, 15, obs); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointPreservesStopEvidence(t *testing.T) {
	e := NewEngine(-10, 10)
	e.Ingest(ob(1, 1, 8))
	e.Ingest(ob(2, 1, 5))
	cp := e.Save()
	if !cp.Conclusion.Stopped || cp.Conclusion.Evidence != (ObsKey{Time: 2, Seq: 1}) {
		t.Fatalf("checkpoint lost stop evidence %+v", cp.Conclusion)
	}
	r := Restore(-10, 10, cp)
	// Recovered engine stays frozen and still suppresses duplicates.
	if r.Ingest(ob(3, 1, -50)) || r.Ingest(ob(1, 1, 8)) {
		t.Fatal("recovered engine accepted data after stop")
	}
	if !r.Conclusion().Equal(cp.Conclusion) {
		t.Fatalf("recovered conclusion %+v != %+v", r.Conclusion(), cp.Conclusion)
	}
}

func TestRevisionMovesStopLaterAndCompensates(t *testing.T) {
	s := NewSimulator(-10, 10)
	s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: 6})
	entries := s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 2, Seq: 1}, Value: 5})
	if len(entries) != 1 || entries[0].Kind != ActionStopUpper {
		t.Fatalf("expected executed stop action, got %+v", entries)
	}
	executed := entries[0]

	// Late revision: 5 -> 1 erases the touch (sum 7 < 10).
	comp := s.Apply(Event{Kind: EventRevise, Key: ObsKey{Time: 2, Seq: 1}, Value: 1})
	if len(comp) != 1 || comp[0].Kind != ActionCompensate {
		t.Fatalf("expected compensation suggestion, got %+v", comp)
	}
	if comp[0].Compensates != executed.Version {
		t.Fatalf("compensation targets v%d, want v%d", comp[0].Compensates, executed.Version)
	}
	if !strings.Contains(comp[0].Reason, "erased") {
		t.Fatalf("unexpected reason %q", comp[0].Reason)
	}

	// The original executed action must be untouched.
	ledger := s.Ledger.Entries()
	if len(ledger) != 2 || !ledger[0].Conclusion.Equal(executed.Conclusion) || ledger[0].Kind != ActionStopUpper {
		t.Fatalf("ledger rewritten: %+v", ledger)
	}

	// Fact versions, recomputed conclusions and action versions coexist.
	if versions := s.Store.Versions(ObsKey{Time: 2, Seq: 1}); len(versions) != 2 || versions[1].Value != 1 {
		t.Fatalf("bad fact versions %+v", versions)
	}
	last := s.Conclusions[len(s.Conclusions)-1]
	if last.Stopped {
		t.Fatalf("recomputed conclusion should not be stopped: %+v", last)
	}

	// New data resumes from the recomputed state and stops later.
	entries = s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 3, Seq: 1}, Value: 8})
	if len(entries) != 1 || entries[0].Kind != ActionStopUpper || entries[0].Conclusion.Steps != 3 {
		t.Fatalf("expected new stop at step 3, got %+v", entries)
	}
}

func TestRevisionMovesStopEarlier(t *testing.T) {
	s := NewSimulator(-10, 10)
	s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: 3})
	s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 2, Seq: 1}, Value: 4})
	entries := s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 3, Seq: 1}, Value: 6})
	if len(entries) != 1 || entries[0].Conclusion.Steps != 3 {
		t.Fatalf("expected stop at step 3, got %+v", entries)
	}

	// Revision makes the cumulative sum touch at step 2 instead.
	comp := s.Apply(Event{Kind: EventRevise, Key: ObsKey{Time: 1, Seq: 1}, Value: 9})
	if len(comp) != 1 || comp[0].Kind != ActionCompensate {
		t.Fatalf("expected compensation, got %+v", comp)
	}
	if !strings.Contains(comp[0].Reason, "earlier") {
		t.Fatalf("unexpected reason %q", comp[0].Reason)
	}
	last := s.Conclusions[len(s.Conclusions)-1]
	if !last.Stopped || last.Steps != 2 || last.Evidence != (ObsKey{Time: 2, Seq: 1}) {
		t.Fatalf("stop did not move earlier: %+v", last)
	}
	// Live engine follows the recomputed truth: further data is ignored.
	if got := s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 4, Seq: 1}, Value: 100}); len(got) != 0 {
		t.Fatalf("post-stop data produced entries %+v", got)
	}
	if s.Live.Steps() != 2 {
		t.Fatalf("live engine not frozen at recomputed stop, steps=%d", s.Live.Steps())
	}
}

func TestRevisionFlipsBoundary(t *testing.T) {
	s := NewSimulator(-10, 10)
	entries := s.Apply(Event{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: 12})
	if len(entries) != 1 || entries[0].Kind != ActionStopUpper {
		t.Fatalf("expected upper stop, got %+v", entries)
	}
	comp := s.Apply(Event{Kind: EventRevise, Key: ObsKey{Time: 1, Seq: 1}, Value: -12})
	if len(comp) != 1 || !strings.Contains(comp[0].Reason, "flipped") {
		t.Fatalf("expected flip compensation, got %+v", comp)
	}
	last := s.Conclusions[len(s.Conclusions)-1]
	if last.Boundary != BoundaryLower {
		t.Fatalf("expected lower conclusion, got %+v", last)
	}
}

func TestEventReplayReproducesState(t *testing.T) {
	events := []Event{
		{Kind: EventObserve, Key: ObsKey{Time: 1, Seq: 1}, Value: 6},
		{Kind: EventObserve, Key: ObsKey{Time: 2, Seq: 1}, Value: 5},
		{Kind: EventObserve, Key: ObsKey{Time: 2, Seq: 1}, Value: 5}, // duplicate
		{Kind: EventRevise, Key: ObsKey{Time: 2, Seq: 1}, Value: 1},
		{Kind: EventObserve, Key: ObsKey{Time: 3, Seq: 1}, Value: 8},
		{Kind: EventRevise, Key: ObsKey{Time: 1, Seq: 1}, Value: 9},
		{Kind: EventRevise, Key: ObsKey{Time: 9, Seq: 9}, Value: 1}, // unknown key
	}
	a := Replay(-10, 10, events)
	b := Replay(-10, 10, events)
	if len(a.Conclusions) != len(b.Conclusions) {
		t.Fatalf("conclusion histories differ: %d vs %d", len(a.Conclusions), len(b.Conclusions))
	}
	for i := range a.Conclusions {
		if !a.Conclusions[i].Equal(b.Conclusions[i]) {
			t.Fatalf("conclusion %d differs: %+v vs %+v", i, a.Conclusions[i], b.Conclusions[i])
		}
	}
	la, lb := a.Ledger.Entries(), b.Ledger.Entries()
	if len(la) != len(lb) {
		t.Fatalf("ledgers differ: %d vs %d", len(la), len(lb))
	}
	for i := range la {
		if la[i] != lb[i] {
			t.Fatalf("ledger entry %d differs: %+v vs %+v", i, la[i], lb[i])
		}
	}
	// The replayed run shows both directions of stop movement and keeps
	// the first executed action intact.
	if len(la) == 0 || la[0].Kind != ActionStopUpper || la[0].Conclusion.Steps != 2 {
		t.Fatalf("first executed action missing or wrong: %+v", la)
	}
	var compensations int
	for _, e := range la {
		if e.Kind == ActionCompensate {
			compensations++
		}
	}
	if compensations == 0 {
		t.Fatal("expected compensation entries after revisions")
	}
}

func TestRecoveryViaEventReplayMatchesCheckpointResume(t *testing.T) {
	var obs []Observation
	seed := 77
	for i := 0; i < 60; i++ {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		obs = append(obs, ob(i, 1, seed%13-6))
	}
	events := make([]Event, 0, len(obs))
	for _, o := range obs {
		events = append(events, Event{Kind: EventObserve, Key: o.Key, Value: o.Value})
	}
	// Crash halfway, then recover by replaying the event log.
	crashed := Replay(-12, 12, events[:30])
	cp := crashed.Live.Save()
	resumed := Restore(-12, 12, cp)
	for _, o := range obs[30:] {
		resumed.Ingest(o)
	}
	full := Replay(-12, 12, events)
	if !resumed.Conclusion().Equal(full.Live.Conclusion()) {
		t.Fatalf("checkpoint resume %+v != replay %+v", resumed.Conclusion(), full.Live.Conclusion())
	}
}
