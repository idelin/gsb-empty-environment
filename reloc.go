package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// Permission bits describing how a segment may be accessed.
const (
	PermRead    = 1 << 0
	PermWrite   = 1 << 1
	PermExecute = 1 << 2
)

// Sentinel errors returned by the loader. Errors are wrapped with contextual
// detail, so callers should use errors.Is rather than comparing directly.
var (
	// ErrOverlappingSegments is reported when two non-empty segments occupy
	// overlapping address ranges.
	ErrOverlappingSegments = errors.New("netsec: overlapping segment ranges")

	// ErrInvalidTargetIndex is reported when a relocation targets a segment ID
	// that does not exist.
	ErrInvalidTargetIndex = errors.New("netsec: relocation target segment id out of range")

	// ErrInvalidSymbolIndex is reported when a relocation references a symbol
	// segment ID that does not exist.
	ErrInvalidSymbolIndex = errors.New("netsec: relocation symbol segment id out of range")

	// ErrInvalidWidth is reported when a relocation width is neither 4 nor 8.
	ErrInvalidWidth = errors.New("netsec: relocation width must be 4 or 8")

	// ErrTargetNotWritable is reported when the target segment is not writable.
	ErrTargetNotWritable = errors.New("netsec: relocation target segment is not writable")

	// ErrTargetExecutable is reported when the target segment is executable,
	// i.e. a forbidden writable-and-executable (W+X) relocation.
	ErrTargetExecutable = errors.New("netsec: relocation target segment is executable (W+X forbidden)")

	// ErrTargetOutOfRange is reported when the full-width write does not fit
	// inside the target segment's content (a truncated write).
	ErrTargetOutOfRange = errors.New("netsec: relocation target does not fit in segment (truncated write)")

	// ErrSymbolNotReadable is reported when the symbol segment is not readable.
	ErrSymbolNotReadable = errors.New("netsec: relocation symbol segment is not readable")

	// ErrSymbolOutOfRange is reported when the symbol offset falls outside its
	// segment's content.
	ErrSymbolOutOfRange = errors.New("netsec: relocation symbol offset out of range")

	// ErrAddressOverflow is reported when a symbol address or the addended
	// value wraps around the uint64 address space.
	ErrAddressOverflow = errors.New("netsec: relocation arithmetic overflows uint64 (address wraparound)")

	// ErrValueTooWide is reported when the computed value cannot be encoded in
	// the requested width (e.g. a value needing 8 bytes written with width 4).
	ErrValueTooWide = errors.New("netsec: relocation value does not fit in requested width (truncated write)")

	// ErrOverlappingRelocations is reported when two relocations touch the
	// same byte, including exact duplicates.
	ErrOverlappingRelocations = errors.New("netsec: overlapping or duplicate relocations")

	// ErrNotApplied is reported by SealTargets when Apply has not succeeded.
	ErrNotApplied = errors.New("netsec: SealTargets called before a successful Apply")
)

// Segment describes one loaded segment: a base virtual address, its byte
// content and its access permissions.
type Segment struct {
	Base  uint64
	Data  []byte
	Perms uint8
}

// Readable reports whether the segment grants read access.
func (s *Segment) Readable() bool { return s.Perms&PermRead != 0 }

// Writable reports whether the segment grants write access.
func (s *Segment) Writable() bool { return s.Perms&PermWrite != 0 }

// Executable reports whether the segment grants execute access.
func (s *Segment) Executable() bool { return s.Perms&PermExecute != 0 }

// Relocation is one attacker-controlled relocation entry. The value written is
// the mathematical integer (symbol segment base + SymbolOffset + Addend),
// encoded using Width bytes (4 or 8) little-endian at TargetOffset inside the
// target segment.
type Relocation struct {
	TargetID     uint64
	TargetOffset uint64
	Width        uint8
	SymbolID     uint64
	SymbolOffset uint64
	Addend       int64
}

// Loader owns a private, deep-copied view of the segment table. Input segments
// are never mutated directly; Apply swaps in a freshly built content table only
// after every relocation has been validated.
type Loader struct {
	segments []Segment
	applied  bool
	targets  map[uint64]struct{}
}

// NewLoader deep-copies segments and validates their address ranges. Empty
// (zero-length) segments occupy no address space and never overlap. It returns
// ErrOverlappingSegments if two non-empty ranges intersect.
func NewLoader(segments []Segment) (*Loader, error) {
	copied := make([]Segment, len(segments))
	for i := range segments {
		copied[i] = Segment{
			Base:  segments[i].Base,
			Perms: segments[i].Perms,
			Data:  append([]byte(nil), segments[i].Data...),
		}
	}

	type span struct {
		lo, hi uint64
	}
	var spans []span
	for i := range copied {
		length := uint64(len(copied[i].Data))
		if length == 0 {
			continue
		}
		end, ok := checkedAdd(copied[i].Base, length)
		if !ok {
			return nil, fmt.Errorf("%w: segment %d base+length exceeds uint64", ErrAddressOverflow, i)
		}
		spans = append(spans, span{lo: copied[i].Base, hi: end})
	}

	sort.Slice(spans, func(a, b int) bool { return spans[a].lo < spans[b].lo })
	for i := 1; i < len(spans); i++ {
		// Half-open ranges [lo, hi): touching ranges (lo == previous hi) are
		// disjoint and therefore allowed.
		if spans[i].lo < spans[i-1].hi {
			return nil, fmt.Errorf("%w: ranges overlap near address %#x", ErrOverlappingSegments, spans[i].lo)
		}
	}

	return &Loader{segments: copied}, nil
}

// Segments returns a deep copy of the loader's current segment table. Callers
// may mutate the returned slice without affecting loader state.
func (l *Loader) Segments() []Segment {
	out := make([]Segment, len(l.segments))
	for i := range l.segments {
		out[i] = Segment{
			Base:  l.segments[i].Base,
			Perms: l.segments[i].Perms,
			Data:  append([]byte(nil), l.segments[i].Data...),
		}
	}
	return out
}

// plannedReloc is a fully validated relocation ready to be written.
type plannedReloc struct {
	targetID     uint64
	targetOffset uint64
	width        uint8
	value        uint64
	absLo        uint64
	absHi        uint64
}

// Apply validates every relocation and, only if all of them are valid, writes
// them atomically into a fresh copy of the segment table. On any validation
// failure the loader (and the caller's original segments) stay byte-for-byte
// unchanged and the error is returned. A successful Apply may be retried; each
// call is validated independently against the current state.
func (l *Loader) Apply(relocs []Relocation) error {
	planned := make([]plannedReloc, 0, len(relocs))
	targets := make(map[uint64]struct{}, len(relocs))

	for idx := range relocs {
		plan, err := l.planOne(idx, relocs[idx])
		if err != nil {
			return err
		}
		planned = append(planned, plan)
		targets[plan.targetID] = struct{}{}
	}

	// Reject duplicate or intersecting relocations based on their absolute
	// target byte ranges.
	sort.Slice(planned, func(a, b int) bool {
		if planned[a].absLo != planned[b].absLo {
			return planned[a].absLo < planned[b].absLo
		}
		return planned[a].absHi < planned[b].absHi
	})
	for i := 1; i < len(planned); i++ {
		if planned[i].absLo < planned[i-1].absHi {
			return fmt.Errorf("%w: entries %d and %d share target bytes",
				ErrOverlappingRelocations, i-1, i)
		}
	}

	// Build the post-image before publishing anything, so a write failure can
	// never leave the segments partially modified.
	next := make([]Segment, len(l.segments))
	for i := range l.segments {
		next[i] = Segment{
			Base:  l.segments[i].Base,
			Perms: l.segments[i].Perms,
			Data:  append([]byte(nil), l.segments[i].Data...),
		}
	}
	for _, p := range planned {
		dst := next[p.targetID].Data[p.targetOffset:]
		if p.width == 8 {
			putUint64(dst, p.value)
		} else {
			putUint32(dst, p.value)
		}
	}

	l.segments = next
	l.applied = true
	l.targets = targets
	return nil
}

// planOne runs every per-entry validation and returns a write plan. No segment
// state is mutated here.
func (l *Loader) planOne(idx int, r Relocation) (plannedReloc, error) {
	if int(r.TargetID) >= len(l.segments) {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d target id %d",
			ErrInvalidTargetIndex, idx, r.TargetID)
	}
	if int(r.SymbolID) >= len(l.segments) {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d symbol id %d",
			ErrInvalidSymbolIndex, idx, r.SymbolID)
	}

	target := &l.segments[r.TargetID]
	symbol := &l.segments[r.SymbolID]

	if r.Width != 4 && r.Width != 8 {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d width %d",
			ErrInvalidWidth, idx, r.Width)
	}

	// Target must be writable and non-executable (no W+X).
	if !target.Writable() {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d target segment %d",
			ErrTargetNotWritable, idx, r.TargetID)
	}
	if target.Executable() {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d target segment %d",
			ErrTargetExecutable, idx, r.TargetID)
	}

	// The full-width write must lie inside the target content.
	width := uint64(r.Width)
	if r.TargetOffset > uint64(len(target.Data)) ||
		width > uint64(len(target.Data))-r.TargetOffset {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d offset %d width %d segment len %d",
			ErrTargetOutOfRange, idx, r.TargetOffset, r.Width, len(target.Data))
	}

	// Symbol must live in a readable segment and the symbol must be in range.
	if !symbol.Readable() {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d symbol segment %d",
			ErrSymbolNotReadable, idx, r.SymbolID)
	}
	if r.SymbolOffset >= uint64(len(symbol.Data)) {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d symbol offset %d segment len %d",
			ErrSymbolOutOfRange, idx, r.SymbolOffset, len(symbol.Data))
	}

	// Symbol address = base + offset, checked for uint64 wraparound.
	symAddr, ok := checkedAdd(symbol.Base, r.SymbolOffset)
	if !ok {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d symbol base+offset",
			ErrAddressOverflow, idx)
	}

	// value = symbol address + signed addend, computed over mathematical
	// integers and rejected if it cannot be represented as uint64.
	value, ok := addSigned(symAddr, r.Addend)
	if !ok {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d value 0x%x + (%d)",
			ErrAddressOverflow, idx, symAddr, r.Addend)
	}

	// A width-4 write must encode the full value without truncation.
	if r.Width == 4 && value > uint64(^uint32(0)) {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d value %#x width 4",
			ErrValueTooWide, idx, value)
	}

	absLo, ok := checkedAdd(target.Base, r.TargetOffset)
	if !ok {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d target base+offset",
			ErrAddressOverflow, idx)
	}
	absHi, ok := checkedAdd(absLo, width)
	if !ok {
		return plannedReloc{}, fmt.Errorf("%w: relocation %d target range end",
			ErrAddressOverflow, idx)
	}

	return plannedReloc{
		targetID:     r.TargetID,
		targetOffset: r.TargetOffset,
		width:        r.Width,
		value:        value,
		absLo:        absLo,
		absHi:        absHi,
	}, nil
}

// SealTargets clears the write permission bit on exactly the segments that
// received a relocation during the last successful Apply, letting the host seal
// those target segments as read-only. It returns ErrNotApplied if Apply has not
// produced a successful relocation set.
func (l *Loader) SealTargets() error {
	if !l.applied {
		return ErrNotApplied
	}
	for id := range l.targets {
		if int(id) < len(l.segments) {
			l.segments[id].Perms &^= PermWrite
		}
	}
	l.applied = false
	l.targets = nil
	return nil
}

// checkedAdd returns a+b and false if the addition wraps past uint64.
func checkedAdd(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}

// addSigned computes the mathematical integer value v + addend and reports
// whether the result is representable as uint64.
func addSigned(v uint64, addend int64) (uint64, bool) {
	if addend >= 0 {
		return checkedAdd(v, uint64(addend))
	}
	// A negative addend k contributes -(-k). Interpreting -k as uint64 yields
	// exactly its magnitude, so subtraction underflows precisely when the
	// magnitude exceeds v.
	magnitude := uint64(-addend)
	if magnitude > v {
		return 0, false
	}
	return v - magnitude, true
}

func putUint64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

func putUint32(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
