// Package gcrt solves systems of simultaneous congruences whose moduli are
// not necessarily coprime (generalized CRT). It supports negative residues
// and big integers, transactional updates with historical snapshots, and
// verified checkpoints.
package gcrt

import (
	"errors"
	"fmt"
	"math/big"
)

// Constraint is a single congruence x ≡ Res (mod Mod).
type Constraint struct {
	ID  int64    // identity of the original constraint
	Mod *big.Int // modulus, must be > 0
	Res *big.Int // residue, any sign (normalized internally)
}

func (c Constraint) validate() error {
	if c.Mod == nil || c.Mod.Sign() <= 0 {
		return fmt.Errorf("gcrt: constraint %d has non-positive modulus", c.ID)
	}
	if c.Res == nil {
		return fmt.Errorf("gcrt: constraint %d has nil residue", c.ID)
	}
	return nil
}

// normalized returns the residue reduced into [0, Mod).
func (c Constraint) normalized() (r, m *big.Int) {
	m = new(big.Int).Set(c.Mod)
	r = new(big.Int).Mod(c.Res, m)
	return r, m
}

// Solution describes the solution set of a system of congruences: every integer
// x with x ≡ Res (mod Period). Res is the minimal non-negative
// representative. The empty system yields Res = 0, Period = 1, i.e. all
// integers. When OK is false the system is infeasible and Conflict holds the
// IDs of the earliest contradictory pair of original constraints (earliest
// by insertion order).
type Solution struct {
	OK       bool
	Res      *big.Int
	Period   *big.Int
	Conflict [2]int64
}

// All returns the solution set containing every integer.
func All() Solution {
	return Solution{OK: true, Res: big.NewInt(0), Period: big.NewInt(1)}
}

// Contains reports whether x belongs to the solution set.
func (s Solution) Contains(x *big.Int) bool {
	if !s.OK {
		return false
	}
	r := new(big.Int).Mod(x, s.Period)
	return r.Cmp(s.Res) == 0
}

func (s Solution) String() string {
	if !s.OK {
		return fmt.Sprintf("infeasible (constraints %d and %d conflict)", s.Conflict[0], s.Conflict[1])
	}
	return fmt.Sprintf("x ≡ %s (mod %s)", s.Res, s.Period)
}

// merge combines x ≡ r1 (mod m1) with x ≡ r2 (mod m2). It is solvable iff
// r1 ≡ r2 (mod gcd(m1, m2)); the result is defined modulo lcm(m1, m2).
func merge(r1, m1, r2, m2 *big.Int) (r, m *big.Int, ok bool) {
	// g = gcd(m1, m2) with u*m1 + v*m2 = g.
	u := new(big.Int)
	g := new(big.Int).GCD(u, nil, m1, m2)
	diff := new(big.Int).Sub(r2, r1)
	if new(big.Int).Mod(diff, g).Sign() != 0 {
		return nil, nil, false
	}
	// t ≡ ((r2-r1)/g) * u (mod m2/g), then x = r1 + m1*t.
	m2g := new(big.Int).Quo(m2, g)
	t := new(big.Int).Quo(diff, g)
	t.Mul(t, u)
	t.Mod(t, m2g)
	period := new(big.Int).Mul(m1, m2g) // lcm(m1, m2)
	r = new(big.Int).Mul(m1, t)
	r.Add(r, r1)
	r.Mod(r, period)
	return r, period, true
}

// pairwiseConsistent reports whether two constraints admit a common solution.
func pairwiseConsistent(c1, c2 Constraint) bool {
	r1, m1 := c1.normalized()
	r2, m2 := c2.normalized()
	g := new(big.Int).GCD(nil, nil, m1, m2)
	d := new(big.Int).Sub(r1, r2)
	return new(big.Int).Mod(d, g).Sign() == 0
}

// SolveConstraints fully merges the given constraints in order and returns
// the combined solution. On infeasibility it reports the earliest
// contradictory pair of original constraints: the smallest j (in slice
// order) involved in any conflict, paired with the smallest i < j that
// conflicts with it. A system of congruences is solvable iff it is pairwise
// solvable, so such a pair always exists.
func SolveConstraints(cs []Constraint) Solution {
	acc := All()
	for j, c := range cs {
		if err := c.validate(); err != nil {
			panic(err)
		}
		r, m := c.normalized()
		nr, nm, ok := merge(acc.Res, acc.Period, r, m)
		if !ok {
			for i := 0; i < j; i++ {
				if !pairwiseConsistent(cs[i], c) {
					return Solution{OK: false, Conflict: [2]int64{cs[i].ID, c.ID}}
				}
			}
			panic(errors.New("gcrt: merge failed without a pairwise conflict"))
		}
		acc = Solution{OK: true, Res: nr, Period: nm}
	}
	return acc
}
