package ormap

// Replica is a local copy of the map with its own logical clock. Each Set
// mints a fresh dot (replica ID, next counter); each Remove observes the
// dots currently live for the key and carries exactly those dots.
type Replica struct {
	id    string
	clock uint64
	state *State
}

func NewReplica(id string) *Replica {
	return &Replica{id: id, state: NewState()}
}

func (r *Replica) ID() string { return r.id }

// Set assigns value to key, applies the delta locally and returns it for
// dissemination to other replicas.
func (r *Replica) Set(key, value string) *Op {
	r.clock++
	op := NewSetOp(r.id, r.clock, key, value)
	// Local apply cannot fail: the dot is fresh.
	_ = r.state.Merge(op)
	return op
}

// Remove deletes exactly the assignments for key that this replica has
// observed so far. Concurrent assignments not yet seen survive. Returns
// the delta for dissemination.
func (r *Replica) Remove(key string) *Op {
	var observed []Dot
	for dot := range r.state.Store[key] {
		observed = append(observed, dot)
	}
	op := NewRemoveOp(key, observed...)
	_ = r.state.Merge(op)
	return op
}

// Apply merges a received operation (delta-state) into the local state.
// Duplicate delivery is safe. Returns DotConflictError if the op assigns
// different content to an already known dot.
func (r *Replica) Apply(op *Op) error {
	return r.state.Merge(op)
}

// MergeState merges a full state snapshot from another replica.
func (r *Replica) MergeState(o *State) error {
	return r.state.Merge(o)
}

// State returns a deep copy of the local state (safe to ship as a
// state-based snapshot or to mutate by the caller).
func (r *Replica) State() *State {
	return r.state.Clone()
}

// Live returns all surviving assignments for key.
func (r *Replica) Live(key string) map[Dot]string {
	return r.state.Live(key)
}

// Display returns the presentation value for key (highest logical time,
// replica ID tiebreak) and whether concurrent assignments coexist.
func (r *Replica) Display(key string) (string, bool) {
	return r.state.Display(key)
}
