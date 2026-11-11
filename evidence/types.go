// Package evidence implements a bounded, stream-oriented evidence buffer for
// network detection hosts.
//
// A Buffer accepts byte writes addressed by absolute stream offsets. Detection
// probes register Candidate notifications describing the matched range and the
// amount of context to retain on either side. The buffer stores only the bytes
// covered by active candidate windows: overlapping windows share the same
// backing blocks through reference counts, and unreferenced bytes are reclaimed
// under global budget pressure.
//
// Stream holes (offsets that were never written) are never synthesized. When
// evidence is collected they are emitted as explicit Missing segments, so callers
// can distinguish a real gap from context that has not arrived yet or context
// lost to budget-driven eviction.
package evidence

import "errors"

// Reason describes why a window range cannot be served from retained bytes.
type Reason int

const (
	// ReasonGap marks offsets that were never written to the stream.
	ReasonGap Reason = iota
	// ReasonPending marks offsets beyond the data received so far; they may
	// still arrive on a later Write.
	ReasonPending
	// ReasonEvicted marks offsets dropped because the global storage budget
	// was exhausted and the owning candidate was evicted.
	ReasonEvicted
)

func (r Reason) String() string {
	switch r {
	case ReasonGap:
		return "gap"
	case ReasonPending:
		return "pending"
	case ReasonEvicted:
		return "evicted"
	default:
		return "unknown"
	}
}

// Range is a half-open byte interval [Start, End) in absolute stream offsets.
type Range struct {
	Start int64
	End   int64
}

// Len returns the length of the range in bytes.
func (r Range) Len() int64 { return r.End - r.Start }

// Segment is one contiguous piece of a materialized evidence window. Exactly
// one of Data or Reason is meaningful: when Data is non-nil the segment carries
// retained bytes, otherwise Reason explains the missing range.
type Segment struct {
	Range  Range
	Data   []byte
	Reason Reason
}

// Candidate is a probe notification. EventID is the unique identifier of the
// detection event (tie breaker for eviction). Smaller Priority values are more
// important and are evicted last; equal priorities are ordered by EventID.
type Candidate struct {
	EventID       uint64
	Priority      int
	Match         Range
	BeforeContext int64
	AfterContext  int64
}

// Window returns the absolute range the candidate commits to retain, clamped at
// stream offset zero.
func (c Candidate) Window() Range {
	start := c.Match.Start - c.BeforeContext
	if start < 0 {
		start = 0
	}
	return Range{Start: start, End: c.Match.End + c.AfterContext}
}

// Evidence is a materialized candidate window. Segments are ordered, disjoint,
// and cover the whole window with no overlaps or holes between them.
type Evidence struct {
	EventID  uint64
	Priority int
	Match    Range
	Window   Range
	Segments []Segment
	// Evicted is true when the candidate was dropped by the budget policy.
	Evicted bool
}

// Eviction records a candidate removed by the budget policy together with the
// retained ranges that were lost at eviction time. Ranges overlapping a real
// stream gap only describe the retained portion.
type Eviction struct {
	EventID  uint64
	Priority int
	Window   Range
	// Missing holds ranges no longer retained, with ReasonEvicted.
	Missing []Segment
}

// Stats summarizes buffer occupancy.
type Stats struct {
	// RetainedBytes counts bytes currently held in backing blocks, including
	// blocks with no candidate references kept opportunistically as cache.
	RetainedBytes int64
	// ReferencedBytes counts bytes held on behalf of at least one candidate.
	ReferencedBytes int64
	// CachedBytes counts unreferenced blocks awaiting reclaim.
	CachedBytes int64
	// OverflowBytes is a defensive signal for the amount above BudgetBytes
	// that cannot be relieved because every retained byte is pinned and no
	// candidate remains to evict. Normal policy pressure is resolved through
	// eviction plus cache reclaim, so this stays zero in practice.
	OverflowBytes     int64
	BudgetBytes       int64
	ActiveCandidates  int
	EvictedCandidates int
	Sealed            bool
}

// Sentinel errors returned by Buffer operations.
var (
	// ErrInvalidRange is returned for zero/negative-length ranges or negative
	// context lengths.
	ErrInvalidRange = errors.New("evidence: invalid range")
	// ErrDuplicateEvent is returned when a candidate EventID is already known.
	ErrDuplicateEvent = errors.New("evidence: duplicate candidate event id")
	// ErrCandidateNotFound is returned when the event id is unknown.
	ErrCandidateNotFound = errors.New("evidence: candidate not found")
	// ErrCandidateFinalized is returned when the candidate was evicted or
	// canceled and can no longer be mutated.
	ErrCandidateFinalized = errors.New("evidence: candidate already finalized")
	// ErrSealed is returned when Write is called after Close.
	ErrSealed = errors.New("evidence: buffer is sealed")
)
