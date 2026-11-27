package writeproof

import "errors"

const (
	MaxLogicalBits = 48
	MaxPhysicalBits = 63
)

type Status string

const (
	StatusSafe     Status = "safe"
	StatusConflict Status = "conflict"
	StatusUnknown  Status = "unknown"
)

type Phase string

const (
	PhaseErase Phase = "erase"
	PhaseWrite Phase = "write"
)

type ProtectionKind string

const (
	ProtectionLogical ProtectionKind = "logical"
	ProtectionPhysical ProtectionKind = "physical"
)

// AffineTransform is y = A*x + b over GF(2). Each row stores one matrix
// coefficient per logical-address bit; row i produces physical-address bit i.
type AffineTransform struct {
	Rows [MaxLogicalBits]uint64
	Bias uint64
}

func (t AffineTransform) Apply(x uint64) uint64 {
	var y uint64
	for bit := 0; bit < MaxLogicalBits; bit++ {
		if parity(t.Rows[bit]&x) != 0 {
			y |= uint64(1) << bit
		}
	}
	return y ^ t.Bias
}

type WriteSegment struct {
	Start   uint64
	End     uint64
	Mask    uint64
	Match   uint64
	Transform AffineTransform
}

type Protection struct {
	Kind      ProtectionKind
	Start     uint64
	End       uint64
	Transform AffineTransform
}

type Request struct {
	Writes         []WriteSegment
	Protections    []Protection
	EraseBlockSize uint64
	PhysicalBits   int
	Budget         Budget
	logicalBits    int
}

type Budget struct {
	Unlimited      bool
	MaxSystemOps   int
	MaxProofs      int
	UsedSystemOps  int
	UsedProofs     int
}

type LinearEquation struct {
	Terms    []uint64
	Constant bool
}

type LinearSystem struct {
	Variables  int
	Equations  []LinearEquation
}

type UnsatCertificate struct {
	System       LinearSystem
	PivotColumns []int
	Contradiction LinearEquation
}

type Solution struct {
	System LinearSystem
	Values []bool
}

type BitCube struct {
	Base     uint64
	FreeMask uint64
}

type IntervalDecomposition struct {
	Start uint64
	End   uint64
	Cubes []BitCube
}

type DomainCube struct {
	Base       uint64
	FreeMask   uint64
	FixedMask  uint64
	FixedValue uint64
}

type DomainDecomposition struct {
	Start uint64
	End   uint64
	Mask  uint64
	Match uint64
	Cubes []DomainCube
}

type ProtectionProof struct {
	Index    int
	Logical  *DomainDecomposition
	Physical *IntervalDecomposition
}

type DisjointProof struct {
	Phase               Phase
	ProtectionKind      ProtectionKind
	WriteCubeIndex      int
	ProtectionIndex     int
	ProtectionCubeIndex int
	Certificate         UnsatCertificate
}

type Preimage struct {
	CubeIndex       int
	LogicalAddress  uint64
	PhysicalAddress uint64
	Solution        Solution
}

type Conflict struct {
	ActionIndex        int
	Phase              Phase
	ProtectionIndex    int
	WriteCubeIndex     int
	ProtectionCubeIndex int
	PhysicalAddress    uint64
	WritePreimage      *Preimage
	ProtectedPreimage  *Preimage
}

type ActionProof struct {
	Index           int
	Domain          DomainDecomposition
	EraseProofs     []DisjointProof
	WriteProofs     []DisjointProof
}

type Report struct {
	Status      Status
	Reason      string
	Conflict    *Conflict
	Actions     []ActionProof
	Protections []ProtectionProof
	Budget      Budget
}

var (
	ErrInvalidRange        = errors.New("writeproof: half-open range is invalid")
	ErrInvalidAddress      = errors.New("writeproof: address exceeds configured address width")
	ErrInvalidMask         = errors.New("writeproof: mask or match is invalid")
	ErrInvalidBlockSize    = errors.New("writeproof: erase block size must be a power of two")
	ErrInvalidWidth        = errors.New("writeproof: address width is invalid")
	ErrInvalidTransform    = errors.New("writeproof: transform contains bits outside configured width")
	ErrUnknownProtection   = errors.New("writeproof: unknown protection kind")
)
