package netsec

import "errors"

// Record is a single contribution that a collection host hands to the
// trimming package.
type Record struct {
	// EventID uniquely identifies an event within a deployment. Re-delivery of
	// the same EventID is treated idempotently as long as the record content is
	// identical.
	EventID string

	// UserID is the contributing user. Limits are scoped per user.
	UserID string

	// Window is the counting window the contribution belongs to. Limits are
	// scoped per (user, window) pair.
	Window int64

	// Group is the grouping key the contribution falls into. A local cap is
	// enforced per (user, window, group), and a separate global cap bounds the
	// total number of retained records across all groups of one (user, window).
	Group string

	// Payload is the opaque event body. It participates in the keyed digest and
	// in conflict detection but is otherwise not interpreted.
	Payload []byte
}

// Reason explains why a record was excluded from the retained set.
type Reason int

const (
	// ReasonUnknown is the zero value and must never be reported by Trim.
	ReasonUnknown Reason = iota

	// ReasonLocalCap means the record lost the per (user, window, group)
	// competition for one of the K smallest keyed digests.
	ReasonLocalCap

	// ReasonGlobalCap means the record survived its local group competition but
	// lost the cross-group competition for the total cap within its
	// (user, window) pair.
	ReasonGlobalCap

	// ReasonWithdrawn means the event was retracted via Selector.Withdraw and
	// its tombstone is still retained.
	ReasonWithdrawn
)

// String returns a stable, human-readable name for the reason.
func (r Reason) String() string {
	switch r {
	case ReasonLocalCap:
		return "local_cap"
	case ReasonGlobalCap:
		return "global_cap"
	case ReasonWithdrawn:
		return "withdrawn"
	default:
		return "unknown"
	}
}

// DroppedRecord pairs an excluded record with the reason for its exclusion.
type DroppedRecord struct {
	Record Record
	Reason Reason
}

// Result is the output of a trimming run: the records that survived and the
// records that were excluded, each annotated with the cause of exclusion.
type Result struct {
	// Kept holds the retained records ordered globally by (keyed digest,
	// EventID), which makes the output identical regardless of ingest order.
	Kept []Record

	// Dropped holds every excluded non-withdrawn record together with the
	// reason it lost its slot, ordered by EventID. Withdrawn events are not
	// listed here; they are reported in Withdrawn.
	Dropped []DroppedRecord

	// Withdrawn lists tombstones for retracted events, ordered by EventID.
	Withdrawn []Record
}

// ErrConflict is returned when an EventID is observed more than once with
// record contents that do not match the first accepted copy.
var ErrConflict = errors.New("netsec: conflicting records for the same event ID")

// ErrInvalidConfig is returned when a selector or pure trimming call is built
// with unusable parameters.
var ErrInvalidConfig = errors.New("netsec: invalid trimming configuration")
