package indexvis

import "fmt"

// TxID identifies a database transaction.
//
// NoTransaction is a reserved value used as the "no deleter" marker in a
// version summary.
type TxID uint32

// NoTransaction denotes the absence of a deleting transaction.
const NoTransaction TxID = ^TxID(0)

// NoDeleter is an alias for NoTransaction, for use in version pairs.
const NoDeleter = NoTransaction

// Status is the final commit/abort state of a transaction. Unknown means that
// the commit table has not supplied the answer yet.
type Status uint8

const (
	Unknown Status = iota
	Committed
	Aborted
)

func (s Status) String() string {
	switch s {
	case Unknown:
		return "unknown"
	case Committed:
		return "committed"
	case Aborted:
		return "aborted"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

func (s Status) valid() bool {
	return s == Unknown || s == Committed || s == Aborted
}

// Snapshot describes what the index executor knows about transaction visibility
// at a snapshot.
//
// A transaction is eligible for snapshot visibility only when its ID is at most
// UpperBound and it is not listed as active. Transactions above UpperBound or
// in Active are "not yet effective": even if they eventually commit, their
// versions (and their deletes) do not affect this snapshot.
//
// Known fixes the final state of a transaction. A transaction absent from Known
// is unknown and may still be completed as either committed or aborted. Fixing
// an ineligible transaction is allowed; it only constrains dependency edges and
// never makes that transaction visible in this snapshot.
type Snapshot struct {
	UpperBound TxID
	Active     map[TxID]struct{}
	Known      map[TxID]Status
}

// NewSnapshot builds a snapshot from a bound, a slice of active transactions,
// and known final statuses. The Active slice may be nil.
func NewSnapshot(upperBound TxID, active []TxID, known map[TxID]Status) Snapshot {
	s := Snapshot{UpperBound: upperBound}
	if len(known) > 0 {
		s.Known = make(map[TxID]Status, len(known))
		for id, st := range known {
			s.Known[id] = st
		}
	}
	if len(active) > 0 {
		s.Active = make(map[TxID]struct{}, len(active))
		for _, id := range active {
			s.Active[id] = struct{}{}
		}
	}
	return s
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := Snapshot{UpperBound: s.UpperBound}
	if s.Active != nil {
		out.Active = make(map[TxID]struct{}, len(s.Active))
		for id := range s.Active {
			out.Active[id] = struct{}{}
		}
	}
	if s.Known != nil {
		out.Known = make(map[TxID]Status, len(s.Known))
		for id, st := range s.Known {
			out.Known[id] = st
		}
	}
	return out
}

// Validate checks the snapshot representation itself.
func (s Snapshot) Validate() error {
	for id, st := range s.Known {
		if id == NoTransaction {
			return fmt.Errorf("snapshot: NoTransaction cannot have a known status")
		}
		if !st.valid() {
			return fmt.Errorf("snapshot: transaction %d has invalid status %d", id, st)
		}
	}
	for id := range s.Active {
		if id == NoTransaction {
			return fmt.Errorf("snapshot: NoTransaction cannot be active")
		}
	}
	return nil
}

// Eligible reports whether a transaction can be effective at this snapshot.
func (s Snapshot) Eligible(id TxID) bool {
	if id == NoTransaction || id > s.UpperBound {
		return false
	}
	_, active := s.Active[id]
	return !active
}

// InitialStatus returns the known final status, or Unknown.
func (s Snapshot) InitialStatus(id TxID) Status {
	if st, ok := s.Known[id]; ok {
		return st
	}
	return Unknown
}
