package ipp

import (
	"bytes"
	"fmt"
	"math/big"
)

// Point is always the adapter's canonical point encoding inside this package.
type Point []byte

// Adapter is the trusted boundary supplying group parameters and arithmetic.
// Implementations must use a prime-order group and one unique encoding per point.
type Adapter interface {
	Order() *big.Int
	Identity() Point
	ParsePoint(raw []byte) (Point, error)
	AddPoints(a, b Point) (Point, error)
	MultiplyPoint(scalar *big.Int, p Point) (Point, error)
}

type Generators struct {
	G []Point
	H []Point
	U Point
}

type Statement struct {
	Context     []byte
	Commitment  Point
	Generators  Generators
	DomainLabel []byte
}

type Witness struct {
	A []*big.Int
	B []*big.Int
}

func ValidN(n int) bool {
	return n >= 1 && n <= 1024 && n&(n-1) == 0
}

func roundsForN(n int) (int, error) {
	if !ValidN(n) {
		return 0, fmt.Errorf("%w: got %d", ErrN, n)
	}
	rounds := 0
	for m := n; m > 1; m >>= 1 {
		rounds++
	}
	return rounds, nil
}

func clonePoint(p Point) Point {
	if p == nil {
		return nil
	}
	out := make(Point, len(p))
	copy(out, p)
	return out
}

func canonicalPoint(ad Adapter, raw []byte) (Point, error) {
	if ad == nil {
		return nil, fmt.Errorf("%w: nil adapter", ErrAdapter)
	}
	parsed, err := ad.ParsePoint(clonePoint(raw))
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("%w: adapter returned empty encoding", ErrInvalidPoint)
	}
	if !bytes.Equal(parsed, raw) {
		return nil, fmt.Errorf("%w: adapter canonicalized alternate encoding", ErrNonCanonicalPoint)
	}
	checked, err := ad.ParsePoint(parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical encoding rejected: %v", ErrNonCanonicalPoint, err)
	}
	if !bytes.Equal(checked, parsed) {
		return nil, fmt.Errorf("%w: unstable canonical encoding", ErrNonCanonicalPoint)
	}
	return clonePoint(parsed), nil
}

func identityPoint(ad Adapter) (Point, error) {
	id := ad.Identity()
	if len(id) == 0 {
		return nil, fmt.Errorf("%w: empty identity encoding", ErrAdapter)
	}
	canonical, err := canonicalPoint(ad, id)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid adapter identity: %v", ErrAdapter, err)
	}
	return canonical, nil
}

func requireNonIdentity(p, identity Point, name string) error {
	if bytes.Equal(p, identity) {
		return fmt.Errorf("%w: %s", ErrIdentityGenerator, name)
	}
	return nil
}

type preparedParams struct {
	q      *big.Int
	n      int
	rounds int
	g      []Point
	h      []Point
	u      Point
	p      Point
	id     Point
}

func prepareParams(ad Adapter, context []byte, gens Generators, commitment Point, requireHostGenerators bool) (*preparedParams, error) {
	if ad == nil {
		return nil, fmt.Errorf("%w: nil", ErrAdapter)
	}
	q := ad.Order()
	if err := checkOrder(q); err != nil {
		return nil, err
	}
	n := len(gens.G)
	if !ValidN(n) {
		return nil, fmt.Errorf("%w: %d", ErrN, n)
	}
	if len(gens.H) != n {
		return nil, fmt.Errorf("%w: G=%d H=%d", ErrGeneratorLength, n, len(gens.H))
	}
	if len(gens.U) == 0 {
		return nil, fmt.Errorf("%w", ErrMissingU)
	}
	rounds, err := roundsForN(n)
	if err != nil {
		return nil, err
	}
	id, err := identityPoint(ad)
	if err != nil {
		return nil, err
	}

	prepared := &preparedParams{q: q, n: n, rounds: rounds, id: id}
	prepared.g = make([]Point, n)
	prepared.h = make([]Point, n)
	for i := 0; i < n; i++ {
		g, err := canonicalPoint(ad, gens.G[i])
		if err != nil {
			return nil, fmt.Errorf("G[%d]: %w", i, err)
		}
		h, err := canonicalPoint(ad, gens.H[i])
		if err != nil {
			return nil, fmt.Errorf("H[%d]: %w", i, err)
		}
		if requireHostGenerators {
			if err := requireNonIdentity(g, id, fmt.Sprintf("G[%d]", i)); err != nil {
				return nil, err
			}
			if err := requireNonIdentity(h, id, fmt.Sprintf("H[%d]", i)); err != nil {
				return nil, err
			}
		}
		prepared.g[i] = g
		prepared.h[i] = h
	}
	u, err := canonicalPoint(ad, gens.U)
	if err != nil {
		return nil, fmt.Errorf("U: %w", err)
	}
	if requireHostGenerators {
		if err := requireNonIdentity(u, id, "U"); err != nil {
			return nil, err
		}
	}
	prepared.u = u

	p, err := canonicalPoint(ad, commitment)
	if err != nil {
		return nil, fmt.Errorf("P: %w", err)
	}
	prepared.p = p
	_ = context
	return prepared, nil
}
