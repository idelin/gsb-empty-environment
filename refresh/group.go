package refresh

import (
	"encoding/binary"
	"errors"
	"math/big"
)

// Point is an element of the group the commitments live in. Implementations
// are supplied by the key host, which keeps the actual group choice outside
// this package.
type Point interface {
	// Equals reports group equality.
	Equals(other Point) bool
	// Marshal produces a stable canonical encoding used in digests and audits.
	Marshal() ([]byte, error)
}

// Group abstracts the cyclic group used for coefficient-by-coefficient
// commitments. Only the operations needed by the refresh acceptance protocol
// are exposed; scalar arithmetic stays in the injected prime field.
type Group interface {
	// Generator returns the fixed generator g.
	Generator() Point
	// ScalarBaseMul returns g*s.
	ScalarBaseMul(s *big.Int) Point
	// ScalarMul returns p*s.
	ScalarMul(p Point, s *big.Int) Point
	// Add returns the group sum of its arguments.
	Add(a, b Point) Point
	// Neg returns the group inverse of p.
	Neg(p Point) Point
	// Identity returns the group identity element.
	Identity() Point
	// ParsePoint decodes an encoding previously produced by Point.Marshal.
	ParsePoint(b []byte) (Point, error)
}

// Commitment is the coefficient-by-coefficient commitment C = [C_0 .. C_{d}],
// C_l = g*c_l, to a refresh polynomial. A refresh polynomial has zero constant
// term, so a well-formed commitment has C_0 == identity.
type Commitment struct {
	Round    uint64
	SenderID string
	Terms    []Point
}

// MarshalCommitment renders a commitment into its canonical byte form. The
// encoding is deterministic and is reused both inside transcript digests and
// inside audit records; it contains group points only, never scalars.
func MarshalCommitment(c Commitment) ([]byte, error) {
	var out []byte
	out = appendUvarint(out, c.Round)
	out = append(out, []byte(c.SenderID)...)
	out = append(out, 0)
	out = appendUvarint(out, uint64(len(c.Terms)))
	for _, t := range c.Terms {
		if t == nil {
			return nil, errors.New("refresh: commitment contains a nil term")
		}
		b, err := t.Marshal()
		if err != nil {
			return nil, err
		}
		out = appendUvarint(out, uint64(len(b)))
		out = append(out, b...)
	}
	return out, nil
}

// EvalCommitment evaluates a coefficient commitment at coordinate x:
// E(x) = sum_l C_l * x^l. The receiver's delivered increment is accepted only
// when g*delta equals this value — a valid signature alone is never enough.
func EvalCommitment(g Group, c Commitment, x *big.Int) (Point, error) {
	if len(c.Terms) == 0 {
		return nil, errors.New("refresh: empty commitment")
	}
	acc := g.Identity()
	power := big.NewInt(1)
	for _, term := range c.Terms {
		acc = g.Add(acc, g.ScalarMul(term, power))
		power = new(big.Int).Mul(power, x)
	}
	return acc, nil
}

// SumCommitments adds commitments term by term. Terms past the longest vector
// are treated as identity, matching polynomial addition. The result keeps the
// round/identity of the first argument; callers that need labels set them.
func SumCommitments(g Group, rounds []Commitment) ([]Point, error) {
	if len(rounds) == 0 {
		return nil, errors.New("refresh: cannot sum an empty commitment set")
	}
	maxLen := 0
	for _, c := range rounds {
		if len(c.Terms) > maxLen {
			maxLen = len(c.Terms)
		}
	}
	out := make([]Point, maxLen)
	for i := range out {
		out[i] = g.Identity()
	}
	for _, c := range rounds {
		for i, t := range c.Terms {
			out[i] = g.Add(out[i], t)
		}
	}
	return out, nil
}

// ZeroConstant reports whether the commitment's constant term is the group
// identity, i.e. the refresh polynomial has zero constant term as required.
func ZeroConstant(g Group, c Commitment) bool {
	if len(c.Terms) == 0 || c.Terms[0] == nil {
		return false
	}
	return c.Terms[0].Equals(g.Identity())
}

func appendUvarint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(dst, v)
}
