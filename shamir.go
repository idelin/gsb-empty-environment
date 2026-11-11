package sharevault

import (
	"encoding/binary"
	"io"
)

// Share is a single Shamir share over GF(257). X is the non-zero
// participant coordinate, Y the share value, and Generation the
// refresh generation the share belongs to.
type Share struct {
	X          uint16
	Y          uint16
	Generation uint64
}

// Delta is a point on a zero-constant refresh polynomial, addressed to
// the participant owning coordinate X.
type Delta struct {
	X uint16
	Y uint16
}

func validateXS(xs []uint16) error {
	if len(xs) == 0 {
		return ErrNoShares
	}
	seen := make(map[uint16]struct{}, len(xs))
	for _, x := range xs {
		if x == 0 || !feValid(x) {
			return ErrInvalidCoordinate
		}
		if _, dup := seen[x]; dup {
			return ErrDuplicateX
		}
		seen[x] = struct{}{}
	}
	return nil
}

func evalPoly(coeffs []uint16, x uint16) uint16 {
	acc := uint16(0)
	for i := len(coeffs) - 1; i >= 0; i-- {
		acc = feAdd(feMul(acc, x), coeffs[i])
	}
	return acc
}

// interpolateAt evaluates the unique polynomial of degree < len(points)
// passing through points at the coordinate at.
func interpolateAt(points []Delta, at uint16) uint16 {
	acc := uint16(0)
	for i, p := range points {
		num, den := uint16(1), uint16(1)
		for j, q := range points {
			if i == j {
				continue
			}
			num = feMul(num, feSub(at, q.X))
			den = feMul(den, feSub(p.X, q.X))
		}
		inv, ok := feInv(den)
		if !ok {
			// Unreachable: coordinates are validated distinct.
			continue
		}
		acc = feAdd(acc, feMul(p.Y, feMul(num, inv)))
	}
	return acc
}

func randomFieldElement(rng io.Reader) (uint16, error) {
	var buf [2]byte
	for {
		if _, err := io.ReadFull(rng, buf[:]); err != nil {
			return 0, err
		}
		if v := binary.BigEndian.Uint16(buf[:]); feValid(v) {
			return v, nil
		}
	}
}

// Split divides secret into one share per coordinate in xs using a
// random polynomial of degree threshold-1 whose constant term is the
// secret. Coordinates must be non-zero, in field range, and distinct.
func Split(rng io.Reader, secret uint16, threshold int, generation uint64, xs []uint16) ([]Share, error) {
	if !feValid(secret) {
		return nil, ErrInvalidSecret
	}
	if threshold < 1 || threshold > len(xs) {
		return nil, ErrInvalidThreshold
	}
	if err := validateXS(xs); err != nil {
		return nil, err
	}
	coeffs := make([]uint16, threshold)
	coeffs[0] = secret
	for i := 1; i < threshold; i++ {
		c, err := randomFieldElement(rng)
		if err != nil {
			return nil, err
		}
		coeffs[i] = c
	}
	shares := make([]Share, len(xs))
	for i, x := range xs {
		shares[i] = Share{X: x, Y: evalPoly(coeffs, x), Generation: generation}
	}
	return shares, nil
}

// Reconstruct recovers the secret from any set of shares of a single
// generation. It rejects duplicate x coordinates and shares spliced
// from different generations.
func Reconstruct(shares []Share) (uint16, error) {
	if len(shares) == 0 {
		return 0, ErrNoShares
	}
	generation := shares[0].Generation
	seen := make(map[uint16]struct{}, len(shares))
	points := make([]Delta, len(shares))
	for i, sh := range shares {
		if sh.Generation != generation {
			return 0, ErrGenerationMismatch
		}
		if sh.X == 0 || !feValid(sh.X) {
			return 0, ErrInvalidCoordinate
		}
		if !feValid(sh.Y) {
			return 0, ErrInvalidShareValue
		}
		if _, dup := seen[sh.X]; dup {
			return 0, ErrDuplicateX
		}
		seen[sh.X] = struct{}{}
		points[i] = Delta{X: sh.X, Y: sh.Y}
	}
	return interpolateAt(points, 0), nil
}
