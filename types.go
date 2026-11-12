package netsec

import (
	"errors"
)

// Kind distinguishes a predeclared fixed query sequence from an adaptive
// plan whose next query is chosen based on earlier results.
type Kind int

const (
	// Adaptive plans branch off a published parent result and spend only the
	// budget reserved by their parent plan.
	Adaptive Kind = iota
	// Fixed plans are predeclared at creation with a known step count. A
	// fixed plan may never branch.
	Fixed
)

func (k Kind) String() string {
	if k == Fixed {
		return "fixed"
	}
	return "adaptive"
}

// State is the state of a plan in the state machine.
type State int

const (
	// Reserved: created with budget locked but has not published anything.
	Reserved State = iota
	// Active: has published at least one receipt and may publish more.
	Active
	// Canceled: published then canceled. The unspent reservation is
	// forfeited and is not refunded.
	Canceled
	// Completed: all declared work is done (fixed plan reached its step
	// count, or an adaptive plan was explicitly finished).
	Completed
	// Released: an unpublished branch gave its reservation back to the
	// parent. Terminal.
	Released
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Canceled:
		return "canceled"
	case Completed:
		return "completed"
	case Released:
		return "released"
	default:
		return "reserved"
	}
}

// Sentinel errors. Callers should compare with errors.Is.
var (
	ErrUnknownPlan      = errors.New("netsec: unknown plan")
	ErrUnknownReceipt   = errors.New("netsec: unknown receipt")
	ErrDuplicateReceipt = errors.New("netsec: duplicate receipt")
	ErrInvalidState     = errors.New("netsec: operation not allowed in plan state")
	ErrFixedBranch      = errors.New("netsec: fixed plans cannot branch")
	ErrNoParentResult   = errors.New("netsec: parent has no result to branch from")
	ErrBudgetExceeded   = errors.New("netsec: reservation exceeds available budget")
	ErrCostExceeded     = errors.New("netsec: cost exceeds plan available budget")
	ErrInvalidCost      = errors.New("netsec: cost must be positive")
	ErrInvalidSteps     = errors.New("netsec: fixed plan requires a positive step count")
	ErrStepCount        = errors.New("netsec: publish exceeds fixed plan step count")
)

// ZeroDigest is the parent digest used by a root plan's first receipt.
var ZeroDigest = [32]byte{}

// PathHop records one hop of the result lineage a receipt belongs to.
type PathHop struct {
	PlanID    string
	ReceiptID string
	Step      int
	Digest    [32]byte
}

// Receipt is the immutable record emitted when a query result is published.
// It binds the parent result digest, the charged cost and its full lineage.
type Receipt struct {
	ID           string
	PlanID       string
	Step         int
	Seq          int
	Cost         int64
	ResultDigest [32]byte
	// ParentDigest is the digest of the previous receipt in the same plan,
	// or for the first receipt of a branch, the digest of the parent result
	// it forked from. Root first receipts use ZeroDigest.
	ParentDigest [32]byte
	// ParentPlan and ParentReceipt name the result a branch forked from.
	// Both are empty for a root plan's first receipt.
	ParentPlan    string
	ParentReceipt string
	Path          []PathHop
}

// PublishInput carries the data for one publish call.
type PublishInput struct {
	PlanID       string
	ReceiptID    string
	Cost         int64
	ResultDigest [32]byte
}

// Plan is an immutable snapshot of a plan's state.
type Plan struct {
	ID         string
	Kind       Kind
	State      State
	Reserve    int64
	Spent      int64
	Steps      int // declared steps for fixed plans, 0 for adaptive
	Published  int
	ParentID   string
	ParentRecv string
	// Available is the amount still spendable by this plan. A parent's
	// available amount includes reservations held by its open branches.
	Available int64
}
