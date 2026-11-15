package netsec

import (
	"crypto/elliptic"
	"fmt"
	"math/big"
)

// GroupID is an SAE group number as carried in the Authentication frame.
type GroupID uint16

const (
	// GroupP192 is NIST P-192 (SAE group 25).
	GroupP192 GroupID = 25
	// GroupP224 is NIST P-224 (SAE group 26).
	GroupP224 GroupID = 26
	// GroupP256 is NIST P-256 (SAE group 19).
	GroupP256 GroupID = 19
	// GroupP384 is NIST P-384 (SAE group 20).
	GroupP384 GroupID = 20
	// GroupP521 is NIST P-521 (SAE group 21).
	GroupP521 GroupID = 21
)

var (
	two = big.NewInt(2)

	// P-192 is no longer exposed by crypto/elliptic, but CurveParams still
	// provides the validation operations needed here (all NIST curves use
	// a = -3).
	p192 = func() elliptic.Curve {
		params := &elliptic.CurveParams{Name: "P-192"}
		params.P, _ = new(big.Int).SetString("fffffffffffffffffffffffffffffffeffffffffffffffff", 16)
		params.N, _ = new(big.Int).SetString("ffffffffffffffffffffffff99def836146bc9b1b4d22831", 16)
		params.B, _ = new(big.Int).SetString("64210519e59c80e70fa7e9ab72243049feb8deecc146b9b1", 16)
		params.Gx, _ = new(big.Int).SetString("188da80eb03090f67cbf20eb43a18800f4ff0afd82ff1012", 16)
		params.Gy, _ = new(big.Int).SetString("07192b95ffc8da78631011ed6b24cdd573f977a11e794811", 16)
		params.BitSize = 192
		return params
	}()
)

type groupSpec struct {
	id    GroupID
	curve elliptic.Curve
	size  int
	order *big.Int
}

func lookupGroup(id GroupID) (groupSpec, error) {
	var curve elliptic.Curve
	switch id {
	case GroupP192:
		curve = p192
	case GroupP224:
		curve = elliptic.P224()
	case GroupP256:
		curve = elliptic.P256()
	case GroupP384:
		curve = elliptic.P384()
	case GroupP521:
		curve = elliptic.P521()
	default:
		return groupSpec{}, fmt.Errorf("%w: %d", ErrUnsupportedGroup, id)
	}
	params := curve.Params()
	return groupSpec{
		id:    id,
		curve: curve,
		size:  (params.BitSize + 7) / 8,
		order: params.N,
	}, nil
}

// String returns the curve name, or the numeric group ID when unsupported.
func (g GroupID) String() string {
	switch g {
	case GroupP192:
		return "P-192"
	case GroupP224:
		return "P-224"
	case GroupP256:
		return "P-256"
	case GroupP384:
		return "P-384"
	case GroupP521:
		return "P-521"
	default:
		return fmt.Sprintf("SAE-group-%d", uint16(g))
	}
}

// Supported reports whether g can be validated by this package.
func (g GroupID) Supported() bool {
	_, err := lookupGroup(g)
	return err == nil
}

// Size returns the encoded field-element size in bytes, or zero when the
// group is unsupported.
func (g GroupID) Size() int {
	spec, err := lookupGroup(g)
	if err != nil {
		return 0
	}
	return spec.size
}

// Order returns a copy of the group order r, or nil when the group is
// unsupported.
func (g GroupID) Order() *big.Int {
	spec, err := lookupGroup(g)
	if err != nil {
		return nil
	}
	return new(big.Int).Set(spec.order)
}

// Commit is the peer's SAE commit contribution.
//
// LocalElement is the AP's own commit element for the same handshake. It is
// used to reject reflected commits. It may be omitted only by callers that
// perform that check separately; normal Authentication frame handling should
// always supply it.
//
// Token is empty for an initial commit. When a challenge is returned the
// caller retries with the token in this field.
type Commit struct {
	Group        GroupID
	Scalar       []byte
	Element      []byte
	LocalElement []byte
	Token        []byte
}

func validateScalar(spec groupSpec, encoded []byte) error {
	if len(encoded) == 0 || len(encoded) > spec.size {
		return ErrInvalidScalar
	}
	scalar := new(big.Int).SetBytes(encoded)
	if scalar.Cmp(two) < 0 || scalar.Cmp(spec.order) >= 0 {
		return ErrInvalidScalar
	}
	return nil
}

func isZero(b []byte) bool {
	var nonzero byte
	for _, v := range b {
		nonzero |= v
	}
	return nonzero == 0
}

// decodeElement accepts the fixed-width x||y encoding used by SAE commit
// frames as well as SEC1 uncompressed and compressed encodings.
func decodeElement(spec groupSpec, encoded []byte) (*big.Int, *big.Int, error) {
	if len(encoded) == 0 {
		return nil, nil, ErrInvalidElement
	}
	if (len(encoded) == 1 && encoded[0] == 0) ||
		(len(encoded) == 2*spec.size && isZero(encoded)) {
		return nil, nil, ErrPointAtInfinity
	}

	var sec1 []byte
	switch {
	case len(encoded) == 2*spec.size:
		sec1 = make([]byte, 1+len(encoded))
		sec1[0] = 4
		copy(sec1[1:], encoded)
	case len(encoded) == 1+2*spec.size && encoded[0] == 4:
		sec1 = encoded
	case len(encoded) == 1+spec.size && (encoded[0] == 2 || encoded[0] == 3):
		sec1 = encoded
	default:
		return nil, nil, ErrInvalidElement
	}

	x, y := elliptic.Unmarshal(spec.curve, sec1)
	if x == nil || y == nil || !spec.curve.IsOnCurve(x, y) {
		return nil, nil, ErrInvalidElement
	}
	if x.Sign() == 0 && y.Sign() == 0 {
		return nil, nil, ErrPointAtInfinity
	}
	return x, y, nil
}

func validateCommit(spec groupSpec, c Commit) error {
	if err := validateScalar(spec, c.Scalar); err != nil {
		return err
	}
	x, y, err := decodeElement(spec, c.Element)
	if err != nil {
		return err
	}
	if len(c.LocalElement) == 0 {
		return nil
	}
	localX, localY, err := decodeElement(spec, c.LocalElement)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLocalElement, err)
	}
	if x.Cmp(localX) == 0 && y.Cmp(localY) == 0 {
		return ErrReflectedCommit
	}
	return nil
}

// ValidateCommit checks the peer scalar and element before SAE curve
// operations. The scalar must be in [2, r-1]; the element must be a
// non-infinity point on the selected curve and must differ from the AP's own
// element.
func ValidateCommit(c Commit) error {
	spec, err := lookupGroup(c.Group)
	if err != nil {
		return err
	}
	return validateCommit(spec, c)
}
