package cacheaudit

import "slices"

// Minimize returns a 1-minimal subset of events for which violates still
// holds: removing any single remaining event makes the predicate false.
// The predicate is supplied by the caller (Audit passes one that requires
// the same contract violation with source-version evidence preserved), so
// the shrunk subset can never drop the publish a returned version came from.
//
// The search is a greedy delta-debugging pass: try chunks first for speed,
// then single events, to a fixpoint.
func Minimize(events []Event, violates func([]Event) bool) []Event {
	if !violates(events) {
		return nil
	}
	cur := slices.Clone(events)

	// Chunk passes with decreasing granularity.
	for chunk := 8; chunk >= 1; chunk /= 2 {
		for i := 0; i+chunk <= len(cur) && len(cur) > 1; {
			cand := slices.Clone(slices.Delete(slices.Clone(cur), i, i+chunk))
			if violates(cand) {
				cur = cand
			} else {
				i += chunk
			}
		}
	}
	// Single-event fixpoint (1-minimal).
	for changed := true; changed && len(cur) > 1; {
		changed = false
		for i := 0; i < len(cur); {
			cand := slices.Clone(slices.Delete(slices.Clone(cur), i, i+1))
			if violates(cand) {
				cur = cand
				changed = true
			} else {
				i++
			}
		}
	}
	return cur
}
