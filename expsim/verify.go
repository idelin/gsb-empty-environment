package expsim

import (
	"fmt"
	"sort"
)

// Dedupe collapses repeated events by ID. A repeated ID carrying
// different content is a conflict (such changes must go through
// revisions, not redelivery).
func Dedupe(log []Observation) (unique []Observation, dups int, conflicts []Observation) {
	seen := make(map[string]Observation)
	var order []string
	for _, o := range log {
		if prev, ok := seen[o.ID]; ok {
			if prev != o {
				conflicts = append(conflicts, o)
			} else {
				dups++
			}
			continue
		}
		seen[o.ID] = o
		order = append(order, o.ID)
	}
	for _, id := range order {
		unique = append(unique, seen[id])
	}
	return unique, dups, conflicts
}

// SortObs orders observations by (Time, Seq, ID): same-time
// observations are applied in sequence order.
func SortObs(obs []Observation) {
	sort.SliceStable(obs, func(i, j int) bool {
		if obs[i].Time != obs[j].Time {
			return obs[i].Time < obs[j].Time
		}
		if obs[i].Seq != obs[j].Seq {
			return obs[i].Seq < obs[j].Seq
		}
		return obs[i].ID < obs[j].ID
	})
}

// VerifyStop re-derives the stop by exhaustive prefix enumeration
// over the raw log (which may contain repeated events) and checks
// that the recorded evidence is exactly the first boundary touch:
// no shorter prefix may touch a boundary, and the touching prefix
// must match the evidence in sum, decision and causing observation.
func VerifyStop(rawLog []Observation, b Boundaries, ev StopEvidence) error {
	unique, _, conflicts := Dedupe(rawLog)
	if len(conflicts) > 0 {
		return fmt.Errorf("conflicting repeated event %q: use revisions for corrections", conflicts[0].ID)
	}
	SortObs(unique)
	if ev.Step < 1 || ev.Step > len(unique) {
		return fmt.Errorf("evidence step %d outside log of %d observations", ev.Step, len(unique))
	}
	var sum int64
	for i, o := range unique {
		sum += o.Value
		step := i + 1
		if step < ev.Step {
			if _, hit := b.touches(step, sum); hit {
				return fmt.Errorf("prefix of length %d already touches a boundary (sum=%d): stop at step %d is not the first touch", step, sum, ev.Step)
			}
			continue
		}
		if step == ev.Step {
			if sum != ev.Sum {
				return fmt.Errorf("sum mismatch at step %d: recomputed %d, evidence %d", step, sum, ev.Sum)
			}
			dec, hit := b.touches(step, sum)
			if !hit {
				return fmt.Errorf("no boundary touched at step %d (sum=%d)", step, sum)
			}
			if dec != ev.Decision {
				return fmt.Errorf("decision mismatch at step %d: recomputed %s, evidence %s", step, dec, ev.Decision)
			}
			if o.ID != ev.ObsID || o.Value != ev.ObsValue || o.Time != ev.ObsTime || o.Seq != ev.ObsSeq {
				return fmt.Errorf("causing observation mismatch at step %d: recomputed %+v, evidence obs %s", step, o, ev.ObsID)
			}
			if b.Upper[step-1] != ev.Upper || b.Lower[step-1] != ev.Lower {
				return fmt.Errorf("boundary snapshot mismatch at step %d", step)
			}
			return nil
		}
	}
	return fmt.Errorf("unreachable")
}

// VerifyCheckpoint checks a checkpoint against the raw log by
// exhaustive prefix enumeration: the processed count, cumulative sum
// and frozen conclusion must all follow from the unique events, and
// repeated events must not have been counted twice.
func VerifyCheckpoint(rawLog []Observation, b Boundaries, cp Checkpoint) error {
	unique, dups, conflicts := Dedupe(rawLog)
	if len(conflicts) > 0 {
		return fmt.Errorf("conflicting repeated event %q: use revisions for corrections", conflicts[0].ID)
	}
	if dups != cp.Dups {
		return fmt.Errorf("duplicate count mismatch: log has %d repeated events, checkpoint recorded %d", dups, cp.Dups)
	}
	SortObs(unique)
	var sum int64
	counted := 0
	stopped := false
	var dec Decision
	var ev *StopEvidence
	for i, o := range unique {
		if stopped {
			if i+1 <= cp.Processed {
				return fmt.Errorf("checkpoint counted %d observations but the stop froze the statistic at step %d", cp.Processed, counted)
			}
			continue
		}
		sum += o.Value
		counted++
		if d, hit := b.touches(counted, sum); hit {
			stopped = true
			dec = d
			ev = &StopEvidence{
				Step:     counted,
				Sum:      sum,
				Upper:    b.Upper[counted-1],
				Lower:    b.Lower[counted-1],
				Decision: d,
				ObsID:    o.ID,
				ObsTime:  o.Time,
				ObsSeq:   o.Seq,
				ObsValue: o.Value,
			}
		}
	}
	if counted != cp.Processed {
		return fmt.Errorf("processed count mismatch: recomputed %d, checkpoint %d", counted, cp.Processed)
	}
	if sum != cp.Sum {
		return fmt.Errorf("sum mismatch: recomputed %d, checkpoint %d", sum, cp.Sum)
	}
	if stopped != cp.Stopped || dec != cp.Decision {
		return fmt.Errorf("conclusion mismatch: recomputed stopped=%v decision=%s, checkpoint stopped=%v decision=%s",
			stopped, dec, cp.Stopped, cp.Decision)
	}
	if stopped {
		if cp.Evidence == nil {
			return fmt.Errorf("checkpoint stopped but carries no stop evidence")
		}
		if *cp.Evidence != *ev {
			return fmt.Errorf("evidence mismatch: recomputed %v, checkpoint %v", *ev, *cp.Evidence)
		}
		if err := VerifyStop(rawLog, b, *cp.Evidence); err != nil {
			return err
		}
	}
	return nil
}
