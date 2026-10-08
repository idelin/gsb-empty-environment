package material

// EventType enumerates the material-transforming events.
type EventType int

const (
	EvReceive EventType = iota // bring qty of a pure source into a new lot
	EvSplit                    // split a lot into integer-quantity parts
	EvMerge                    // merge lots, quantity-weighted provenance
	EvLoss                     // explicit loss, proportional rational deduction
)

func (t EventType) String() string {
	switch t {
	case EvReceive:
		return "receive"
	case EvSplit:
		return "split"
	case EvMerge:
		return "merge"
	case EvLoss:
		return "loss"
	}
	return "unknown"
}

// Part is one integer-quantity output of a split.
type Part struct {
	LotID string
	Qty   int64
}

// Event is a single state transition. Every event carries a unique ID;
// re-applying an event with an already-seen ID is a no-op (idempotent).
//
// Conservation law: for every event,
//   sum(input quantities) == sum(output quantities) + loss.
// Any shortfall in a split is recorded as explicit loss in the Receipt.
type Event struct {
	ID   string
	Type EventType

	// EvReceive: new lot LotID with Qty units of pure Source.
	LotID  string
	Qty    int64
	Source string

	// EvSplit: lot From is consumed into Parts (integer quantities).
	From  string
	Parts []Part

	// EvMerge: lots Ins are consumed into new lot Out.
	Ins []string
	Out string

	// EvLoss: LossQty units are deducted from lot LotID, proportionally
	// across its provenance vector (rational deduction).
	LossQty int64
}

// Receipt is the audit record of an applied event. It makes every loss
// explicit and allows idempotent re-application and replay verification.
type Receipt struct {
	EventID string
	Type    EventType

	// LossQty is the quantity lost by this event (0 = fully conserved).
	LossQty int64
	// LossVec is the provenance composition of the lost quantity.
	LossVec Vector

	// Consumed snapshots the input lots before the event.
	Consumed []*Lot
	// Produced snapshots the output lots after the event.
	Produced []*Lot
}
