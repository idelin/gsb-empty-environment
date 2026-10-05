package main

import "math/big"

type testRand struct{ state uint64 }

func newRand(seed uint64) *testRand {
	if seed == 0 {
		seed = 1
	}
	return &testRand{state: seed}
}

func (r *testRand) next() uint64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return r.state
}

func (r *testRand) intn(n int) int { return int(r.next() % uint64(n)) }

// randomSystem builds a random mixed-kind constraint system with small
// integer coefficients.
func randomSystem(r *testRand, numVars, numCons int) []*Constraint {
	sys := make([]*Constraint, 0, numCons)
	for i := 0; i < numCons; i++ {
		var kind Kind
		switch x := r.intn(10); {
		case x < 6:
			kind = Le
		case x < 8:
			kind = Lt
		default:
			kind = Eq
		}
		c := newConstraint(numVars, kind, []int{i})
		for j := 0; j < numVars; j++ {
			c.Coeff[j] = big.NewRat(int64(r.intn(7)-3), 1)
		}
		if c.isZeroRow() {
			c.Coeff[r.intn(numVars)] = big.NewRat(1, 1)
		}
		c.Const = big.NewRat(int64(r.intn(11)-5), 1)
		sys = append(sys, c)
	}
	return sys
}
