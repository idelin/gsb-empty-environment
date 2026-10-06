package eventlog

import "fmt"

// Verify replays the compressed segment from its initial summary and checks
// the replay against the preserved anchors: every public observation value
// and the final summary. A compression that drops, reorders, or count-merges
// non-commuting events fails verification because the replayed observations
// or final state diverge from the recorded anchors.
func (c CompressedLog) Verify() error {
	prevIndex := -1
	for _, obs := range c.Observations {
		if obs.Index <= prevIndex {
			return fmt.Errorf("eventlog: observation indices not strictly increasing: %d after %d", obs.Index, prevIndex)
		}
		prevIndex = obs.Index
	}
	state := c.Initial
	nextObs := 0
	index := 0
	for _, run := range c.Runs {
		if run.Count <= 0 {
			return fmt.Errorf("eventlog: run of %v has non-positive count %d", run.Event, run.Count)
		}
		for i := 0; i < run.Count; i++ {
			state = run.Event.Apply(state)
			if nextObs < len(c.Observations) && c.Observations[nextObs].Index == index {
				if got := state.Value; got != c.Observations[nextObs].Value {
					return fmt.Errorf("eventlog: observation %d (entry %d): replayed %d, recorded %d",
						nextObs, index, got, c.Observations[nextObs].Value)
				}
				nextObs++
			}
			index++
		}
	}
	if nextObs != len(c.Observations) {
		return fmt.Errorf("eventlog: observation %d at entry %d is beyond log length %d",
			nextObs, c.Observations[nextObs].Index, index)
	}
	if state != c.Final {
		return fmt.Errorf("eventlog: final summary mismatch: replayed %v, recorded %v", state, c.Final)
	}
	return nil
}
