package logtrunc

// Reference is a deliberately naive per-entry dependency-set model used
// to validate Planner. It tracks, for every log entry, the explicit set
// of entities (consumers and snapshots) that still depend on it. An
// entry is deletable exactly when its dependency set is empty; a segment
// is deletable exactly when every entry in it is deletable.
type Reference struct {
	maxSeq    uint64
	deps      map[uint64]map[string]bool // seq -> dependents ("c/<id>" or "s/<id>")
	horizons  map[string]uint64
	snapshots map[string][2]uint64
}

// NewReference creates an empty Reference.
func NewReference() *Reference {
	return &Reference{
		deps:      map[uint64]map[string]bool{},
		horizons:  map[string]uint64{},
		snapshots: map[string][2]uint64{},
	}
}

// SetLogEnd grows the log; every existing consumer gains a dependency
// on each new entry.
func (r *Reference) SetLogEnd(n uint64) {
	for seq := r.maxSeq + 1; seq <= n; seq++ {
		for id := range r.horizons {
			r.addDep(seq, "c/"+id)
		}
	}
	r.maxSeq = n
}

// RegisterConsumer adds a consumer that depends on every existing entry.
func (r *Reference) RegisterConsumer(id string) {
	r.horizons[id] = 0
	for seq := uint64(1); seq <= r.maxSeq; seq++ {
		r.addDep(seq, "c/"+id)
	}
}

// ConsumerExit removes every dependency held by the consumer.
func (r *Reference) ConsumerExit(id string) {
	delete(r.horizons, id)
	for seq := range r.deps {
		delete(r.deps[seq], "c/"+id)
	}
}

// AdvanceHorizon moves a consumer's continuous prefix to h, dropping
// its dependencies on (old, h].
func (r *Reference) AdvanceHorizon(id string, h uint64) {
	old := r.horizons[id]
	if h < old {
		panic("reference: horizon moved backwards")
	}
	for seq := old + 1; seq <= h; seq++ {
		if deps, ok := r.deps[seq]; ok {
			delete(deps, "c/"+id)
		}
	}
	r.horizons[id] = h
}

// PinSnapshot adds a snapshot dependency on every entry in [from, to].
func (r *Reference) PinSnapshot(id string, from, to uint64) {
	r.snapshots[id] = [2]uint64{from, to}
	for seq := from; seq <= to && seq <= r.maxSeq; seq++ {
		r.addDep(seq, "s/"+id)
	}
}

// ReleaseSnapshot removes every dependency held by the snapshot.
func (r *Reference) ReleaseSnapshot(id string) {
	delete(r.snapshots, id)
	for seq := range r.deps {
		delete(r.deps[seq], "s/"+id)
	}
}

// Deletable reports whether every entry of seg has an empty dependency
// set.
func (r *Reference) Deletable(seg Segment) bool {
	for seq := seg.Start; seq <= seg.End; seq++ {
		if len(r.deps[seq]) > 0 {
			return false
		}
	}
	return true
}

// Dependents returns the dependency set of one entry (for assertions).
func (r *Reference) Dependents(seq uint64) map[string]bool {
	out := map[string]bool{}
	for dep := range r.deps[seq] {
		out[dep] = true
	}
	return out
}

func (r *Reference) addDep(seq uint64, dep string) {
	if r.deps[seq] == nil {
		r.deps[seq] = map[string]bool{}
	}
	r.deps[seq][dep] = true
}
