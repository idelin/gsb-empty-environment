package sim

// Checkpoint captures everything needed to resume an engine, including the
// stop evidence, so a recovered run reaches the identical conclusion.
type Checkpoint struct {
	Sum        int
	Steps      int
	Conclusion Conclusion
	Seen       []ObsKey
}

// Save snapshots the engine state.
func (e *Engine) Save() Checkpoint {
	cp := Checkpoint{
		Sum:        e.sum,
		Steps:      e.steps,
		Conclusion: e.concl,
		Seen:       make([]ObsKey, 0, len(e.seen)),
	}
	for key := range e.seen {
		cp.Seen = append(cp.Seen, key)
	}
	return cp
}

// Restore rebuilds an engine from a checkpoint. The restored engine keeps
// the frozen conclusion and the duplicate-suppression set, so replaying
// already seen observations after recovery is still a no-op.
func Restore(lower, upper int, cp Checkpoint) *Engine {
	e := NewEngine(lower, upper)
	e.sum = cp.Sum
	e.steps = cp.Steps
	e.concl = cp.Conclusion
	for _, key := range cp.Seen {
		e.seen[key] = true
	}
	return e
}
