package expsim

import "sort"

// Checkpoint is a restorable snapshot of the engine. It carries the
// stop evidence so a recovered engine keeps the frozen conclusion
// without re-processing history.
type Checkpoint struct {
	Processed int
	Sum       int64
	Stopped   bool
	Decision  Decision
	Evidence  *StopEvidence
	Dups      int
	Ignored   int
	Seen      []Observation // sorted by ID, for idempotent dedupe after restore
}

// SaveCheckpoint snapshots the current engine state, including the
// stop evidence when the experiment has stopped.
func (e *Engine) SaveCheckpoint() Checkpoint {
	seen := make([]Observation, 0, len(e.seen))
	for _, o := range e.seen {
		seen = append(seen, o)
	}
	sort.Slice(seen, func(i, j int) bool { return seen[i].ID < seen[j].ID })
	cp := Checkpoint{
		Processed: e.n,
		Sum:       e.sum,
		Stopped:   e.stopped,
		Decision:  e.decision,
		Dups:      e.dups,
		Ignored:   e.ignored,
		Seen:      seen,
	}
	if e.evidence != nil {
		ev := *e.evidence
		cp.Evidence = &ev
	}
	return cp
}

// Restore rebuilds an engine from a checkpoint. The restored engine
// keeps the frozen conclusion and the dedupe set, so replayed or
// repeated events cannot alter it.
func Restore(b Boundaries, cp Checkpoint) *Engine {
	e := NewEngine(b)
	e.n = cp.Processed
	e.sum = cp.Sum
	e.stopped = cp.Stopped
	e.decision = cp.Decision
	e.dups = cp.Dups
	e.ignored = cp.Ignored
	if cp.Evidence != nil {
		ev := *cp.Evidence
		e.evidence = &ev
	}
	for _, o := range cp.Seen {
		e.seen[o.ID] = o
	}
	return e
}
