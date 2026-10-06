package expsim

import "sort"

// Engine accumulates observations step by step and stops at the
// first boundary touch. Once stopped the conclusion is frozen:
// later observations are logged for audit but never change the
// cumulative statistic or the decision.
type Engine struct {
	bounds Boundaries

	n        int   // number of observations counted into the statistic
	sum      int64 // cumulative statistic
	seen     map[string]Observation
	log      []Observation // every accepted observation, in processing order
	dups     int           // repeated events dropped by ID
	ignored  int           // observations received after the stop
	stopped  bool
	decision Decision
	evidence *StopEvidence
}

// NewEngine creates an engine with the given pre-given boundaries.
func NewEngine(b Boundaries) *Engine {
	return &Engine{bounds: b, seen: make(map[string]Observation)}
}

// Process ingests a batch of observations. The batch is ordered by
// (Time, Seq, ID) so that observations sharing a time are applied in
// sequence order. Repeated events (same ID) are dropped; a repeated
// ID carrying different content is a conflict and is reported.
func (e *Engine) Process(obs ...Observation) (conflicts []Observation) {
	sort.SliceStable(obs, func(i, j int) bool {
		if obs[i].Time != obs[j].Time {
			return obs[i].Time < obs[j].Time
		}
		if obs[i].Seq != obs[j].Seq {
			return obs[i].Seq < obs[j].Seq
		}
		return obs[i].ID < obs[j].ID
	})
	for _, o := range obs {
		if prev, ok := e.seen[o.ID]; ok {
			if prev != o {
				conflicts = append(conflicts, o)
			} else {
				e.dups++
			}
			continue
		}
		e.seen[o.ID] = o
		e.log = append(e.log, o)
		if e.stopped {
			e.ignored++
			continue
		}
		e.n++
		e.sum += o.Value
		if dec, hit := e.bounds.touches(e.n, e.sum); hit {
			e.stopped = true
			e.decision = dec
			e.evidence = &StopEvidence{
				Step:     e.n,
				Sum:      e.sum,
				Upper:    e.bounds.Upper[e.n-1],
				Lower:    e.bounds.Lower[e.n-1],
				Decision: dec,
				ObsID:    o.ID,
				ObsTime:  o.Time,
				ObsSeq:   o.Seq,
				ObsValue: o.Value,
			}
		}
	}
	return conflicts
}

// Stopped reports whether the experiment has stopped.
func (e *Engine) Stopped() bool { return e.stopped }

// Decision returns the frozen conclusion (DecisionNone while running).
func (e *Engine) Decision() Decision { return e.decision }

// Evidence returns the stop proof, or nil while running.
func (e *Engine) Evidence() *StopEvidence { return e.evidence }

// Counts returns processed, duplicate-dropped and post-stop-ignored
// observation counts.
func (e *Engine) Counts() (processed, dups, ignored int) {
	return e.n, e.dups, e.ignored
}

// Sum returns the cumulative statistic (frozen once stopped).
func (e *Engine) Sum() int64 { return e.sum }

// Log returns accepted observations in processing order, including
// any received after the stop.
func (e *Engine) Log() []Observation {
	out := make([]Observation, len(e.log))
	copy(out, e.log)
	return out
}
