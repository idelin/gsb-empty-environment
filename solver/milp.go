package solver

import (
	"errors"
	"math/big"
)

// milp is a minimization problem over non-negative integer variables with
// rational costs and linear constraints.
type milp struct {
	cost []*big.Rat
	cons []lpConstraint // includes variable upper bounds
	nVar int
}

type milpResult struct {
	obj    *big.Rat
	x      []*big.Rat
	proven bool // false if the node limit cut the search short
	nodes  int
}

var errUnboundedRelaxation = errors.New("internal error: unbounded LP relaxation despite variable bounds")

// solve runs branch and bound: the LP relaxation (solved exactly) gives
// lower bounds; fractional variables are branched on until all variables
// are integral. All variables are bounded, so the search is finite.
func (m *milp) solve(maxNodes int) (milpResult, error) {
	type node struct{ extra []lpConstraint }
	stack := []node{{}}
	var best *big.Rat
	var bestX []*big.Rat
	nodes := 0
	proven := true

	oneHot := func(j int, s sense, bound *big.Rat) lpConstraint {
		coeffs := make([]*big.Rat, m.nVar)
		for k := range coeffs {
			coeffs[k] = ratInt(0)
		}
		coeffs[j] = ratInt(1)
		return lpConstraint{coeffs: coeffs, sense: s, rhs: bound}
	}

	for len(stack) > 0 {
		nd := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > maxNodes {
			proven = false
			break
		}
		cons := make([]lpConstraint, 0, len(m.cons)+len(nd.extra))
		cons = append(cons, m.cons...)
		cons = append(cons, nd.extra...)

		st, obj, x := simplex(m.cost, cons)
		if st == lpUnbounded {
			return milpResult{}, errUnboundedRelaxation
		}
		if st == lpInfeasible {
			continue
		}
		if best != nil && obj.Cmp(best) >= 0 {
			continue
		}
		frac := -1
		for j := 0; j < m.nVar; j++ {
			if !x[j].IsInt() {
				frac = j
				break
			}
		}
		if frac == -1 {
			best = obj
			bestX = x
			continue
		}
		// Branch on the first fractional variable. Values are >= 0, so
		// truncated division is the floor.
		floor := new(big.Int).Quo(x[frac].Num(), x[frac].Denom())
		ceil := new(big.Int).Add(floor, big.NewInt(1))
		down := append(append([]lpConstraint{}, nd.extra...), oneHot(frac, senseLe, new(big.Rat).SetInt(floor)))
		up := append(append([]lpConstraint{}, nd.extra...), oneHot(frac, senseGe, new(big.Rat).SetInt(ceil)))
		stack = append(stack, node{up}, node{down}) // explore "down" first
	}
	return milpResult{obj: best, x: bestX, proven: proven, nodes: nodes}, nil
}

// buildMILP assembles the procurement model:
//
//	variables  p_i = packages bought of material i   (integer, bounded)
//	           y_r = applications of rule r          (integer, bounded)
//	minimize   sum_i packageCost_i * p_i
//	subject to packageSize_i * p_i + sum_r M_ir * y_r >= demand_i  for all i
//
// The balance constraint says available units (purchased plus net produced
// by substitutions) must cover demand; surplus is waste.
func (p *Problem) buildMILP() *milp {
	nm := len(p.Materials)
	nr := len(p.Rules)
	nVar := nm + nr
	net := p.netMatrix()

	cost := make([]*big.Rat, nVar)
	for i := range p.Materials {
		cost[i] = ratClone(p.Materials[i].cost)
	}
	for r := 0; r < nr; r++ {
		cost[nm+r] = ratInt(0)
	}

	cons := make([]lpConstraint, 0, nm+nVar)
	for i := range p.Materials {
		coeffs := make([]*big.Rat, nVar)
		for j := range coeffs {
			coeffs[j] = ratInt(0)
		}
		coeffs[i] = ratInt(p.Materials[i].PackageSize)
		for r := 0; r < nr; r++ {
			coeffs[nm+r] = ratInt(net[i][r])
		}
		cons = append(cons, lpConstraint{coeffs: coeffs, sense: senseGe, rhs: ratInt(p.demand[i])})
	}
	bound := func(j int, ub int64) {
		coeffs := make([]*big.Rat, nVar)
		for k := range coeffs {
			coeffs[k] = ratInt(0)
		}
		coeffs[j] = ratInt(1)
		cons = append(cons, lpConstraint{coeffs: coeffs, sense: senseLe, rhs: ratInt(ub)})
	}
	for i := 0; i < nm; i++ {
		bound(i, p.Limits.MaxPackages)
	}
	for r := 0; r < nr; r++ {
		bound(nm+r, p.Limits.MaxApplications)
	}
	return &milp{cost: cost, cons: cons, nVar: nVar}
}
