package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Material describes a purchasable (or only convertible) material.
type Material struct {
	Name        string
	Purchasable bool
	PackageSize *big.Rat // quantity per package (purchasable only)
	PackageCost *big.Rat // cost per package (purchasable only)
}

// Rule is a substitution recipe: applying it once consumes Inputs and
// produces Outputs. Rules may be applied at any non-negative rational scale.
type Rule struct {
	Name    string
	Inputs  map[string]*big.Rat
	Outputs map[string]*big.Rat
}

type Problem struct {
	Materials []Material
	Demands   map[string]*big.Rat
	Rules     []Rule
}

func (p *Problem) materialIndex() map[string]int {
	idx := make(map[string]int, len(p.Materials))
	for i, m := range p.Materials {
		idx[m.Name] = i
	}
	return idx
}

func netOf(r Rule, material string) *big.Rat {
	net := zeroRat()
	if v, ok := r.Outputs[material]; ok {
		net.Add(net, v)
	}
	if v, ok := r.Inputs[material]; ok {
		net.Sub(net, v)
	}
	return net
}

func validate(p *Problem) error {
	seen := map[string]bool{}
	for _, m := range p.Materials {
		if m.Name == "" {
			return fmt.Errorf("material with empty name")
		}
		if seen[m.Name] {
			return fmt.Errorf("duplicate material %q", m.Name)
		}
		seen[m.Name] = true
		if m.Purchasable {
			if m.PackageSize == nil || m.PackageSize.Sign() <= 0 {
				return fmt.Errorf("material %q: packageSize must be positive", m.Name)
			}
			if m.PackageCost == nil || m.PackageCost.Sign() <= 0 {
				return fmt.Errorf("material %q: packageCost must be positive", m.Name)
			}
		}
	}
	known := func(name string) error {
		if !seen[name] {
			return fmt.Errorf("unknown material %q", name)
		}
		return nil
	}
	for name, d := range p.Demands {
		if err := known(name); err != nil {
			return err
		}
		if d.Sign() < 0 {
			return fmt.Errorf("demand for %q must be non-negative", name)
		}
	}
	rseen := map[string]bool{}
	for _, r := range p.Rules {
		if r.Name == "" {
			return fmt.Errorf("rule with empty name")
		}
		if rseen[r.Name] {
			return fmt.Errorf("duplicate rule %q", r.Name)
		}
		rseen[r.Name] = true
		if len(r.Inputs)+len(r.Outputs) == 0 {
			return fmt.Errorf("rule %q has no inputs and no outputs", r.Name)
		}
		for name, v := range r.Inputs {
			if err := known(name); err != nil {
				return err
			}
			if v.Sign() <= 0 {
				return fmt.Errorf("rule %q: input %q must be positive", r.Name, name)
			}
		}
		for name, v := range r.Outputs {
			if err := known(name); err != nil {
				return err
			}
			if v.Sign() <= 0 {
				return fmt.Errorf("rule %q: output %q must be positive", r.Name, name)
			}
		}
	}
	return nil
}

// detectProductiveCycle decides whether some non-negative combination of
// rules produces at least as much of every material as it consumes, and
// strictly more of some material. Such a combination is a self-amplifying
// cycle: applied repeatedly it creates available quantity out of nothing,
// which would make the planning model degenerate. This is decided exactly
// by solving one LP (a Farkas-lemma style certificate), not by any
// depth-bounded traversal:
//
//	max  sum_m (V x)_m
//	s.t. V x >= 0   (net production of every material is non-negative)
//	     sum_r x_r <= 1, x >= 0
//
// The maximum is > 0 iff a productive cycle exists. If so, the rules with
// x_r > 0 in the certificate are reported.
func detectProductiveCycle(p *Problem) ([]string, bool) {
	nr := len(p.Rules)
	if nr == 0 {
		return nil, false
	}
	var cons []lpConstraint
	for _, m := range p.Materials {
		coeffs := map[int]*big.Rat{}
		for r, rule := range p.Rules {
			if net := netOf(rule, m.Name); net.Sign() != 0 {
				coeffs[r] = net
			}
		}
		cons = append(cons, lpConstraint{coeffs: coeffs, sense: senseGeq, rhs: zeroRat()})
	}
	sumCoeffs := map[int]*big.Rat{}
	for r := 0; r < nr; r++ {
		sumCoeffs[r] = big.NewRat(1, 1)
	}
	cons = append(cons, lpConstraint{coeffs: sumCoeffs, sense: senseLeq, rhs: big.NewRat(1, 1)})

	objective := make([]*big.Rat, nr)
	for r, rule := range p.Rules {
		total := zeroRat()
		for _, m := range p.Materials {
			total.Add(total, netOf(rule, m.Name))
		}
		objective[r] = new(big.Rat).Neg(total) // minimize negative total net
	}
	st, obj, x := solveLP(nr, cons, objective)
	if st != lpOptimal || obj.Sign() >= 0 {
		return nil, false
	}
	var names []string
	for r, v := range x {
		if v.Sign() > 0 {
			names = append(names, p.Rules[r].Name)
		}
	}
	sort.Strings(names)
	return names, true
}

func floorRat(r *big.Rat) *big.Rat {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func ceilRat(r *big.Rat) *big.Rat {
	f := floorRat(r)
	if f.Cmp(r) == 0 {
		return f
	}
	return new(big.Rat).Add(f, big.NewRat(1, 1))
}

func isIntRat(r *big.Rat) bool {
	return r.Denom().Cmp(big.NewInt(1)) == 0
}

// MaterialPlan reports the flow balance of one material.
type MaterialPlan struct {
	Name      string
	Purchased *big.Rat
	Produced  *big.Rat
	Consumed  *big.Rat
	Demand    *big.Rat
	Surplus   *big.Rat
}

type RuleUsage struct {
	Rule   string
	Amount *big.Rat
}

type Purchase struct {
	Material string
	Packages *big.Rat // integer
	Quantity *big.Rat
}

type Plan struct {
	TotalCost *big.Rat
	Purchases []Purchase
	RuleUsage []RuleUsage
	Materials []MaterialPlan
}

// milp is the branch-and-bound solver over integer package counts with
// rational rule usages handled by the LP relaxation.
type milp struct {
	p         *Problem
	numRules  int
	purch     []int // material indices that are purchasable
	numVars   int   // numRules + len(purch)
	baseCons  []lpConstraint
	objective []*big.Rat
}

func newMILP(p *Problem) *milp {
	m := &milp{p: p, numRules: len(p.Rules)}
	for i, mat := range p.Materials {
		if mat.Purchasable {
			m.purch = append(m.purch, i)
		}
	}
	m.numVars = m.numRules + len(m.purch)
	purchVar := map[int]int{} // material index -> variable index
	for k, mi := range m.purch {
		purchVar[mi] = m.numRules + k
	}
	for i, mat := range p.Materials {
		coeffs := map[int]*big.Rat{}
		for r, rule := range p.Rules {
			if net := netOf(rule, mat.Name); net.Sign() != 0 {
				coeffs[r] = net
			}
		}
		if v, ok := purchVar[i]; ok {
			coeffs[v] = mat.PackageSize
		}
		demand := zeroRat()
		if d, ok := p.Demands[mat.Name]; ok {
			demand.Set(d)
		}
		m.baseCons = append(m.baseCons, lpConstraint{coeffs: coeffs, sense: senseGeq, rhs: demand})
	}
	m.objective = make([]*big.Rat, m.numVars)
	for j := range m.objective {
		m.objective[j] = zeroRat()
	}
	for k, mi := range m.purch {
		m.objective[m.numRules+k] = p.Materials[mi].PackageCost
	}
	return m
}

func (m *milp) relaxation(lower, upper []*big.Rat) (lpStatus, *big.Rat, []*big.Rat) {
	cons := make([]lpConstraint, len(m.baseCons), len(m.baseCons)+2*len(m.purch))
	copy(cons, m.baseCons)
	for k := range m.purch {
		v := m.numRules + k
		if lower[k] != nil && lower[k].Sign() > 0 {
			cons = append(cons, lpConstraint{
				coeffs: map[int]*big.Rat{v: big.NewRat(1, 1)},
				sense:  senseGeq, rhs: lower[k],
			})
		}
		if upper[k] != nil {
			cons = append(cons, lpConstraint{
				coeffs: map[int]*big.Rat{v: big.NewRat(1, 1)},
				sense:  senseLeq, rhs: upper[k],
			})
		}
	}
	return solveLP(m.numVars, cons, m.objective)
}

type bnbNode struct {
	lower []*big.Rat
	upper []*big.Rat
}

func cloneBounds(b []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(b))
	for i, v := range b {
		if v != nil {
			out[i] = new(big.Rat).Set(v)
		}
	}
	return out
}

// solve returns the optimal plan, or nil with feasible=false if infeasible.
func (m *milp) solve() (*Plan, bool) {
	k := len(m.purch)
	lower := make([]*big.Rat, k)
	upper := make([]*big.Rat, k)

	st, _, x0 := m.relaxation(lower, upper)
	if st != lpOptimal {
		return nil, false
	}

	// Initial incumbent: round the relaxation's package counts up. Feasible,
	// because purchases only relax the >= flow constraints.
	bestX := make([]*big.Rat, m.numVars)
	copy(bestX, x0)
	bestCost := zeroRat()
	for j := 0; j < m.numRules; j++ {
		bestX[j] = new(big.Rat).Set(x0[j])
	}
	for i := 0; i < k; i++ {
		b := ceilRat(x0[m.numRules+i])
		bestX[m.numRules+i] = b
		c := new(big.Rat).Mul(b, m.p.Materials[m.purch[i]].PackageCost)
		bestCost.Add(bestCost, c)
	}
	// Any improving solution buys at most floor(bestCost / unitCost) packages
	// of a material; use these as global upper bounds.
	for i := 0; i < k; i++ {
		cost := m.p.Materials[m.purch[i]].PackageCost
		upper[i] = floorRat(new(big.Rat).Quo(bestCost, cost))
	}

	stack := []bnbNode{{lower: lower, upper: upper}}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		st, obj, x := m.relaxation(node.lower, node.upper)
		if st != lpOptimal || obj.Cmp(bestCost) >= 0 {
			continue
		}
		frac := -1
		for i := 0; i < k; i++ {
			if !isIntRat(x[m.numRules+i]) {
				frac = i
				break
			}
		}
		if frac < 0 {
			bestCost = obj
			bestX = x
			continue
		}
		b := x[m.numRules+frac]
		fl := floorRat(b)
		ce := new(big.Rat).Add(fl, big.NewRat(1, 1))

		lo1, hi1 := cloneBounds(node.lower), cloneBounds(node.upper)
		if hi1[frac] == nil || fl.Cmp(hi1[frac]) < 0 {
			hi1[frac] = fl
		}
		stack = append(stack, bnbNode{lower: lo1, upper: hi1})

		lo2, hi2 := cloneBounds(node.lower), cloneBounds(node.upper)
		if lo2[frac] == nil || ce.Cmp(lo2[frac]) > 0 {
			lo2[frac] = ce
		}
		stack = append(stack, bnbNode{lower: lo2, upper: hi2})
	}
	return m.buildPlan(bestX, bestCost), true
}

func (m *milp) buildPlan(x []*big.Rat, cost *big.Rat) *Plan {
	plan := &Plan{TotalCost: cost}
	produced := make([]*big.Rat, len(m.p.Materials))
	consumed := make([]*big.Rat, len(m.p.Materials))
	for i := range produced {
		produced[i] = zeroRat()
		consumed[i] = zeroRat()
	}
	idx := m.p.materialIndex()
	for r, rule := range m.p.Rules {
		amount := x[r]
		if amount.Sign() > 0 {
			plan.RuleUsage = append(plan.RuleUsage, RuleUsage{Rule: rule.Name, Amount: amount})
		}
		for name, v := range rule.Inputs {
			t := new(big.Rat).Mul(amount, v)
			consumed[idx[name]].Add(consumed[idx[name]], t)
		}
		for name, v := range rule.Outputs {
			t := new(big.Rat).Mul(amount, v)
			produced[idx[name]].Add(produced[idx[name]], t)
		}
	}
	purchased := make([]*big.Rat, len(m.p.Materials))
	for i := range purchased {
		purchased[i] = zeroRat()
	}
	for i, mi := range m.purch {
		b := x[m.numRules+i]
		if b.Sign() > 0 {
			mat := m.p.Materials[mi]
			qty := new(big.Rat).Mul(b, mat.PackageSize)
			purchased[mi] = qty
			plan.Purchases = append(plan.Purchases, Purchase{Material: mat.Name, Packages: b, Quantity: qty})
		}
	}
	for i, mat := range m.p.Materials {
		demand := zeroRat()
		if d, ok := m.p.Demands[mat.Name]; ok {
			demand.Set(d)
		}
		avail := zeroRat()
		avail.Add(avail, purchased[i])
		avail.Add(avail, produced[i])
		avail.Sub(avail, consumed[i])
		surplus := new(big.Rat).Sub(avail, demand)
		plan.Materials = append(plan.Materials, MaterialPlan{
			Name:      mat.Name,
			Purchased: purchased[i],
			Produced:  produced[i],
			Consumed:  consumed[i],
			Demand:    demand,
			Surplus:   surplus,
		})
	}
	return plan
}

// SolveProblem validates, checks for productive cycles, and solves.
// Returns (plan, cycleRules, feasible, error):
//   - error != nil: invalid input
//   - cycleRules != nil: rejected due to a productive cycle
//   - feasible == false: demands cannot be met
func SolveProblem(p *Problem) (*Plan, []string, bool, error) {
	if err := validate(p); err != nil {
		return nil, nil, false, err
	}
	if names, bad := detectProductiveCycle(p); bad {
		return nil, names, false, nil
	}
	plan, feasible := newMILP(p).solve()
	if !feasible {
		return nil, nil, false, nil
	}
	return plan, nil, true, nil
}
