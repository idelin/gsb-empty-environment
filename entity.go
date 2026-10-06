package sim

// Entity is a simulation object with integer attributes and its own
// deterministic random stream.
type Entity struct {
	ID    string
	Attrs map[string]int64
	rng   *RNG
}

func newEntity(worldSeed uint64, id string) *Entity {
	return &Entity{
		ID:    id,
		Attrs: make(map[string]int64),
		rng:   NewRNG(deriveSeed(worldSeed, id)),
	}
}

// entitySnapshot is the serializable form of an Entity, including the exact
// RNG state and draw count so a restore resumes the identical stream.
type entitySnapshot struct {
	ID       string
	Attrs    map[string]int64
	RNGState uint64
	RNGCalls uint64
}

func (e *Entity) snapshot() entitySnapshot {
	attrs := make(map[string]int64, len(e.Attrs))
	for k, v := range e.Attrs {
		attrs[k] = v
	}
	return entitySnapshot{
		ID:       e.ID,
		Attrs:    attrs,
		RNGState: e.rng.state,
		RNGCalls: e.rng.calls,
	}
}

func entityFromSnapshot(s entitySnapshot) *Entity {
	attrs := make(map[string]int64, len(s.Attrs))
	for k, v := range s.Attrs {
		attrs[k] = v
	}
	return &Entity{
		ID:    s.ID,
		Attrs: attrs,
		rng:   &RNG{state: s.RNGState, calls: s.RNGCalls},
	}
}
