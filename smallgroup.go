package ipp

import (
	"fmt"
	"math/big"
)

type SmallGroup struct {
	q     *big.Int
	qInt  int
	order int64
}

func NewSmallGroup() *SmallGroup {
	g, err := NewSmallGroupWithOrder(11)
	if err != nil {
		panic(err)
	}
	return g
}

func NewSmallGroupWithOrder(order int64) (*SmallGroup, error) {
	if order < 3 || order > 251 {
		return nil, fmt.Errorf("%w: small reference order must be a prime in [3,251]", ErrAdapter)
	}
	q := big.NewInt(order)
	if !q.ProbablyPrime(32) {
		return nil, fmt.Errorf("%w: %d is not prime", ErrAdapter, order)
	}
	return &SmallGroup{q: q, qInt: int(order), order: order}, nil
}

func (g *SmallGroup) Order() *big.Int {
	return new(big.Int).Set(g.q)
}

func (g *SmallGroup) Identity() Point {
	return Point{0}
}

func (g *SmallGroup) ParsePoint(raw []byte) (Point, error) {
	if len(raw) != 1 {
		return nil, fmt.Errorf("%w: small-group point must be one byte", ErrInvalidPoint)
	}
	if int(raw[0]) >= g.qInt {
		return nil, fmt.Errorf("%w: point %d >= q", ErrNonCanonicalPoint, raw[0])
	}
	return Point{raw[0]}, nil
}

func (g *SmallGroup) AddPoints(a, b Point) (Point, error) {
	if len(a) != 1 || len(b) != 1 || int(a[0]) >= g.qInt || int(b[0]) >= g.qInt {
		return nil, fmt.Errorf("%w: small-group point", ErrInvalidPoint)
	}
	return Point{byte((int(a[0]) + int(b[0])) % g.qInt)}, nil
}

func (g *SmallGroup) MultiplyPoint(scalar *big.Int, p Point) (Point, error) {
	if err := checkScalar(scalar, g.q); err != nil {
		return nil, err
	}
	if len(p) != 1 || int(p[0]) >= g.qInt {
		return nil, fmt.Errorf("%w: small-group point", ErrInvalidPoint)
	}
	value := new(big.Int).Mul(scalar, big.NewInt(int64(p[0])))
	value.Mod(value, g.q)
	return Point{byte(value.Int64())}, nil
}

func SmallGenerators(q *SmallGroup, n int) Generators {
	gens := Generators{G: make([]Point, n), H: make([]Point, n)}
	nonIdentityValues := q.qInt - 1
	for i := 0; i < n; i++ {
		gens.G[i] = Point{byte(1 + i%nonIdentityValues)}
		hValue := 1 + (i*3+2)%nonIdentityValues
		gens.H[i] = Point{byte(hValue)}
	}
	gens.U = Point{byte(q.qInt - 1)}
	return gens
}
