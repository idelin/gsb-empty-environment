package sim

// EventType enumerates the operations that can appear in the event log.
type EventType string

const (
	// EventReceive introduces Quantity units of virgin material from Source.
	EventReceive EventType = "receive"
	// EventSplit consumes Entity and produces len(Parts) entities whose
	// integer quantities are Parts. The parts must sum to the input
	// quantity (conservation); shares are allocated proportionally.
	EventSplit EventType = "split"
	// EventMerge consumes Entities and produces one entity whose source
	// vector is the element-wise sum of the inputs (conservation).
	EventMerge EventType = "merge"
	// EventLoss removes Quantity units from Entity, deducting every source
	// share proportionally with exact rational arithmetic. The loss is
	// recorded explicitly in the simulator's loss ledger.
	EventLoss EventType = "loss"
)

// Event is a single recorded operation. Output entity IDs are derived
// deterministically from the event ID ("<id>/0", "<id>/1", ...), which makes
// replay deterministic and duplicate application idempotent.
type Event struct {
	ID       string    `json:"id"`
	Type     EventType `json:"type"`
	Source   string    `json:"source,omitempty"`
	Quantity int64     `json:"quantity,omitempty"`
	Entity   string    `json:"entity,omitempty"`
	Entities []string  `json:"entities,omitempty"`
	Parts    []int64   `json:"parts,omitempty"`
}
