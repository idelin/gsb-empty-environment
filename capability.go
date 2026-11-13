package netsec

import (
	"errors"
	"math/bits"
	"strings"
)

// Perm is a permission set. The four object permissions Load, Store,
// Execute and Derive govern memory access and further derivation. Unseal is
// reserved to host keys and may never appear on a capability built from a
// request.
type Perm uint8

const (
	Load    Perm = 1 << 0
	Store   Perm = 1 << 1
	Execute Perm = 1 << 2
	Derive  Perm = 1 << 3
	Unseal  Perm = 1 << 4

	// ObjectPerms are the permissions that may appear on ordinary object
	// capabilities and in requests.
	ObjectPerms Perm = Load | Store | Execute | Derive
	// AllPerms additionally includes the host-reserved Unseal permission.
	AllPerms Perm = ObjectPerms | Unseal
)

func (p Perm) String() string {
	if p == 0 {
		return "none"
	}
	names := []struct {
		bit  Perm
		text string
	}{
		{Load, "Load"},
		{Store, "Store"},
		{Execute, "Execute"},
		{Derive, "Derive"},
		{Unseal, "Unseal"},
	}
	var parts []string
	for _, name := range names {
		if p&name.bit != 0 {
			parts = append(parts, name.text)
		}
	}
	if unknown := p &^ AllPerms; unknown != 0 {
		parts = append(parts, "unknown")
	}
	return strings.Join(parts, "|")
}

// MinPrecision and MaxPrecision bound the caller-selectable encoding
// precision p.
const (
	MinPrecision = 4
	MaxPrecision = 16
)

// Sentinel errors. Wrapped errors remain matchable with errors.Is.
var (
	// ErrInvalidPrecision is returned when p is outside [4, 16].
	ErrInvalidPrecision = errors.New("netsec: encoding precision must be between 4 and 16")
	// ErrRangeOverflow is returned when a requested or encoded range reaches
	// or crosses the top of the address space (limit).
	ErrRangeOverflow = errors.New("netsec: address range overflows the address space")
	// ErrInvalidRange is returned for a malformed base/length pair.
	ErrInvalidRange = errors.New("netsec: invalid address range")
	// ErrCursorOutside is returned when a cursor is not within [base, top].
	ErrCursorOutside = errors.New("netsec: cursor outside encoded range")
	// ErrTagCleared is returned when an operation requires a tagged
	// capability and the tag is clear.
	ErrTagCleared = errors.New("netsec: capability tag is clear")
	// ErrSealed is returned when a sealed capability is used for an operation
	// other than Unseal through a matching host key.
	ErrSealed = errors.New("netsec: sealed capability cannot be used or derived")
	// ErrNotSealed is returned when Unseal targets an unsealed capability.
	ErrNotSealed = errors.New("netsec: capability is not sealed")
	// ErrPermissionEscalation is returned when a derived capability requests
	// a permission its parent does not hold.
	ErrPermissionEscalation = errors.New("netsec: derived permissions must be a subset of the parent permissions")
	// ErrInexactDenied is returned when rounded encoding covers bytes outside
	// the request and the caller did not opt in with AllowInexact.
	ErrInexactDenied = errors.New("netsec: inexact encoding denied; rounded range covers bytes outside the request")
	// ErrOutsideParent is returned when an encoded range escapes the parent
	// capability's original range.
	ErrOutsideParent = errors.New("netsec: encoded range escapes the parent capability range")
	// ErrUnsealForbidden is returned when a request attempts to obtain the
	// host-reserved Unseal permission.
	ErrUnsealForbidden = errors.New("netsec: Unseal permission is reserved to host keys and cannot be requested")
	// ErrInvalidPermission is returned for unknown permission bits.
	ErrInvalidPermission = errors.New("netsec: unknown permission bits")
	// ErrWrongKey is returned for an unusable or mismatched unseal key.
	ErrWrongKey = errors.New("netsec: unseal key does not match sealed type or lacks Unseal permission")
)

// Interval is an inclusive [Low, High] byte interval.
type Interval struct {
	Low  uint64
	High uint64
}

func (i Interval) contains(addr uint64) bool {
	return addr >= i.Low && addr <= i.High
}

// ExtraCoverage reports bytes that rounded encoding adds on each side of the
// requested [base, base+length) range. A side with no extra bytes is zero.
type ExtraCoverage struct {
	Left  Interval
	Right Interval
}

// Cap is an architectural capability. A zero value with Tag false is invalid.
type Cap struct {
	Base       uint64
	Length     uint64
	Cursor     uint64
	Perms      Perm
	SealedType uint32
	Sealed     bool
	Tag        bool
	Generation uint64
	// keyID is zero for ordinary object capabilities. Host unseal keys carry
	// a non-zero id registered with the host so that a key-shaped struct
	// fabricated by a request cannot authenticate an Unseal.
	keyID uint64
}

// Top returns the exclusive upper bound of the capability range.
func (c Cap) Top() uint64 {
	return c.Base + c.Length
}

// Host implements the secure capability host. NewHost produces a host for the
// full 32-bit address space; testHost with a smaller limit drives exhaustive
// enumeration over a small address domain while reusing identical logic.
type Host struct {
	limit      uint64
	generation uint64
	keySerial  uint64
	keys       map[uint64]bool
}

// NewHost creates a host whose address space covers [0, 2^32).
func NewHost() *Host {
	return &Host{limit: uint64(1) << 32, keys: make(map[uint64]bool)}
}

func testHost(limit uint64) *Host {
	return &Host{limit: limit, keys: make(map[uint64]bool)}
}

// Encoding is the finite-precision encoding of a requested base/length pair.
type Encoding struct {
	Precision int
	E         int
	Granule   uint64
	Base      uint64
	Top       uint64
	Length    uint64
	Exact     bool
	Extra     ExtraCoverage
}

// checkRange validates a requested [base, base+length) range against the
// address space limit and reports arithmetic overflow.
func (h *Host) checkRange(base, length uint64) error {
	top, carry := bits.Add64(base, length, 0)
	if carry != 0 || top > h.limit || base > h.limit {
		return ErrRangeOverflow
	}
	if length == 0 && base == h.limit {
		return ErrRangeOverflow
	}
	return nil
}

// Encode encodes the requested [base, base+length) range at precision p.
//
// For a non-zero length it chooses the smallest e such that
// length <= (2^p) * 2^e, rounds base down and top=base+length up to multiples
// of 2^e, and reports whether the encoding is exact plus the extra covered
// byte intervals. A zero-length object always encodes exactly with granule 1.
func (h *Host) Encode(base, length uint64, p int) (Encoding, error) {
	if p < MinPrecision || p > MaxPrecision {
		return Encoding{}, ErrInvalidPrecision
	}
	if err := h.checkRange(base, length); err != nil {
		return Encoding{}, err
	}

	enc := Encoding{Precision: p}
	if length == 0 {
		enc.Granule = 1
		enc.Base = base
		enc.Top = base
		enc.Length = 0
		enc.Exact = true
		return enc, nil
	}

	requestTop := base + length
	granule := uint64(1)
	e := 0
	for length > (uint64(1)<<uint(p))*granule {
		granule <<= 1
		e++
	}
	enc.E = e
	enc.Granule = granule
	enc.Base = (base / granule) * granule
	enc.Top = ((requestTop + granule - 1) / granule) * granule
	enc.Length = enc.Top - enc.Base
	enc.Exact = enc.Base == base && enc.Top == requestTop

	if enc.Top > h.limit {
		return enc, ErrRangeOverflow
	}
	if enc.Base < base {
		enc.Extra.Left = Interval{Low: enc.Base, High: base - 1}
	}
	if enc.Top > requestTop {
		enc.Extra.Right = Interval{Low: requestTop, High: enc.Top - 1}
	}
	return enc, nil
}

// DeriveRequest is a child-capability derivation request.
type DeriveRequest struct {
	Base         uint64
	Length       uint64
	Cursor       uint64
	Perms        Perm
	Precision    int
	AllowInexact bool
}

// DeriveResult is the derived capability together with its encoding details.
type DeriveResult struct {
	Cap      Cap
	Encoding Encoding
}

// Derive encodes the requested sub-range and derives a child capability from
// parent. Derivation may only narrow permissions and range; the object
// generation is preserved. An inexact (rounded) encoding is rejected unless
// the caller sets AllowInexact and the parent's original range fully contains
// the encoded range. Any overflow, permission escalation or out-of-range
// cursor makes derivation fail.
func (h *Host) Derive(parent Cap, req DeriveRequest) (DeriveResult, error) {
	if !parent.Tag {
		return DeriveResult{}, ErrTagCleared
	}
	if parent.Sealed {
		return DeriveResult{}, ErrSealed
	}
	if parent.Perms&Derive == 0 {
		return DeriveResult{}, ErrPermissionEscalation
	}
	if req.Perms&Unseal != 0 {
		return DeriveResult{}, ErrUnsealForbidden
	}
	if req.Perms&^ObjectPerms != 0 {
		return DeriveResult{}, ErrInvalidPermission
	}
	if req.Perms&^parent.Perms != 0 {
		return DeriveResult{}, ErrPermissionEscalation
	}

	enc, err := h.Encode(req.Base, req.Length, req.Precision)
	if err != nil {
		return DeriveResult{}, err
	}
	if !enc.Exact && !req.AllowInexact {
		return DeriveResult{Encoding: enc}, ErrInexactDenied
	}
	if enc.Base < parent.Base || enc.Top > parent.Top() {
		return DeriveResult{Encoding: enc}, ErrOutsideParent
	}
	if req.Cursor < enc.Base || req.Cursor > enc.Top {
		return DeriveResult{Encoding: enc}, ErrCursorOutside
	}

	child := Cap{
		Base:       enc.Base,
		Length:     enc.Length,
		Cursor:     req.Cursor,
		Perms:      req.Perms,
		Tag:        true,
		Generation: parent.Generation,
	}
	return DeriveResult{Cap: child, Encoding: enc}, nil
}

// FromRequest constructs a root object capability from an explicit request.
// The host stamps the tag and a fresh generation. Requested capabilities are
// always unsealed and can never carry the Unseal permission; host keys must be
// minted through MintKey instead and therefore cannot be forged by callers.
func (h *Host) FromRequest(base, length, cursor uint64, perms Perm, p int, allowInexact bool) (Cap, Encoding, error) {
	if perms&^ObjectPerms != 0 {
		if perms&Unseal != 0 {
			return Cap{}, Encoding{}, ErrUnsealForbidden
		}
		return Cap{}, Encoding{}, ErrInvalidPermission
	}
	enc, err := h.Encode(base, length, p)
	if err != nil {
		return Cap{}, enc, err
	}
	if !enc.Exact && !allowInexact {
		return Cap{}, enc, ErrInexactDenied
	}
	if cursor < enc.Base || cursor > enc.Top {
		return Cap{}, enc, ErrCursorOutside
	}
	h.generation++
	root := Cap{
		Base:       enc.Base,
		Length:     enc.Length,
		Cursor:     cursor,
		Perms:      perms,
		Tag:        true,
		Generation: h.generation,
	}
	return root, enc, nil
}

// MintKey creates a host key capability bound to sealedType and carrying
// Unseal. It has no address range and cannot be produced from a request.
func (h *Host) MintKey(sealedType uint32) Cap {
	h.generation++
	h.keySerial++
	h.keys[h.keySerial] = true
	return Cap{
		Perms:      Unseal,
		SealedType: sealedType,
		Tag:        true,
		Generation: h.generation,
		keyID:      h.keySerial,
	}
}

// Seal marks a tagged, unsealed capability as sealed with sealedType and
// strips its object permissions. A sealed capability cannot be loaded, stored,
// executed or derived until Unseal restores it through a matching host key.
func (h *Host) Seal(c Cap, sealedType uint32) (Cap, error) {
	if !c.Tag {
		return Cap{}, ErrTagCleared
	}
	if c.Sealed {
		return Cap{}, ErrSealed
	}
	c.Sealed = true
	c.SealedType = sealedType
	c.Perms = 0
	return c, nil
}

// Unseal restores a sealed capability using a host key. The key must be
// tagged, hold Unseal, and carry the same sealed type. The original range,
// cursor, tag and generation are preserved; permissions at seal time had been
// cleared, so callers re-derive narrowed permissions after unsealing.
func (h *Host) Unseal(sealed Cap, key Cap) (Cap, error) {
	if !sealed.Tag {
		return Cap{}, ErrTagCleared
	}
	if !sealed.Sealed {
		return Cap{}, ErrNotSealed
	}
	if !key.Tag || key.Sealed || key.Perms&Unseal == 0 ||
		key.keyID == 0 || !h.keys[key.keyID] || key.SealedType != sealed.SealedType {
		return Cap{}, ErrWrongKey
	}
	sealed.Sealed = false
	sealed.SealedType = 0
	sealed.Perms = 0
	return sealed, nil
}

// Accessible reports whether address a lies within the capability's range and
// the capability is tagged and unsealed. It deliberately ignores permissions;
// use CanLoad/CanStore/CanExecute for permission checks.
func (c Cap) Accessible(a uint64) bool {
	return c.Tag && !c.Sealed && a >= c.Base && a < c.Top()
}

// CanLoad reports a Load at address a through the capability.
func (c Cap) CanLoad(a uint64) bool {
	return c.Accessible(a) && c.Perms&Load != 0
}

// CanStore reports a Store at address a through the capability.
func (c Cap) CanStore(a uint64) bool {
	return c.Accessible(a) && c.Perms&Store != 0
}

// CanExecute reports instruction execution at address a through the
// capability.
func (c Cap) CanExecute(a uint64) bool {
	return c.Accessible(a) && c.Perms&Execute != 0
}
