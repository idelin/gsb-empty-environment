package admission

import (
	"errors"
	"fmt"
	"math"
)

const (
	// MaxSectors is the largest logical volume length, and also the exclusive
	// upper bound of the physical sector address space accepted by this package.
	MaxSectors uint64 = 1 << 48
	// MaxLayers is the maximum number of reachable volume nodes on one path,
	// including the root and a physical leaf volume.
	MaxLayers = 8
)

var (
	ErrInvalid         = errors.New("admission: invalid mapping")
	ErrNotFound        = errors.New("admission: volume not found")
	ErrCycle           = errors.New("admission: cycle in reachable volume graph")
	ErrTooDeep         = errors.New("admission: mapping exceeds maximum layers")
	ErrOverflow        = errors.New("admission: sector offset overflow")
	ErrOverlap         = errors.New("admission: overlapping logical segments")
	ErrUnmapped        = errors.New("admission: virtual range is not fully mapped")
	ErrNotOwned        = errors.New("admission: physical range is not tenant-owned")
	ErrNotWritable     = errors.New("admission: physical range is not tenant-writable")
	ErrIsolation       = errors.New("admission: isolated volumes share writable physical sectors")
	ErrProofTooComplex = errors.New("admission: proof exceeds arithmetic-class complexity limit")
)

// Permission is a permission lattice. The zero value grants nothing. Permissions
// only narrow along a mapping path, using bitwise intersection.
type Permission uint8

const (
	PermNone      Permission = 0
	PermRead      Permission = 1 << 0
	PermWrite     Permission = 1 << 1
	PermReadWrite Permission = PermRead | PermWrite
)

func (p Permission) valid() bool {
	return p&^(PermRead|PermWrite) == 0
}

// AllowsRead reports whether p contains read permission.
func (p Permission) AllowsRead() bool { return p&PermRead != 0 }

// AllowsWrite reports whether p contains write permission.
func (p Permission) AllowsWrite() bool { return p&PermWrite != 0 }

// Narrow returns the permission that is common to both permissions.
func (p Permission) Narrow(q Permission) Permission { return p & q }

func (p Permission) String() string {
	switch p {
	case PermNone:
		return "none"
	case PermRead:
		return "read"
	case PermWrite:
		return "write"
	case PermReadWrite:
		return "read-write"
	default:
		return fmt.Sprintf("permission(%d)", p)
	}
}

// Span is a half-open sector interval [Start, Start+Length).
type Span struct {
	Start  uint64
	Length uint64
}

// PhysicalRange is a tenant-owned physical sector range. A physical leaf may
// only map into these ranges; a writable leaf may only map into ranges carrying
// PermWrite. Ranges must be non-overlapping.
type PhysicalRange struct {
	Start      uint64
	Length     uint64
	Permission Permission
}

// IsolationPair declares that two volumes must not expose the same physical
// sector through any effective-writable logical location.
type IsolationPair struct {
	A string
	B string
}

// MapKind identifies the leaf operation of a segment.
type MapKind uint8

const (
	MapInvalid MapKind = iota
	MapLinear
	MapStripe
	MapPhysical
)

func (k MapKind) String() string {
	switch k {
	case MapLinear:
		return "linear"
	case MapStripe:
		return "stripe"
	case MapPhysical:
		return "physical"
	default:
		return "invalid"
	}
}

// StripeSide identifies one branch of a two-subvolume stripe segment.
type StripeSide uint8

const (
	StripeNone StripeSide = iota
	StripeLeft
	StripeRight
)

func (s StripeSide) String() string {
	switch s {
	case StripeLeft:
		return "left"
	case StripeRight:
		return "right"
	default:
		return "none"
	}
}

// Mapping describes how a segment addresses the layer below it.
//
// MapLinear maps the segment's local offset x to ChildOffset+x.
// MapStripe maps local stripe 2k to LeftOffset+k*StripeWidth and local stripe
// 2k+1 to RightOffset+k*StripeWidth. Only StripeWidth, a power of two, is used
// as the local stripe width.
// MapPhysical maps local offset x to PhysicalStart+x.
type Mapping struct {
	Kind MapKind

	Child       string
	ChildOffset uint64

	Left        string
	LeftOffset  uint64
	Right       string
	RightOffset uint64
	StripeWidth uint64

	PhysicalStart uint64
}

// Segment is a non-overlapping logical interval in a Volume.
type Segment struct {
	VirtualStart uint64
	Length       uint64
	Permission   Permission
	Mapping      Mapping
}

// Linear is a convenience constructor for a linear segment.
func Linear(virtualStart, length uint64, perm Permission, child string, childOffset uint64) Segment {
	return Segment{
		VirtualStart: virtualStart,
		Length:       length,
		Permission:   perm,
		Mapping: Mapping{
			Kind:        MapLinear,
			Child:       child,
			ChildOffset: childOffset,
		},
	}
}

// Stripe is a convenience constructor for a two-subvolume stripe segment.
func Stripe(virtualStart, length uint64, perm Permission, width uint64,
	left string, leftOffset uint64, right string, rightOffset uint64) Segment {
	return Segment{
		VirtualStart: virtualStart,
		Length:       length,
		Permission:   perm,
		Mapping: Mapping{
			Kind:        MapStripe,
			Left:        left,
			LeftOffset:  leftOffset,
			Right:       right,
			RightOffset: rightOffset,
			StripeWidth: width,
		},
	}
}

// Physical is a convenience constructor for a physical leaf segment.
func Physical(virtualStart, length uint64, perm Permission, physicalStart uint64) Segment {
	return Segment{
		VirtualStart: virtualStart,
		Length:       length,
		Permission:   perm,
		Mapping: Mapping{
			Kind:          MapPhysical,
			PhysicalStart: physicalStart,
		},
	}
}

// Volume is a node in the mapping graph. Segments must not overlap. Volume
// length is at most MaxSectors. IsolationGroup is optional; every pair of
// volumes carrying the same non-empty group is treated as isolated.
type Volume struct {
	Name           string
	Length         uint64
	Segments       []Segment
	IsolationGroup string
}

// Graph is one complete mapping version. If Roots is empty, all volumes that
// are not referenced by another volume's segment become roots. Only volumes
// reachable from Roots are analysed.
type Graph struct {
	Volumes    []Volume
	Roots      []string
	Physical   []PhysicalRange
	Isolations []IsolationPair
}

// Class is a compact, exact set of sector intervals:
//
//	for base := Start; each repeat combination adds repeat offsets; the inner
//	set is [base+i*Stride, base+i*Stride+Run) for 0 <= i < Count.
//
// Repeats are Cartesian dimensions. They are used for periodic stripe images so
// that even a stripe width of one does not force one proof record per sector.
type Class struct {
	Start   uint64
	Run     uint64
	Stride  uint64
	Count   uint64
	Repeats []Repeat
}

// Repeat replicates an inner class at offsets k*Stride for 0 <= k < Count.
type Repeat struct {
	Stride uint64
	Count  uint64
}

func (c Class) validate() error {
	if c.Count == 0 {
		return fmt.Errorf("%w: class count is zero", ErrInvalid)
	}
	if c.Run == 0 || c.Run > c.Stride {
		return fmt.Errorf("%w: class run must be in [1,stride]", ErrInvalid)
	}
	var prev uint64
	for i, r := range c.Repeats {
		if r.Count == 0 || r.Stride == 0 {
			return fmt.Errorf("%w: invalid repeat", ErrInvalid)
		}
		if i > 0 && r.Stride <= prev {
			return fmt.Errorf("%w: repeats must have increasing strides", ErrInvalid)
		}
		prev = r.Stride
	}
	if _, ok := c.maxEnd(); !ok {
		return fmt.Errorf("%w: class end overflow", ErrOverflow)
	}
	return nil
}

func (c Class) innerEnd() (uint64, bool) {
	if c.Count == 0 {
		return c.Start, true
	}
	steps := c.Count - 1
	m, ok := mulOK(steps, c.Stride)
	if !ok {
		return 0, false
	}
	m, ok = addOK(m, c.Run)
	if !ok {
		return 0, false
	}
	return addOK(c.Start, m)
}

func (c Class) maxEnd() (uint64, bool) {
	end, ok := c.innerEnd()
	if !ok {
		return 0, false
	}
	for _, r := range c.Repeats {
		steps := r.Count - 1
		off, ok := mulOK(steps, r.Stride)
		if !ok {
			return 0, false
		}
		end, ok = addOK(end, off)
		if !ok {
			return 0, false
		}
	}
	return end, true
}

// Size returns the number of sectors in the class.
func (c Class) Size() (uint64, bool) {
	n := c.Run
	if c.Count != 0 {
		var ok bool
		n, ok = mulOK(n, c.Count)
		if !ok {
			return 0, false
		}
	}
	for _, r := range c.Repeats {
		var ok bool
		n, ok = mulOK(n, r.Count)
		if !ok {
			return 0, false
		}
	}
	return n, true
}

// Step is one independently checkable mapping layer in a proof.
type Step struct {
	Volume       string
	Segment      int
	SegmentStart uint64
	SegmentLen   uint64
	Kind         MapKind
	Side         StripeSide
	Mapping      Mapping
	SegmentPerm  Permission

	Before   Permission
	After    Permission
	Narrowed bool
}

// Claim maps one virtual arithmetic class to one physical arithmetic class,
// together with the ordered path and every permission narrowing point.
type Claim struct {
	Virtual          Class
	Physical         Class
	Access           Permission
	LeafPermission   Permission
	TenantPermission Permission
	Path             []Step
}

// Proof is a replayable certificate for a virtual range. Claims are disjoint in
// the virtual domain and cover the requested range exactly.
type Proof struct {
	Volume   string
	Virtual  Span
	Writable bool
	Claims   []Claim
	Version  uint64
}

// Fragment is a resolved physical image for one virtual class.
type Fragment struct {
	Virtual  Class
	Physical Class
	Access   Permission
	Path     []Step
}

func mulOK(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > math.MaxUint64/b {
		return 0, false
	}
	return a * b, true
}

func addOK(a, b uint64) (uint64, bool) {
	if b > math.MaxUint64-a {
		return 0, false
	}
	return a + b, true
}

func ceilDiv(a, b uint64) uint64 {
	if a == 0 {
		return 0
	}
	return 1 + (a-1)/b
}

func floorDiv(a, b uint64) uint64 {
	return a / b
}
