package paxos

// NoOpValue is proposed by a leader to fill log holes discovered during
// Phase 1, so the committed prefix never contains gaps.
const NoOpValue = "\x00noop"

// Entry is a single replicated log slot.
type Entry struct {
	Slot      int
	Value     string
	Ballot    Ballot // ballot under which the entry was accepted
	Committed bool
}
