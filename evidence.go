package taskpkg

import (
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrInvalidIdentity is returned when an identity has no tenant.
	ErrInvalidIdentity = errors.New("evidence: invalid identity")
	// ErrInvalidInterval is returned for an interval whose end precedes its start.
	ErrInvalidInterval = errors.New("evidence: invalid half-open interval")
	// ErrOffsetOverflow is returned when a fragment's byte range wraps uint64.
	ErrOffsetOverflow = errors.New("evidence: fragment offset overflows")
	// ErrRevoked is returned once evidence for a tenant or connection is revoked.
	ErrRevoked = errors.New("evidence: tenant or connection revoked")
	// ErrEvidenceInvalid is returned when an evidence value is not structurally valid.
	ErrEvidenceInvalid = errors.New("evidence: invalid evidence")
	// ErrChainBroken is returned when a byte cannot be traced back to one identity.
	ErrChainBroken = errors.New("evidence: identity chain broken")
	// ErrGap is returned when a requested offset is an explicit gap.
	ErrGap = errors.New("evidence: offset is a gap")
	// ErrOutOfRange is returned when an offset is outside the alert interval.
	ErrOutOfRange = errors.New("evidence: offset outside alert interval")
)

// Identity is the immutable, trusted provenance of a stream fragment.
// Generation distinguishes successive connections for the same tenant; a
// fragment from one generation never joins evidence from another generation.
type Identity struct {
	Tenant     string `json:"tenant"`
	Generation uint64 `json:"generation"`
}

// Valid reports whether the identity has a tenant. Generation zero is allowed
// because some probes number connections from zero.
func (id Identity) Valid() bool {
	return id.Tenant != ""
}

// Equal reports exact identity equality. It is intentionally not tenant-only.
func (id Identity) Equal(other Identity) bool {
	return id == other
}

func (id Identity) String() string {
	return id.Tenant + "/" + strconv.FormatUint(id.Generation, 10)
}

// BlockID identifies one immutable block accepted by a Store.
type BlockID uint64

func (id BlockID) String() string {
	return strconv.FormatUint(uint64(id), 10)
}

// Fragment is a probe-supplied byte run. Data is copied by Add; the caller may
// immediately reuse or overwrite the backing storage after Add returns.
type Fragment struct {
	Identity Identity
	Offset   uint64
	Data     []byte
}

// Alert requests the half-open interval [Start, End) under one identity.
type Alert struct {
	Identity Identity
	Start    uint64
	End      uint64
}

// Valid reports whether the alert names a tenant and has a non-wrapping range.
func (a Alert) Valid() bool {
	return a.Identity.Valid() && a.Start <= a.End
}

// Len returns End-Start. It must only be called on a valid Alert.
func (a Alert) Len() uint64 {
	return a.End - a.Start
}

// SegmentKind describes what an assembler emitted for a stream range.
type SegmentKind uint8

const (
	// KindData contains exact bytes with a complete provenance chain.
	KindData SegmentKind = iota
	// KindGap is an explicit hole. It never contains filler or adjacent memory.
	KindGap
	// KindOverlapConflict contains disagreeing overlapping contributions. No
	// contributor is selected as the winner.
	KindOverlapConflict
)

// Compatibility aliases with more descriptive names.
const (
	SegmentData     = KindData
	SegmentGap      = KindGap
	SegmentConflict = KindOverlapConflict
)

func (k SegmentKind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindGap:
		return "gap"
	case KindOverlapConflict:
		return "overlap-conflict"
	default:
		return "unknown(" + strconv.Itoa(int(k)) + ")"
	}
}

// Source identifies the block and exact contribution behind a range of output.
// Offset and Length describe the whole immutable block; Start and End describe
// the clipped contribution in stream-offset space.
type Source struct {
	Block    BlockID  `json:"block"`
	Identity Identity `json:"identity"`
	Offset   uint64   `json:"offset"`
	Length   uint64   `json:"length"`
	Start    uint64   `json:"start"`
	End      uint64   `json:"end"`
	Data     []byte   `json:"-"`
}

// EndOffset returns the original block's exclusive end.
func (s Source) EndOffset() uint64 {
	return s.Offset + s.Length
}

// Variant is one distinct byte value found during an overlap conflict.
type Variant struct {
	Data    []byte   `json:"-"`
	Sources []Source `json:"sources"`
}

// Segment is one contiguous part of an evidence interval. Segments always
// partition the requested interval in offset order.
type Segment struct {
	Kind     SegmentKind `json:"kind"`
	Identity Identity    `json:"identity"`
	Start    uint64      `json:"start"`
	End      uint64      `json:"end"`
	Data     []byte      `json:"-"`
	Sources  []Source    `json:"sources,omitempty"`
	Variants []Variant   `json:"variants,omitempty"`
}

// Len returns the segment's byte length.
func (s Segment) Len() uint64 {
	return s.End - s.Start
}

// Conflict is a flattened report of one overlapping, disagreeing range.
type Conflict struct {
	Identity Identity  `json:"identity"`
	Start    uint64    `json:"start"`
	End      uint64    `json:"end"`
	Sources  []Source  `json:"sources"`
	Variants []Variant `json:"variants"`
}

// Trace answers which identity and blocks own one output byte.
type Trace struct {
	Offset   uint64
	Kind     SegmentKind
	Identity Identity
	Sources  []Source
	Variants []Variant
}

// Evidence is a pending, revocation-aware result for one Alert. It is safe to
// retain: Valid consults the issuing Store, so a later Revoke invalidates it.
type Evidence struct {
	Alert     Alert      `json:"alert"`
	Identity  Identity   `json:"identity"`
	Start     uint64     `json:"start"`
	End       uint64     `json:"end"`
	Segments  []Segment  `json:"segments"`
	Conflicts []Conflict `json:"conflicts,omitempty"`

	store     *Store
	snapshots map[BlockID]blockSnapshot
}

// Delivery is a deep-copied snapshot that was atomically checked against
// revocation at the delivery boundary. Evidence still pending after Revoke
// returns cannot produce a Delivery.
type Delivery struct {
	Alert     Alert
	Identity  Identity
	Start     uint64
	End       uint64
	Segments  []Segment
	Conflicts []Conflict
	// Epoch is the store's revocation epoch observed while the delivery lock
	// was held. A delivered snapshot is immutable after that point.
	Epoch uint64

	snapshots map[BlockID]blockSnapshot
}

// Valid reports whether this evidence may still be delivered. Revoking the
// tenant or exact connection after assembly makes it false.
func (e *Evidence) Valid() bool {
	if e == nil || e.store == nil || !e.Alert.Valid() {
		return false
	}
	if !e.Alert.Identity.Equal(e.Identity) || e.Start != e.Alert.Start || e.End != e.Alert.End {
		return false
	}
	return e.store.allows(e.Alert.Identity)
}

// Revoked reports whether the evidence has been invalidated.
func (e *Evidence) Revoked() bool {
	return e == nil || !e.Valid()
}

// Len returns the requested interval length.
func (e *Evidence) Len() uint64 {
	if e == nil || e.End < e.Start {
		return 0
	}
	return e.End - e.Start
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	// Always allocate exactly len(data). In particular, never expose spare
	// capacity that could contain bytes from another pooled stream.
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func (id Identity) validate() error {
	if !id.Valid() {
		return fmt.Errorf("%w: tenant is empty", ErrInvalidIdentity)
	}
	return nil
}
