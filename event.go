package replay

// Event is an immutable fact appended to the journal.
type Event struct {
	ID     string
	Kind   string
	Amount int64
}
