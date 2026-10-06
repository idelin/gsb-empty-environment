package sim

// Entity is a simulation participant with its own independent random
// stream. State is a plain int64 attribute map so it can be snapshotted
// and compared exactly.
type Entity struct {
	ID    string
	Attrs map[string]int64
	rng   RNG
}

// Draw consumes one value from this entity's private random stream.
func (e *Entity) Draw() uint64 {
	return e.rng.Next()
}

// Drawn consumes one value mapped into [0, n).
func (e *Entity) Drawn(n int64) int64 {
	return e.rng.Intn(n)
}

// RandCalls reports how many random values this entity has consumed.
func (e *Entity) RandCalls() uint64 {
	return e.rng.Calls()
}

// Add adjusts one of the entity's numeric attributes.
func (e *Entity) Add(key string, delta int64) {
	e.Attrs[key] += delta
}
