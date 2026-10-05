package solver

import "math/big"

// DetectGainCycle reports whether the substitution rules can amplify the
// available amount of some material out of nothing, i.e. whether there is a
// non-negative combination of rule applications y != 0 whose net effect
// M*y is >= 0 in every material and > 0 in at least one (M is the net
// production matrix). Because M is integral, a rational such y can always be
// scaled to whole rule applications, so existence over the rationals is
// exactly the property we must reject.
//
// The check is the linear program
//
//	maximize   sum_i (M y)_i
//	subject to 0 <= (M y)_i <= 1   for every material i
//	           y >= 0
//
// which is feasible (y = 0) and bounded (objective <= #materials), and whose
// optimum is > 0 iff a gain combination exists. This is an exact,
// complete criterion (the Farkas alternative is a positive price system
// under which no rule is profitable); it does not traverse rule graphs and
// therefore needs no depth cutoff that could mask deep or compounded
// cycles.
//
// On detection it returns the names of the rules used with positive
// intensity in a witnessing combination.
func DetectGainCycle(p *Problem) ([]string, bool) {
	if p.matIndex == nil {
		// Allow calls on a problem that Solve has not validated yet.
		if err := p.validate(); err != nil {
			return nil, false
		}
	}
	if len(p.Rules) == 0 {
		return nil, false
	}
	net := p.netMatrix()
	nm := len(p.Materials)
	nr := len(p.Rules)

	// Variables: y_0..y_{nr-1} >= 0.
	cost := make([]*big.Rat, nr)
	for r := 0; r < nr; r++ {
		// maximize sum_i (M y)_i  <=>  minimize -sum_i M_ir * y_r
		col := ratInt(0)
		for i := 0; i < nm; i++ {
			col.Sub(col, ratInt(net[i][r]))
		}
		cost[r] = col
	}
	cons := make([]lpConstraint, 0, 2*nm)
	for i := 0; i < nm; i++ {
		coeffs := make([]*big.Rat, nr)
		for r := 0; r < nr; r++ {
			coeffs[r] = ratInt(net[i][r])
		}
		cons = append(cons,
			lpConstraint{coeffs: coeffs, sense: senseGe, rhs: ratInt(0)},
			lpConstraint{coeffs: coeffs, sense: senseLe, rhs: ratInt(1)},
		)
	}

	st, obj, y := simplex(cost, cons)
	if st != lpOptimal {
		// The LP is feasible (y = 0) and bounded by construction.
		return nil, false
	}
	if obj.Sign() >= 0 {
		return nil, false // optimum of the maximization is <= 0: no gain
	}
	var names []string
	for r, rule := range p.Rules {
		if y[r].Sign() > 0 {
			names = append(names, rule.Name)
		}
	}
	return sortedRuleNames(names), true
}
