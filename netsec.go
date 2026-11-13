package netsec

import (
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrInvalidField means that p is outside the supported range or is not prime.
	ErrInvalidField = errors.New("netsec: invalid prime field")
	// ErrInvalidParameter means that k, t, or the share count is invalid.
	ErrInvalidParameter = errors.New("netsec: invalid parameter")
	// ErrInvalidShare means that a coordinate is outside F_p or x is zero.
	ErrInvalidShare = errors.New("netsec: invalid share")
	// ErrDuplicateX means that two shares use the same x coordinate.
	ErrDuplicateX = errors.New("netsec: duplicate x coordinate")
	// ErrDistance means that n >= k+2t is violated.
	ErrDistance = errors.New("netsec: shares do not satisfy n >= k+2t")
	// ErrNoConsistentPolynomial means that no degree-bounded polynomial is
	// consistent with at least n-t received shares.
	ErrNoConsistentPolynomial = errors.New("netsec: no sufficiently consistent degree-bounded polynomial")
)

// Share is one received point (X, Y) in F_p. ID is an optional caller-supplied
// label used in Result.InconsistentIDs. Empty IDs fall back to the decimal
// representation of X.
type Share struct {
	ID string
	X  int
	Y  int
}

// Shard is an alias for Share.
type Shard = Share

// NewShare constructs a share with a caller-supplied identifier.
func NewShare(id string, x, y int) Share {
	return Share{ID: id, X: x, Y: y}
}

// Result is the unique polynomial guaranteed by the decoding distance.
//
// Polynomial contains exactly k coefficients in ascending order, so
// Polynomial[0] is f(0). InconsistentIndices contains zero-based positions in
// the input slice. InconsistentIDs contains the corresponding share labels.
// These fields describe disagreement with the recovered polynomial among the
// received shares; they do not identify the original attacker or prove which
// submissions were malicious.
type Result struct {
	Polynomial          []int
	Secret              int
	ConsistentCount     int
	InconsistentIndices []int
	InconsistentIDs     []string
	InconsistentX       []int
}

// Recover reconstructs f and f(0) from shares over F_p.
//
// The requirements are: p is prime and p <= 65521; 1 <= k <= 16;
// k <= len(shares) <= 40; t >= 0; and len(shares) >= k+2t. Every x must be
// distinct, nonzero, and in F_p; every y must be in F_p. At most t shares may
// depart from the codeword, and fewer errors than t are accepted.
//
// Recover fails with ErrNoConsistentPolynomial when no degree-(k-1) polynomial
// agrees with at least n-t shares. It never substitutes interpolation of an
// arbitrary k-subset for that global consistency test.
func Recover(shares []Share, p, k, t int) (*Result, error) {
	field, err := validateRequest(shares, p, k, t)
	if err != nil {
		return nil, err
	}

	polynomial, ok := decodePolynomial(field, shares, k, t)
	if !ok {
		return nil, ErrNoConsistentPolynomial
	}

	n := len(shares)
	result := &Result{
		Polynomial: make([]int, k),
		Secret:     int(polynomial[0]),
	}
	for i, coefficient := range polynomial {
		result.Polynomial[i] = int(coefficient)
	}

	for i, share := range shares {
		if evalPolynomial(polynomial, int64(share.X), field.p) != int64(share.Y) {
			result.InconsistentIndices = append(result.InconsistentIndices, i)
			result.InconsistentIDs = append(result.InconsistentIDs, shareLabel(share))
			result.InconsistentX = append(result.InconsistentX, share.X)
			continue
		}
		result.ConsistentCount++
	}
	if result.ConsistentCount < n-t {
		return nil, ErrNoConsistentPolynomial
	}
	return result, nil
}

// Decode is an alias for Recover.
func Decode(shares []Share, p, k, t int) (*Result, error) {
	return Recover(shares, p, k, t)
}

func validateRequest(shares []Share, p, k, t int) (primeField, error) {
	field := primeField{p: int64(p)}
	if p < 2 || p > 65521 || !isPrime(p) {
		return field, fmt.Errorf("%w: p=%d", ErrInvalidField, p)
	}
	if k < 1 || k > 16 {
		return field, fmt.Errorf("%w: k=%d", ErrInvalidParameter, k)
	}
	n := len(shares)
	if n < k || n > 40 {
		return field, fmt.Errorf("%w: n=%d, k=%d", ErrInvalidParameter, n, k)
	}
	if t < 0 {
		return field, fmt.Errorf("%w: t=%d", ErrInvalidParameter, t)
	}
	if t > (n-k)/2 {
		return field, fmt.Errorf("%w: n=%d, k=%d, t=%d", ErrDistance, n, k, t)
	}

	seen := make(map[int]struct{}, n)
	for i, share := range shares {
		if share.X <= 0 || share.X >= p {
			return field, fmt.Errorf("%w: share %d has x=%d", ErrInvalidShare, i, share.X)
		}
		if share.Y < 0 || share.Y >= p {
			return field, fmt.Errorf("%w: share %d has y=%d", ErrInvalidShare, i, share.Y)
		}
		if _, exists := seen[share.X]; exists {
			return field, fmt.Errorf("%w: x=%d", ErrDuplicateX, share.X)
		}
		seen[share.X] = struct{}{}
	}
	return field, nil
}

func shareLabel(share Share) string {
	if share.ID != "" {
		return share.ID
	}
	return strconv.Itoa(share.X)
}
