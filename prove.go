package netsec

// checkAccess tries to prove 0 <= idx+Offset <= len-1 from the closed
// invariant at the access node and records the constraint basis.
func (g *Graph) checkAccess(inv *dbm, a Access) AccessResult {
	r := AccessResult{Access: a}
	if inv == nil {
		r.Proven = true
		r.Unreachable = true
		r.LowerOK, r.UpperOK = true, true
		return r
	}
	const zero = 0
	length := g.arrays[a.Array]
	needLo := a.Offset              // require 0 - idx <= Offset
	needHi := length - 1 - a.Offset // require idx - 0 <= len-1-Offset
	haveLo := inv.at(zero, a.Index)
	haveHi := inv.at(a.Index, zero)

	r.NeedLower = Constraint{X: g.vars[zero], Y: g.vars[a.Index], Bound: needLo}
	r.HaveLower = Constraint{X: g.vars[zero], Y: g.vars[a.Index], Bound: haveLo}
	r.NeedUpper = Constraint{X: g.vars[a.Index], Y: g.vars[zero], Bound: needHi}
	r.HaveUpper = Constraint{X: g.vars[a.Index], Y: g.vars[zero], Bound: haveHi}
	r.LowerOK = haveLo <= needLo
	r.UpperOK = haveHi <= needHi
	r.Proven = r.LowerOK && r.UpperOK

	if r.LowerOK {
		r.Basis = append(r.Basis, g.basis(inv, zero, a.Index)...)
	}
	if r.UpperOK {
		r.Basis = append(r.Basis, g.basis(inv, a.Index, zero)...)
	}
	return r
}

// basis reconstructs the chain of difference constraints whose sum is the
// derived bound from -> to.
func (g *Graph) basis(inv *dbm, from, to int) []Constraint {
	var cs []Constraint
	for _, e := range inv.path(from, to) {
		cs = append(cs, Constraint{
			X: g.vars[e[0]], Y: g.vars[e[1]], Bound: inv.at(e[0], e[1]),
		})
	}
	return cs
}
