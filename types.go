package netsec

import (
	"errors"
	"fmt"
)

// ErrInvalidReceipt is returned when a receipt is malformed.
var ErrInvalidReceipt = errors.New("netsec: invalid receipt")

// CheckVerdict is the outcome reported by one detector for one chunk check.
type CheckVerdict int

const (
	// VerdictUnknown is the zero value and is never accepted on a receipt.
	VerdictUnknown CheckVerdict = iota
	// VerdictPass means this detector found nothing wrong.
	VerdictPass
	// VerdictPositive means this detector found prohibited content.
	VerdictPositive
	// VerdictFailed means the detector could not complete the check.
	VerdictFailed
)

func (v CheckVerdict) String() string {
	switch v {
	case VerdictPass:
		return "pass"
	case VerdictPositive:
		return "positive"
	case VerdictFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Receipt is one detector result for one check on one chunk.
//
// Generation binds the result to the manifest generation it was produced for.
// Check must belong to that manifest's complete check set; receipts for an
// unknown check, chunk or generation are ignored rather than applied.
type Receipt struct {
	Generation int
	ChunkIndex int
	Check      CheckID
	Verdict    CheckVerdict
}

// Validate reports whether the receipt is well formed.
func (r Receipt) Validate() error {
	if r.Generation < 0 {
		return fmt.Errorf("%w: negative generation", ErrInvalidReceipt)
	}
	if r.ChunkIndex < 0 {
		return fmt.Errorf("%w: negative chunk index", ErrInvalidReceipt)
	}
	if r.Check == "" {
		return fmt.Errorf("%w: empty check id", ErrInvalidReceipt)
	}
	switch r.Verdict {
	case VerdictPass, VerdictPositive, VerdictFailed:
		return nil
	default:
		return fmt.Errorf("%w: unknown verdict", ErrInvalidReceipt)
	}
}

// Status is the aggregate decision for an object.
type Status int

const (
	// StatusIncomplete means the object may not be published: receipts are
	// still outstanding, or processing ended for an unsatisfactory reason.
	StatusIncomplete Status = iota
	// StatusPass means every chunk completed every check without a positive.
	StatusPass
	// StatusBlocked means at least one check produced a positive verdict.
	StatusBlocked
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusBlocked:
		return "blocked"
	default:
		return "incomplete"
	}
}

// Reason explains a decision.
type Reason int

const (
	// ReasonAwaitingManifest means no manifest has been installed yet.
	ReasonAwaitingManifest Reason = iota
	// ReasonAwaitingReceipts means chunks/checks are still outstanding.
	ReasonAwaitingReceipts
	// ReasonTimeout means the manifest deadline elapsed first.
	ReasonTimeout
	// ReasonCheckFailed means a detector reported a failed check.
	ReasonCheckFailed
	// ReasonCancelled means processing was cancelled explicitly.
	ReasonCancelled
	// ReasonAllClear accompanies a passing decision.
	ReasonAllClear
	// ReasonPositive accompanies a blocking decision.
	ReasonPositive
)

func (r Reason) String() string {
	switch r {
	case ReasonAwaitingReceipts:
		return "awaiting_receipts"
	case ReasonTimeout:
		return "timeout"
	case ReasonCheckFailed:
		return "check_failed"
	case ReasonCancelled:
		return "cancelled"
	case ReasonAllClear:
		return "all_clear"
	case ReasonPositive:
		return "positive"
	default:
		return "awaiting_manifest"
	}
}

// SlotRef identifies one (chunk, check) slot.
type SlotRef struct {
	ChunkIndex int
	Check      CheckID
}

// Decision is a snapshot of the aggregate state.
type Decision struct {
	Status     Status
	Reason     Reason
	Generation int
	// Terminal is false while receipts are still expected. A terminal
	// decision never changes again within the same aggregator.
	Terminal bool
	// Missing lists slots that have no accepted receipt.
	Missing []SlotRef
	// Positives lists slots whose accepted verdict was VerdictPositive.
	Positives []SlotRef
	// Failures lists slots whose accepted verdict was VerdictFailed.
	Failures []SlotRef
}
