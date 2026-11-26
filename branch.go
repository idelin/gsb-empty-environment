package hvsched

import (
	"math/big"
)

type box struct {
	lo []int64
	hi []int64
}

// branch is one non-max branch:
//
//	c + sum_i coef[i] / w_i.
//
// State branches have nonnegative coefficients. Comparison differences may
// have signed coefficients.
type branch struct {
	c    *big.Rat
	coef []*big.Rat
}

type branchSet []*branch

func zeroBranch(nVar int) *branch {
	coef := make([]*big.Rat, nVar)
	for i := range coef {
		coef[i] = new(big.Rat)
	}
	return &branch{c: new(big.Rat), coef: coef}
}

func (b *branch) clone() *branch {
	coef := make([]*big.Rat, len(b.coef))
	for i, q := range b.coef {
		coef[i] = new(big.Rat).Set(q)
	}
	return &branch{c: new(big.Rat).Set(b.c), coef: coef}
}

func addLengthTerm(b *branch, length uint64, ref weightRef) *branch {
	if ref.isVar {
		b.coef[ref.varIdx].Add(
			b.coef[ref.varIdx],
			new(big.Rat).SetUint64(length),
		)
		return b
	}
	b.c.Add(
		b.c,
		new(big.Rat).SetFrac(
			new(big.Int).SetUint64(length),
			new(big.Int).SetUint64(ref.fixed),
		),
	)
	return b
}

func addBranch(a, b *branch) *branch {
	out := zeroBranch(len(a.coef))
	out.c.Add(a.c, b.c)
	for i := range out.coef {
		out.coef[i].Add(a.coef[i], b.coef[i])
	}
	return out
}

func subBranch(a, b *branch) *branch {
	out := zeroBranch(len(a.coef))
	out.c.Sub(a.c, b.c)
	for i := range out.coef {
		out.coef[i].Sub(a.coef[i], b.coef[i])
	}
	return out
}

func (b *branch) extrema(dom *box) (min, max *big.Rat) {
	min = new(big.Rat).Set(b.c)
	max = new(big.Rat).Set(b.c)
	w := new(big.Rat)
	termMin := new(big.Rat)
	termMax := new(big.Rat)

	for i, coef := range b.coef {
		sign := coef.Sign()
		if sign == 0 {
			continue
		}
		if sign > 0 {
			w.SetInt64(dom.hi[i])
			termMin.Quo(coef, w)
			w.SetInt64(dom.lo[i])
			termMax.Quo(coef, w)
		} else {
			w.SetInt64(dom.lo[i])
			termMin.Quo(coef, w)
			w.SetInt64(dom.hi[i])
			termMax.Quo(coef, w)
		}
		min.Add(min, termMin)
		max.Add(max, termMax)
	}
	return min, max
}

// pairAlways reports whether a R b for every real point in dom. When strict
// is false R is <=; otherwise R is <.
func pairAlways(a, b *branch, strict bool, dom *box) bool {
	d := subBranch(a, b)
	_, max := d.extrema(dom)
	if strict {
		return max.Sign() < 0
	}
	return max.Sign() <= 0
}

// pairNever reports whether a R b is false for every real point in dom.
func pairNever(a, b *branch, strict bool, dom *box) bool {
	d := subBranch(a, b)
	min, _ := d.extrema(dom)
	if strict {
		// a < b is universally false iff a >= b everywhere.
		return min.Sign() >= 0
	}
	// a <= b is universally false iff a > b everywhere.
	return min.Sign() > 0
}

func sameBranch(a, b *branch) bool {
	if a.c.Cmp(b.c) != 0 || len(a.coef) != len(b.coef) {
		return false
	}
	for i := range a.coef {
		if a.coef[i].Cmp(b.coef[i]) != 0 {
			return false
		}
	}
	return true
}

func dedupeBranches(in branchSet) branchSet {
	out := make(branchSet, 0, len(in))
	for _, b := range in {
		dup := false
		for _, q := range out {
			if sameBranch(b, q) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, b)
		}
	}
	return out
}

// pruneBranches removes y when another branch is >= y throughout dom. Such a
// branch can never change the value of max(set).
func pruneBranches(in branchSet, dom *box) branchSet {
	in = dedupeBranches(in)
	if len(in) <= 1 {
		return in
	}
	keep := make([]bool, len(in))
	for i := range keep {
		keep[i] = true
	}
	for j := range in {
		if !keep[j] {
			continue
		}
		for i := range in {
			if i == j || !keep[j] {
				continue
			}
			if !pairAlways(in[j], in[i], false, dom) {
				continue
			}
			mutual := pairAlways(in[i], in[j], false, dom)
			if mutual && i > j {
				// Equal over this box: retain the lower index.
				continue
			}
			keep[j] = false
		}
	}
	out := make(branchSet, 0, len(in))
	for i, b := range in {
		if keep[i] {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return in[:1]
	}
	return out
}

func unionBranches(groups ...branchSet) branchSet {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	out := make(branchSet, 0, n)
	for _, g := range groups {
		out = append(out, g...)
	}
	return dedupeBranches(out)
}

func finishSetFor(start branchSet, length uint64, ref weightRef, dom *box) branchSet {
	out := make(branchSet, 0, len(start))
	for _, b := range start {
		q := b.clone()
		addLengthTerm(q, length, ref)
		out = append(out, q)
	}
	return pruneBranches(out, dom)
}

// relationAlwaysTrue reports whether max(A) R max(B) for every point in dom.
func relationAlwaysTrue(a, b branchSet, strict bool, dom *box) bool {
	for _, ab := range a {
		covered := false
		for _, bb := range b {
			if pairAlways(ab, bb, strict, dom) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// relationAlwaysFalse reports whether max(A) R max(B) is false for every
// point in dom.
func relationAlwaysFalse(a, b branchSet, strict bool, dom *box) bool {
	for _, ab := range a {
		allOpposite := true
		for _, bb := range b {
			if !pairNever(ab, bb, strict, dom) {
				allOpposite = false
				break
			}
		}
		if allOpposite {
			return true
		}
	}
	return false
}

func pathStrict(tieA, tieB uint64) bool {
	// The smaller tenant number wins an exact tie. If A has the smaller
	// number, A may win with <=; otherwise A needs strict <.
	return tieA > tieB
}

func pathAlwaysWins(a, b branchSet, tieA, tieB uint64, dom *box) bool {
	return relationAlwaysTrue(a, b, pathStrict(tieA, tieB), dom)
}

func pathAlwaysLoses(a, b branchSet, tieA, tieB uint64, dom *box) bool {
	strict := pathStrict(tieA, tieB)
	return relationAlwaysFalse(a, b, strict, dom)
}
