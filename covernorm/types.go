package covernorm

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
)

// Digest is the content digest of a module image. A reload at a different
// base address with the same digest is the same module content; identical raw
// addresses from images with different digests are unrelated.
type Digest [32]byte

// ParseDigest decodes a 64 character lowercase or uppercase hex SHA-256
// string into a Digest.
func ParseDigest(s string) (Digest, error) {
	var digest Digest
	raw, err := hex.DecodeString(s)
	if err != nil {
		return digest, fmt.Errorf("parse module digest %q: %w", s, err)
	}
	if len(raw) != len(digest) {
		return digest, fmt.Errorf("parse module digest %q: want %d bytes, got %d", s, len(digest), len(raw))
	}
	copy(digest[:], raw)
	return digest, nil
}

// MustParseDigest parses s and panics on error. It is intended for tests and
// fixed configuration literals.
func MustParseDigest(s string) Digest {
	digest, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return digest
}

// Hex returns the lowercase hexadecimal representation of the digest.
func (d Digest) Hex() string { return hex.EncodeToString(d[:]) }

// Short returns the leading eight hex digits, suitable for log lines.
func (d Digest) Short() string { return d.Hex()[:8] }

func (d Digest) String() string { return d.Hex() }

// Sentinal errors reported by Ingest and Flush.
var (
	// ErrDuplicateSeq is returned when an event reuses an event sequence
	// number already accepted from another event.
	ErrDuplicateSeq = errors.New("covernorm: duplicate event sequence number")
	// ErrLateEvent is returned when an event sequence number is not greater
	// than the highest sequence already flushed.
	ErrLateEvent = errors.New("covernorm: event sequence precedes already flushed events")
	// ErrNoSuchLoad is returned when an unload does not match any currently
	// active module load.
	ErrNoSuchLoad = errors.New("covernorm: unload does not match an active load")
	// ErrEmptyDigest is returned when a load declares a zero content digest.
	ErrEmptyDigest = errors.New("covernorm: load event has empty content digest")
	// ErrBadSegment is returned when a segment has zero size, an overflowing
	// end address, or overlaps another segment of the same load.
	ErrBadSegment = errors.New("covernorm: invalid or overlapping segment")
	// ErrBadEvent is returned when Ingest receives zero or more than one
	// non-nil event argument.
	ErrBadEvent = errors.New("covernorm: exactly one event must be ingested at a time")
)

// EventSeq orders events and samples on a single host timeline. Values must be
// unique and monotonically increasing once flushed.
type EventSeq uint64

// Segment describes one mapped half-open address range [Base, Base+Size) of a
// module load, relative to module offset Offset. Raw addresses inside the
// segment normalize to Offset+(raw-Base).
type Segment struct {
	Base   uint64
	Size   uint64
	Offset uint64
}

// End returns the exclusive end address, reporting ok=false when Base+Size
// overflows the address space.
func (s Segment) End() (end uint64, ok bool) {
	if math.MaxUint64-s.Base < s.Size {
		return 0, false
	}
	return s.Base + s.Size, true
}

// Contains reports whether raw falls inside [Base, Base+Size).
func (s Segment) Contains(raw uint64) bool {
	end, ok := s.End()
	if !ok {
		return false
	}
	return raw >= s.Base && raw < end
}

// Relative maps a raw address known to belong to the segment to its
// module-relative offset, reporting ok=false on offset overflow.
func (s Segment) Relative(raw uint64) (offset uint64, ok bool) {
	delta := raw - s.Base
	if math.MaxUint64-s.Offset < delta {
		return 0, false
	}
	return s.Offset + delta, true
}

func (s Segment) String() string {
	return fmt.Sprintf("%#x+%#x->%#x", s.Base, s.Size, s.Offset)
}

// LoadEvent declares that a module image identified by its content Digest is
// mapped under Segments. Seq is the declaration order; ConfirmedAt is the
// sequence at which the mapping is actually usable. Samples whose sequence
// precedes ConfirmedAt cannot be attributed to this load even if their raw
// address falls inside a declared segment.
//
// Generation disambiguates repeated loads of the same module; the host assigns
// a monotonically increasing generation per Module.
type LoadEvent struct {
	Seq         EventSeq
	ConfirmedAt EventSeq
	Module      string
	Digest      Digest
	Generation  uint64
	Segments    []Segment
}

// UnloadEvent retires the earliest still-active load matching Module and
// Digest. It never removes or rewrites previously archived edges.
type UnloadEvent struct {
	Seq    EventSeq
	Module string
	Digest Digest
}

// SampleEvent is one observed edge: a control flow jump from raw address From
// to raw address To, attributed to Thread.
type SampleEvent struct {
	Seq    EventSeq
	Thread uint64
	From   uint64
	To     uint64
}

// Reason explains why a sample endpoint could not be bound to a module
// endpoint.
type Reason uint8

const (
	// ReasonUnknown is the zero value and never appears in a finalized record.
	ReasonUnknown Reason = iota
	// ReasonBeforeLoad: a module segment covers the raw address, but every
	// covering load becomes confirmed only after this sample sequence.
	ReasonBeforeLoad
	// ReasonNoMapping: no confirmed active segment covers the raw address at
	// this point of the timeline, and no load ever covered it.
	ReasonNoMapping
	// ReasonAmbiguous: two or more confirmed active segments from different
	// load generations overlap at the raw address, so the owning module cannot
	// be chosen deterministically.
	ReasonAmbiguous
	// ReasonOffsetOverflow: the matching segment cannot represent the
	// module-relative offset without uint64 overflow.
	ReasonOffsetOverflow
)

func (r Reason) String() string {
	switch r {
	case ReasonBeforeLoad:
		return "before-load"
	case ReasonNoMapping:
		return "no-mapping"
	case ReasonAmbiguous:
		return "ambiguous-overlap"
	case ReasonOffsetOverflow:
		return "offset-overflow"
	default:
		return "unknown-reason"
	}
}

// EndpointRef binds one endpoint of an edge to a module image content digest
// and a module-relative offset. It deliberately carries no raw host address
// and no generation: identical code observed under two ASLR layouts or in two
// reload generations resolves to the same EndpointRef.
type EndpointRef struct {
	Digest Digest
	Offset uint64
}

// EdgeID is the content-addressed identity of an observed edge, binding both
// endpoints to their module digests and relative offsets. It carries no raw
// host addresses: a jump observed under two ASLR layouts or after a reload
// collapses to the same EdgeID when both endpoints resolve identically, while
// the same raw address backed by different digests yields different IDs.
type EdgeID struct {
	From EndpointRef
	To   EndpointRef
}

func (e EndpointRef) String() string {
	return fmt.Sprintf("%s+%#x", e.Digest.Short(), e.Offset)
}

func (id EdgeID) String() string {
	return id.From.String() + "->" + id.To.String()
}

// EdgeRecord is an archived, attributed edge. The record is append-only: once
// an edge is archived, later unloads never change it.
type EdgeRecord struct {
	Edge       EdgeID
	FirstSeq   EventSeq
	LastSeq    EventSeq
	HitCount   uint64
	FromModule string
	ToModule   string
	// Threads counts hits per thread identifier carried by the samples.
	Threads map[uint64]uint64
	// FromGenerations and ToGenerations record the load generations that
	// contributed each endpoint, preserving provenance even though generation
	// is not part of the content-addressed EdgeID.
	FromGenerations map[uint64]struct{}
	ToGenerations   map[uint64]struct{}
}

// UnknownRecord holds a sample that could not be normalized into an EdgeID,
// kept deliberately separate from the edge archive instead of being dropped or
// force-attributed.
type UnknownRecord struct {
	Seq        EventSeq
	Thread     uint64
	FromRaw    uint64
	ToRaw      uint64
	FromReason Reason
	ToReason   Reason
}

// EntryKind distinguishes the payload carried by a TimelineEntry.
type EntryKind uint8

const (
	EntryLoad EntryKind = iota + 1
	EntryUnload
	EntryEdge
	EntryUnknown
)

func (k EntryKind) String() string {
	switch k {
	case EntryLoad:
		return "load"
	case EntryUnload:
		return "unload"
	case EntryEdge:
		return "edge"
	case EntryUnknown:
		return "unknown"
	default:
		return "?"
	}
}

// TimelineEntry is one point of the ordered, merged host timeline and is
// suitable for explaining attribution decisions against the full event order.
type TimelineEntry struct {
	Kind EntryKind
	Seq  EventSeq

	// Load payload (Kind == EntryLoad).
	Load *LoadEvent

	// Unload payload (Kind == EntryUnload).
	Unload *UnloadEvent

	// Sample payload (Kind == EntryEdge or EntryUnknown).
	Thread  uint64
	FromRaw uint64
	ToRaw   uint64

	// Resolved edge payload (Kind == EntryEdge only).
	Edge *EdgeID

	// Failure payload (Kind == EntryUnknown only).
	Unknown *UnknownRecord
}

// Archive is the immutable snapshot returned by Flush. Maps must be treated as
// read-only by callers.
type Archive struct {
	Edges    map[EdgeID]*EdgeRecord
	Unknown  []*UnknownRecord
	Timeline []TimelineEntry
	// FlushedThrough is the highest event sequence incorporated so far.
	FlushedThrough EventSeq
}
