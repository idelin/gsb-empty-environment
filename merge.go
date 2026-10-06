// Package gcrt solves systems of simultaneous congruences whose moduli are
// not necessarily coprime (generalized Chinese remainder theorem).
//
// Constraints are managed by a Store through transactions; every commit
// produces an immutable snapshot that stays queryable. Checkpoints persist
// the original constraint identities together with the incremental solver
// state, and loading re-verifies that state with a full re-merge.
package gcrt

import "math/big"

// Congruence is the residue class x ≡ Res (mod Mod), kept normalized so
// that Mod > 0 and 0 <= Res < Mod.
type Congruence struct {
	Res *big.Int
	Mod *big.Int
}

// Normalize reduces res modulo mod. mod must be positive; res may be
// negative or arbitrarily large.
func Normalize(res, mod *big.Int) Congruence {
	m := new(big.Int).Set(mod)
	r := new(big.Int).Mod(res, m)
	return Congruence{Res: r, Mod: m}
}

// Merge intersects two residue classes. It reports ok=false exactly when
// the two congruences conflict, i.e. a.Res ≢ b.Res (mod gcd(a.Mod, b.Mod)).
// Otherwise the result is the single residue class modulo lcm(a.Mod, b.Mod)
// that equals their intersection.
func Merge(a, b Congruence) (merged Congruence, ok bool) {
	g := new(big.Int).GCD(nil, nil, a.Mod, b.Mod)
	diff := new(big.Int).Sub(b.Res, a.Res)
	if new(big.Int).Mod(diff, g).Sign() != 0 {
		return Congruence{}, false
	}
	q1 := new(big.Int).Quo(a.Mod, g)
	q2 := new(big.Int).Quo(b.Mod, g)
	lcm := new(big.Int).Mul(q1, b.Mod)
	t := new(big.Int)
	if q2.Cmp(big.NewInt(1)) != 0 {
		// gcd(q1, q2) == 1, so the inverse exists.
		inv := new(big.Int).ModInverse(q1, q2)
		t.Quo(diff, g)
		t.Mul(t, inv)
		t.Mod(t, q2)
	}
	x := new(big.Int).Mul(a.Mod, t)
	x.Add(x, a.Res)
	x.Mod(x, lcm)
	return Congruence{Res: x, Mod: lcm}, true
}
