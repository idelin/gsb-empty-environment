// Package volrecover implements recovery planning for an encrypted volume
// while two generations of metadata coexist on the same block image.
//
// The package works against an abstract read-only block image and a single
// selected trust root. It verifies signed headers, binds key slots to
// versioned write units, detects same-generation forks and mixed generation
// chains, and plans the minimum number of flush barriers needed so that every
// crash image retains at least one complete trusted authorization.
package volrecover

// Digest is a content digest (SHA-256).
type Digest [32]byte

// UnitID identifies a physical independently-written unit.
type UnitID uint64

// SlotID identifies a key slot.
type SlotID uint32

// SegmentID identifies an encrypted segment.
type SegmentID uint32

// Epoch is the header generation. Higher values are newer generations.
type Epoch uint64

// Interval is a half-open numeric range [Start, End).
type Interval struct {
	Start uint64
	End   uint64
}

// Valid reports whether the interval is well formed.
func (i Interval) Valid() bool { return i.End >= i.Start }

// Contains reports whether x falls inside the interval.
func (i Interval) Contains(x uint64) bool { return i.Start <= x && x < i.End }

// Overlaps reports whether two geometric ranges intersect. Intersecting slot
// ranges are permitted; content binding must still prevent cross-slot
// misinterpretation of a rewritten unit.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}

// Record is the current durable content of one physical write unit.
type Record struct {
	Unit    UnitID
	Version uint64
	Commit  Digest
}

// UnitRef is a header's versioned content commitment for one unit.
type UnitRef struct {
	Unit    UnitID
	Version uint64
	Commit  Digest
}

// SlotSpec describes one key slot: its declared range and the exact unit
// versions and commitments it is authorized to interpret.
type SlotSpec struct {
	ID    SlotID
	Range Interval
	Units []UnitRef
}

// Grant is a signed slot-to-segment authorization entry.
type Grant struct {
	Slot    SlotID
	Segment SegmentID
}

// ReasonCode enumerates verifiable failure reasons and budget conditions.
type ReasonCode int

const (
	ReasonOK ReasonCode = iota
	ReasonMalformedHeader
	ReasonBadSignature
	ReasonBrokenAncestry
	ReasonMixedGeneration
	ReasonFork
	ReasonDanglingUnit
	ReasonVersionRollback
	ReasonCommitMismatch
	ReasonRevokedSlot
	ReasonRangeConflict
	ReasonOverlapConfusion
	ReasonRepeatedUnitWrite
	ReasonRevokeBeforeHeader
	ReasonRootBeforeSpare
	ReasonNoTrustedHeader
	ReasonBudgetExceeded
	ReasonEnumerationLimit
)

// Reason is a structured, machine-inspectable explanation. A zero Reason is
// success. Codes other than the budget codes name a concrete violated
// invariant and are therefore verifiable; budget exhaustion is reported as
// Unknown instead.
type Reason struct {
	Code   ReasonCode
	Detail string
	// Witness identifies the offending artifact (header id, unit, slot, ...)
	// whenever one exists, so a host can re-check the conclusion.
	Witness string
}

// OK reports whether the reason is the zero success value.
func (r Reason) OK() bool { return r.Code == ReasonOK }

func (r Reason) Error() string {
	if r.Code == ReasonOK {
		return "volrecover: ok"
	}
	s := "volrecover: " + r.Code.String()
	if r.Detail != "" {
		s += ": " + r.Detail
	}
	if r.Witness != "" {
		s += " (witness: " + r.Witness + ")"
	}
	return s
}

func codeString(c ReasonCode) string {
	switch c {
	case ReasonOK:
		return "ok"
	case ReasonMalformedHeader:
		return "malformed header"
	case ReasonBadSignature:
		return "signature verification failed"
	case ReasonBrokenAncestry:
		return "header ancestry chain broken"
	case ReasonMixedGeneration:
		return "mixed generation headers"
	case ReasonFork:
		return "same-generation fork"
	case ReasonDanglingUnit:
		return "referenced unit absent from image"
	case ReasonVersionRollback:
		return "newer unit content interpreted through older slot"
	case ReasonCommitMismatch:
		return "unit content commitment mismatch"
	case ReasonRevokedSlot:
		return "header grants an irrevocably revoked slot"
	case ReasonRangeConflict:
		return "slot range or unit binding conflict"
	case ReasonOverlapConfusion:
		return "target rewrites a unit still bound to a live slot"
	case ReasonRepeatedUnitWrite:
		return "same unit written more than once before a barrier"
	case ReasonRevokeBeforeHeader:
		return "revocation registered before replacement header is durable"
	case ReasonRootBeforeSpare:
		return "root pointer updated before spare slot units are flushed"
	case ReasonNoTrustedHeader:
		return "no complete trusted authorization available"
	case ReasonBudgetExceeded:
		return "action budget exhausted"
	case ReasonEnumerationLimit:
		return "crash-state enumeration budget exhausted"
	default:
		return "unknown reason"
	}
}

// String returns a stable human-readable label for the code.
func (c ReasonCode) String() string { return codeString(c) }
