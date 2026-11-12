package netsec

import (
	"crypto/elliptic"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"math/big"
)

// GroupID is the IEEE 802.11 SAE group identifier.
type GroupID uint16

const (
	// Group19 is the NIST P-256 SAE group.
	Group19 GroupID = 19
	// Group20 is the NIST P-384 SAE group.
	Group20 GroupID = 20
	// Group21 is the NIST P-521 SAE group.
	Group21 GroupID = 21

	GroupP256 = Group19
	GroupP384 = Group20
	GroupP521 = Group21
)

// Group describes a supported SAE elliptic-curve group.
type Group struct {
	id         GroupID
	name       string
	curve      elliptic.Curve
	scalarSize int
	coordSize  int
}

var groups = map[GroupID]*Group{
	Group19: newGroup(Group19, "P-256", elliptic.P256()),
	Group20: newGroup(Group20, "P-384", elliptic.P384()),
	Group21: newGroup(Group21, "P-521", elliptic.P521()),
}

func newGroup(id GroupID, name string, curve elliptic.Curve) *Group {
	bitSize := curve.Params().BitSize
	return &Group{
		id:         id,
		name:       name,
		curve:      curve,
		scalarSize: (bitSize + 7) / 8,
		coordSize:  (bitSize + 7) / 8,
	}
}

// LookupGroup returns the implementation for a SAE group.
func LookupGroup(id GroupID) (*Group, bool) {
	g, ok := groups[id]
	return g, ok
}

// SupportedGroups returns the SAE group identifiers implemented by this
// package.
func SupportedGroups() []GroupID {
	return []GroupID{Group19, Group20, Group21}
}

// ID returns the SAE group identifier.
func (g *Group) ID() GroupID { return g.id }

// Name returns the conventional curve name.
func (g *Group) Name() string { return g.name }

// Order returns a copy of the group order r.
func (g *Group) Order() *big.Int {
	return new(big.Int).Set(g.curve.Params().N)
}

// ScalarSize returns the fixed scalar field width in bytes.
func (g *Group) ScalarSize() int { return g.scalarSize }

// CoordinateSize returns the width of one affine coordinate in bytes.
func (g *Group) CoordinateSize() int { return g.coordSize }

// ElementSize returns the size of the raw x||y element representation used by
// SAE.
func (g *Group) ElementSize() int { return 2 * g.coordSize }

// ValidateScalar checks that scalar is a canonical fixed-width encoding in
// [2, r-1].
func (g *Group) ValidateScalar(scalar []byte) error {
	if len(scalar) != g.scalarSize {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrScalarEncoding, len(scalar), g.scalarSize)
	}

	v := new(big.Int).SetBytes(scalar)
	n := g.curve.Params().N
	max := new(big.Int).Sub(n, big.NewInt(1))
	if v.Cmp(big.NewInt(2)) < 0 || v.Cmp(max) > 0 {
		return ErrScalarRange
	}
	return nil
}

// ScalarInRange reports whether scalar passes ValidateScalar.
func (g *Group) ScalarInRange(scalar []byte) bool {
	return g.ValidateScalar(scalar) == nil
}

// MarshalScalar encodes v in the group's fixed-width scalar representation.
// It does not impose the SAE [2, r-1] range.
func (g *Group) MarshalScalar(v *big.Int) ([]byte, error) {
	if v == nil || v.Sign() < 0 || v.Cmp(g.curve.Params().N) >= 0 {
		return nil, ErrScalarRange
	}
	out := make([]byte, g.scalarSize)
	v.FillBytes(out)
	return out, nil
}

// ValidateElement checks that element is a finite point on the curve.
func (g *Group) ValidateElement(element []byte) error {
	_, err := g.CanonicalElement(element)
	return err
}

// CanonicalElement validates element and returns its raw x||y encoding.
// Uncompressed SEC1 encodings (0x04||x||y), compressed SEC1 encodings, and
// the raw x||y representation used by SAE are accepted.
func (g *Group) CanonicalElement(element []byte) ([]byte, error) {
	x, y, err := g.decodeElement(element)
	if err != nil {
		return nil, err
	}
	return g.encodePoint(x, y), nil
}

// EqualElements reports whether two encodings describe the same finite curve
// point, independently of their wire representation.
func (g *Group) EqualElements(a, b []byte) (bool, error) {
	ca, err := g.CanonicalElement(a)
	if err != nil {
		return false, err
	}
	cb, err := g.CanonicalElement(b)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(ca, cb) == 1, nil
}

// ValidateCommit validates the scalar and element. The element is also
// compared with ownElement when ownElement is non-empty.
func (g *Group) ValidateCommit(scalar, element, ownElement []byte) error {
	if err := g.ValidateScalar(scalar); err != nil {
		return err
	}
	elementCanonical, err := g.CanonicalElement(element)
	if err != nil {
		return err
	}
	if len(ownElement) == 0 {
		return nil
	}
	ownCanonical, err := g.CanonicalElement(ownElement)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOwnElement, err)
	}
	if subtle.ConstantTimeCompare(elementCanonical, ownCanonical) == 1 {
		return ErrReflection
	}
	return nil
}

// ValidateCommit validates a Commit against a SAE group. ownElement may be
// nil when reflection checking is not needed.
func ValidateCommit(group GroupID, scalar, element, ownElement []byte) error {
	g, ok := LookupGroup(group)
	if !ok {
		return ErrUnsupportedGroup
	}
	return g.ValidateCommit(scalar, element, ownElement)
}

// ScalarBaseMult computes scalar*G and returns the raw x||y element.
func (g *Group) ScalarBaseMult(scalar []byte) ([]byte, error) {
	if err := g.ValidateScalar(scalar); err != nil {
		return nil, err
	}
	x, y := g.curve.ScalarBaseMult(scalar)
	if x == nil || y == nil {
		return nil, ErrElementInfinity
	}
	return g.encodePoint(x, y), nil
}

// ScalarMult computes scalar*element. It is useful for tests and for deriving
// an AP element from a stored private scalar.
func (g *Group) ScalarMult(element, scalar []byte) ([]byte, error) {
	if err := g.ValidateScalar(scalar); err != nil {
		return nil, err
	}
	x, y, err := g.decodeElement(element)
	if err != nil {
		return nil, err
	}
	x2, y2 := g.curve.ScalarMult(x, y, scalar)
	if x2 == nil || y2 == nil {
		return nil, ErrElementInfinity
	}
	return g.encodePoint(x2, y2), nil
}

func (g *Group) decodeElement(element []byte) (*big.Int, *big.Int, error) {
	coordSize := g.coordSize
	params := g.curve.Params()
	p := params.P

	switch {
	case len(element) == 1 && element[0] == 0:
		return nil, nil, ErrElementInfinity

	case len(element) == 1+2*coordSize && element[0] == 4:
		body := element[1:]
		if allZero(body) {
			return nil, nil, ErrElementInfinity
		}
		x, y := elliptic.Unmarshal(g.curve, element)
		if x == nil || y == nil {
			return nil, nil, ErrElementNotOnCurve
		}
		return x, y, nil

	case len(element) == 1+coordSize && (element[0] == 2 || element[0] == 3):
		x, y := elliptic.UnmarshalCompressed(g.curve, element)
		if x == nil || y == nil {
			return nil, nil, ErrElementNotOnCurve
		}
		return x, y, nil

	case len(element) == 2*coordSize:
		xBytes := element[:coordSize]
		yBytes := element[coordSize:]
		if allZero(element) {
			return nil, nil, ErrElementInfinity
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if x.Cmp(p) >= 0 || y.Cmp(p) >= 0 {
			return nil, nil, ErrElementNotOnCurve
		}
		if !g.curve.IsOnCurve(x, y) {
			return nil, nil, ErrElementNotOnCurve
		}
		return x, y, nil

	default:
		return nil, nil, fmt.Errorf("%w: got %d bytes", ErrElementEncoding, len(element))
	}
}

func (g *Group) encodePoint(x, y *big.Int) []byte {
	out := make([]byte, 2*g.coordSize)
	x.FillBytes(out[:g.coordSize])
	y.FillBytes(out[g.coordSize:])
	return out
}

func allZero(b []byte) bool {
	var v byte
	for _, c := range b {
		v |= c
	}
	return v == 0
}

func groupBytes(id GroupID) [2]byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(id))
	return b
}
