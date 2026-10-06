package sim

// Kind identifies the operation an Event performs.
type Kind int

const (
	// KindAddEntity creates EntityID; Delta becomes its Attrs[Key] value.
	KindAddEntity Kind = iota
	// KindMutate draws from the entity's own RNG and applies
	// Attrs[Key] += Delta + jitter, where jitter is deterministic per entity.
	KindMutate
)

// Event is one deterministic step. Seq is the explicit position in the event
// order: the engine only accepts the event whose Seq equals its next expected
// sequence number, so replay order is fully specified by the event log.
type Event struct {
	Seq      uint64
	Kind     Kind
	EntityID string
	Key      string
	Delta    int64
}
