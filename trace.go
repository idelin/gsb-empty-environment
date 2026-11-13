package netsec

// Snapshot is a per-layer state capture. TransferIndex is the number of
// transfer records present when the snapshot was taken; rollback uses it to
// decide which records belong to a frame.
type Snapshot struct {
	Balance       int `json:"balance"`
	Cash          int `json:"cash"`
	Received      int `json:"received"`
	TransferIndex int `json:"transferIndex"`
}

// TransferStatus distinguishes a transfer that survived to the final
// committed transaction from one erased by a revert.
type TransferStatus string

const (
	TransferCommitted  TransferStatus = "committed"
	TransferRolledBack TransferStatus = "rolled_back"
)

// Transfer record is emitted by OpCashSend. Rolled-back records remain in the
// evidence log so an independent auditor can see that a transient transfer
// existed but was not committed.
type Transfer struct {
	Index         int            `json:"index"`
	FramePath     string         `json:"framePath"`
	Depth         int            `json:"depth"`
	PC            int            `json:"pc"`
	Amount        int            `json:"amount"`
	Status        TransferStatus `json:"status"`
	RollbackCause string         `json:"rollbackCause,omitempty"`
}

// Outcome is the final result of a frame or of the whole transaction.
type Outcome string

const (
	OutcomeReturned Outcome = "returned"
	OutcomeReverted Outcome = "reverted"
	OutcomeUnknown  Outcome = "unknown"
)

// Decision is the attacker's choice at one externalCallback instruction.
type Decision struct {
	Reenter bool `json:"reenter"`
	Amount  int  `json:"amount,omitempty"`
}

// Enter chooses to re-enter the entry program with amount.
func Enter(amount int) Decision { return Decision{Reenter: true, Amount: amount} }

// ReturnNow chooses the immediate return offered by externalCallback.
func ReturnNow() Decision { return Decision{Reenter: false} }

// Plan is a replayable attacker script. RootAmount selects the entry frame's
// amount. Decisions are consumed in execution order: a parent's callback
// decision precedes the decisions made inside the resulting child.
type Plan struct {
	RootAmount int        `json:"rootAmount"`
	Decisions  []Decision `json:"decisions"`
}

// CallbackTrace records one externalCallback instruction occurrence.
type CallbackTrace struct {
	PC       int         `json:"pc"`
	Decision Decision    `json:"decision"`
	Child    *FrameTrace `json:"child,omitempty"`
}

// RollbackEvidence is the proof that a frame restored a snapshot and which
// committed transfers were erased in the process.
type RollbackEvidence struct {
	FramePath      string     `json:"framePath"`
	Depth          int        `json:"depth"`
	Reason         string     `json:"reason"`
	Entry          Snapshot   `json:"entry"`
	BeforeRollback Snapshot   `json:"beforeRollback"`
	AfterRollback  Snapshot   `json:"afterRollback"`
	Transfers      []Transfer `json:"transfers,omitempty"`
}

// FrameTrace is one node of the call tree with its entry, exit, rollback
// snapshots and callback children.
type FrameTrace struct {
	Path           string            `json:"path"`
	Depth          int               `json:"depth"`
	Amount         int               `json:"amount"`
	Entry          Snapshot          `json:"entry"`
	Exit           *Snapshot         `json:"exit,omitempty"`
	BeforeRollback *Snapshot         `json:"beforeRollback,omitempty"`
	AfterRollback  *Snapshot         `json:"afterRollback,omitempty"`
	Outcome        Outcome           `json:"outcome"`
	Reason         string            `json:"reason,omitempty"`
	Instructions   int               `json:"instructions"`
	Callbacks      []CallbackTrace   `json:"callbacks,omitempty"`
	Rollback       *RollbackEvidence `json:"rollback,omitempty"`
}

// Execution is the independently replayed result of a Plan, including the
// complete call tree and transfer journal.
type Execution struct {
	Outcome        Outcome     `json:"outcome"`
	Steps          int         `json:"steps"`
	Calls          int         `json:"calls"`
	Depth          int         `json:"depth"`
	Root           *FrameTrace `json:"root"`
	Initial        Snapshot    `json:"initial"`
	Final          Snapshot    `json:"final"`
	Received       int         `json:"received"`
	Attack         bool        `json:"attack"`
	Plan           Plan        `json:"plan"`
	AmountSequence []int       `json:"amountSequence"`
	Transfers      []Transfer  `json:"transfers"`
}

// RollbackEvidence walks the call tree and returns every frame-level rollback
// proof in pre-order.
func (e *Execution) RollbackEvidence() []RollbackEvidence {
	if e == nil || e.Root == nil {
		return nil
	}
	var out []RollbackEvidence
	var walk func(*FrameTrace)
	walk = func(frame *FrameTrace) {
		if frame == nil {
			return
		}
		if frame.Rollback != nil {
			out = append(out, *frame.Rollback)
		}
		for i := range frame.Callbacks {
			walk(frame.Callbacks[i].Child)
		}
	}
	walk(e.Root)
	return out
}

// WalkFrames visits every frame in pre-order.
func (e *Execution) WalkFrames(fn func(*FrameTrace)) {
	if e == nil || e.Root == nil || fn == nil {
		return
	}
	var walk func(*FrameTrace)
	walk = func(frame *FrameTrace) {
		if frame == nil {
			return
		}
		fn(frame)
		for i := range frame.Callbacks {
			walk(frame.Callbacks[i].Child)
		}
	}
	walk(e.Root)
}
