package ratbez

import "math/big"

// Candidate kinds.
const (
	KindT0   = "t0"   // endpoint t = 0
	KindT1   = "t1"   // endpoint t = 1
	KindRoot = "root" // interior stationary point
)

// IvCert is a rational interval in a certificate.
type IvCert struct {
	Lo *big.Rat `json:"lo"`
	Hi *big.Rat `json:"hi"`
}

// SegCert certifies the monotonicity of R on one open segment between two
// consecutive critical points: the derivative numerator has sign Sign at the
// rational Sample point inside the segment (and no root inside it).
type SegCert struct {
	Sample *big.Rat `json:"sample"`
	Sign   int      `json:"sign"` // +1 increasing, -1 decreasing
}

// CandCert certifies one extremum candidate (an endpoint or an interior
// stationary point) together with a certified enclosure of its value.
type CandCert struct {
	Kind      string   `json:"kind"`
	RootIndex int      `json:"rootIndex,omitempty"` // for KindRoot: index into Cert.Roots
	TL        *big.Rat `json:"tLo,omitempty"`       // for KindRoot: refined isolating interval
	TH        *big.Rat `json:"tHi,omitempty"`
	VLo       *big.Rat `json:"vLo"` // certified enclosure of the candidate value R(t)
	VHi       *big.Rat `json:"vHi"`
	VExact    *big.Rat `json:"vExact,omitempty"` // if set: value is exactly this rational (gcd-certified)
	SL        *big.Rat `json:"sLo,omitempty"`    // for KindRoot (when S present): isolating interval
	SH        *big.Rat `json:"sHi,omitempty"`    // of the value polynomial S containing R(t)
	Group     int      `json:"group"`            // candidates in one group have exactly equal values
}

// Cert is a complete, independently checkable certificate for the global
// extrema of a rational Bezier curve on [0,1].
type Cert struct {
	P          []*big.Rat `json:"p"`   // control values (rationals)
	W          []*big.Rat `json:"w"`   // positive weights (rationals)
	Eps        *big.Rat   `json:"eps"` // required enclosure width (positive rational)
	Constant   bool       `json:"constant"`
	ConstValue *big.Rat   `json:"constValue,omitempty"`
	MPrim      []string   `json:"mPrim,omitempty"` // integer coeffs of the derivative numerator M
	Msf        []string   `json:"msf,omitempty"`   // coeffs of the monic square-free part of M
	Roots      []IvCert   `json:"roots,omitempty"` // isolating intervals of all interior stationary points
	Segments   []SegCert  `json:"segments,omitempty"`
	S          []string   `json:"s,omitempty"` // value polynomial (present iff >= 2 interior roots)
	Candidates []CandCert `json:"candidates,omitempty"`
	MaxGroup   int        `json:"maxGroup"`
	MinGroup   int        `json:"minGroup"`
	MaxLo      *big.Rat   `json:"maxLo"` // certified enclosure of the global maximum value
	MaxHi      *big.Rat   `json:"maxHi"`
	MinLo      *big.Rat   `json:"minLo"` // certified enclosure of the global minimum value
	MinHi      *big.Rat   `json:"minHi"`
}
