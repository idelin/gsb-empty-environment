package netsec

// CheckResult summarizes an incremental-vs-batch consistency check.
type CheckResult struct {
	// OK is true exactly when every invariant and the independent
	// recomputation agree with the incremental state.
	OK bool
	// Reason describes the first mismatch found.
	Reason string
	// Mismatch identifies the term involved, when applicable.
	Mismatch TermID
	// Batch is the snapshot used for the comparison.
	Batch BatchSnapshot
}

// Check runs the independent full recomputation and verifies that it
// reproduces the incremental closure, including shared derivations and the
// late key-leakage timing discipline.
func (e *Engine) Check() CheckResult {
	batch := e.Recompute()

	if len(batch.Earliest) != len(e.terms) {
		return CheckResult{Reason: "universe size mismatch", Batch: batch}
	}

	for id := range e.terms {
		if e.earliest[id] != batch.Earliest[id] {
			return CheckResult{
				Reason:   "earliest event differs from full recomputation",
				Mismatch: TermID(id),
				Batch:    batch,
			}
		}
	}

	for id := range e.terms {
		if e.earliest[id] == NoEvent {
			if e.bestEdge[id] != -1 {
				return CheckResult{Reason: "unknown term carries a derivation", Mismatch: TermID(id), Batch: batch}
			}
			continue
		}
		if reason, ok := e.checkSelectedEdge(TermID(id), batch); !ok {
			return CheckResult{Reason: reason, Mismatch: TermID(id), Batch: batch}
		}
	}

	if reason, id, ok := e.checkTargetDAGAcyclic(); !ok {
		return CheckResult{Reason: reason, Mismatch: id, Batch: batch}
	}

	return CheckResult{OK: true, Batch: batch}
}

func (e *Engine) checkSelectedEdge(id TermID, batch BatchSnapshot) (string, bool) {
	idx := e.bestEdge[id]
	if idx < 0 {
		return "knowable term lacks a derivation", false
	}
	edge := &e.edges[idx]
	if edge.head != id {
		return "derivation head mismatch", false
	}
	at, ok := e.edgeTime(edge)
	if !ok || at != e.earliest[id] {
		return "selected derivation does not fire at the earliest event", false
	}

	for _, otherIdx := range e.edgesOf(id) {
		other := &e.edges[otherIdx]
		otherAt, otherOK := e.edgeTime(other)
		if !otherOK || e.edgeWouldCycle(id, other) {
			continue
		}
		if otherAt < at || (otherAt == at && other.rank < edge.rank) {
			return "a better derivation exists than the selected edge", false
		}
	}

	if e.earliest[id] != batch.Earliest[id] {
		return "batch earliest event disagrees", false
	}
	return "", true
}

// checkTargetDAGAcyclic validates the selected derivations reachable from
// registered targets. The cycle rejection in evaluate already forbids
// self-referential edges; the remaining DAG invariant is monotone timing:
// every premise must be available no later than the conclusion it fires.
func (e *Engine) checkTargetDAGAcyclic() (string, TermID, bool) {
	visited := make(map[TermID]bool)
	var walk func(id TermID) (string, TermID, bool)
	walk = func(id TermID) (string, TermID, bool) {
		if visited[id] {
			return "", 0, true
		}
		visited[id] = true
		idx := e.bestEdge[id]
		if idx < 0 {
			return "", 0, true
		}
		edge := &e.edges[idx]
		for _, dep := range edge.deps {
			if e.earliest[dep] > e.earliest[id] {
				return "dependency becomes available later than its conclusion", id, false
			}
			if reason, mismatch, ok := walk(dep); !ok {
				return reason, mismatch, false
			}
		}
		return "", 0, true
	}

	for _, root := range e.targets {
		if reason, mismatch, ok := walk(root); !ok {
			return reason, mismatch, false
		}
	}
	return "", 0, true
}
