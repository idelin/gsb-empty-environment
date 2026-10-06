package sim

import "fmt"

// Replay applies events in arrival order and returns the resulting
// simulator. Replaying the same log always reproduces the same fact
// versions, recomputed conclusions and action versions, which is what
// makes event-sourced recovery possible.
func Replay(lower, upper int, events []Event) *Simulator {
	s := NewSimulator(lower, upper)
	for _, ev := range events {
		s.Apply(ev)
	}
	return s
}

// VerifyPrefixExhaustive checks, for every prefix of the ordered
// observations, that the incremental engine agrees with a from-scratch
// recompute, that a boundary is touched at most once, and that
// re-ingesting a duplicate of any already seen observation is a no-op.
func VerifyPrefixExhaustive(lower, upper int, obs []Observation) error {
	SortObservations(obs)
	live := NewEngine(lower, upper)
	stops := 0
	for i, ob := range obs {
		if live.Ingest(ob) {
			stops++
		}
		want := Recompute(lower, upper, obs[:i+1])
		if got := live.Conclusion(); !got.Equal(want) {
			return fmt.Errorf("prefix %d: incremental %+v != recompute %+v", i+1, got, want)
		}
		if stops > 1 {
			return fmt.Errorf("prefix %d: boundary touched %d times", i+1, stops)
		}
		sum, steps := live.Sum(), live.Steps()
		if live.Ingest(ob) {
			return fmt.Errorf("prefix %d: duplicate %+v triggered a stop", i+1, ob.Key)
		}
		if live.Sum() != sum || live.Steps() != steps || !live.Conclusion().Equal(want) {
			return fmt.Errorf("prefix %d: duplicate %+v mutated engine state", i+1, ob.Key)
		}
	}
	return nil
}

// VerifyCheckpointRecovery checks that saving a checkpoint at every prefix
// and resuming from it reproduces the uninterrupted run exactly, including
// the frozen stop evidence.
func VerifyCheckpointRecovery(lower, upper int, obs []Observation) error {
	SortObservations(obs)
	full := NewEngine(lower, upper)
	for _, ob := range obs {
		full.Ingest(ob)
	}
	want := full.Conclusion()

	live := NewEngine(lower, upper)
	for i, ob := range obs {
		resumed := Restore(lower, upper, live.Save())
		for _, rest := range obs[i:] {
			resumed.Ingest(rest)
		}
		if got := resumed.Conclusion(); !got.Equal(want) {
			return fmt.Errorf("resume at prefix %d: got %+v, want %+v", i, got, want)
		}
		live.Ingest(ob)
	}
	resumed := Restore(lower, upper, live.Save())
	if got := resumed.Conclusion(); !got.Equal(want) {
		return fmt.Errorf("resume at full prefix: got %+v, want %+v", got, want)
	}
	return nil
}
