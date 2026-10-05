// Package solver finds minimum-cost material procurement and consumption
// plans with rational-ratio, multi-level substitution rules, auxiliary
// material consumption, integer package quantities and bounded scale.
//
// Substitution rule sets that can amplify the available amount of any
// material out of nothing (gain cycles) are detected exactly, with exact
// rational linear programming, and rejected before any optimization runs.
package solver

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Port is a quantity of one material consumed or produced by a rule.
type Port struct {
	Material string `json:"material"`
	Quantity int64  `json:"quantity"`
}

// Material describes a purchasable material. Quantities are bought in whole
// packages of PackageSize units, each costing PackageCost (a rational number,
// e.g. "12", "3.5" or "7/2").
type Material struct {
	Name        string `json:"name"`
	PackageSize int64  `json:"packageSize"`
	PackageCost string `json:"packageCost"`

	cost *big.Rat
}

// Rule converts Input.Quantity units of Input.Material into Output.Quantity
// units of Output.Material, additionally consuming every Aux port. The
// substitution ratio is the rational Output.Quantity / Input.Quantity.
// Rules compose: output of one rule may feed another (multi-level
// substitution). A rule is applied a whole number of times.
type Rule struct {
	Name   string `json:"name"`
	Input  Port   `json:"input"`
	Output Port   `json:"output"`
	Aux    []Port `json:"aux,omitempty"`
}

// Demand is a required amount of a material.
type Demand struct {
	Material string `json:"material"`
	Quantity int64  `json:"quantity"`
}

// Limits bound the search scale. Zero means the documented default.
type Limits struct {
	MaxPackages     int64 `json:"maxPackages"`     // per material, default 1000
	MaxApplications int64 `json:"maxApplications"` // per rule, default 1000
	MaxNodes        int   `json:"maxNodes"`        // branch-and-bound nodes, default 100000
}

// Problem is a full procurement instance.
type Problem struct {
	Materials []Material `json:"materials"`
	Demands   []Demand   `json:"demands"`
	Rules     []Rule     `json:"rules"`
	Limits    Limits     `json:"limits"`

	matIndex map[string]int
	demand   []int64
}

// PurchasePlan is one line of the procurement plan.
type PurchasePlan struct {
	Material string `json:"material"`
	Packages int64  `json:"packages"`
	Units    int64  `json:"units"`
	Cost     string `json:"cost"`
}

// RuleApplication is one line of the consumption (substitution) plan.
type RuleApplication struct {
	Rule  string `json:"rule"`
	Times int64  `json:"times"`
}

// MaterialBalance reports, per material, where units came from and went.
type MaterialBalance struct {
	Material  string `json:"material"`
	Purchased int64  `json:"purchased"`
	Produced  int64  `json:"produced"`
	Consumed  int64  `json:"consumed"`
	Demand    int64  `json:"demand"`
	Leftover  int64  `json:"leftover"`
}

// Solution is a minimum-cost procurement and consumption plan.
type Solution struct {
	TotalCost    string            `json:"totalCost"`
	Purchases    []PurchasePlan    `json:"purchases"`
	Applications []RuleApplication `json:"applications"`
	Balances     []MaterialBalance `json:"balances"`
	// Proven is false if the node limit was hit before optimality was
	// proven; the returned plan is then the best found, not proven minimal.
	Proven bool `json:"proven"`
	Nodes  int  `json:"nodes"`
}

// GainCycleError reports that the substitution rules can amplify the
// available amount of some material out of nothing: a non-negative
// combination of rule applications whose net effect is to consume nothing
// and strictly produce something. Such instances are rejected.
type GainCycleError struct {
	Rules []string
}

func (e *GainCycleError) Error() string {
	return fmt.Sprintf("substitution rules can create available quantity out of nothing (gain cycle involves rules: %s)",
		strings.Join(e.Rules, ", "))
}

// ErrInfeasible reports that no plan meets the demand within the limits.
var ErrInfeasible = errors.New("no feasible procurement plan within the configured limits")

// ErrNodeLimit reports that the search was cut off before finding any plan.
var ErrNodeLimit = errors.New("branch-and-bound node limit exceeded before finding a feasible plan")

const (
	defaultMaxPackages     = 1000
	defaultMaxApplications = 1000
	defaultMaxNodes        = 100000
)

func (p *Problem) validate() error {
	if len(p.Materials) == 0 {
		return errors.New("problem must declare at least one material")
	}
	p.matIndex = make(map[string]int, len(p.Materials))
	for i := range p.Materials {
		m := &p.Materials[i]
		if m.Name == "" {
			return fmt.Errorf("material %d: empty name", i)
		}
		if _, dup := p.matIndex[m.Name]; dup {
			return fmt.Errorf("duplicate material %q", m.Name)
		}
		if m.PackageSize < 1 {
			return fmt.Errorf("material %q: packageSize must be >= 1", m.Name)
		}
		cost, ok := new(big.Rat).SetString(m.PackageCost)
		if !ok {
			return fmt.Errorf("material %q: invalid packageCost %q", m.Name, m.PackageCost)
		}
		if cost.Sign() < 0 {
			return fmt.Errorf("material %q: packageCost must be >= 0", m.Name)
		}
		m.cost = cost
		p.matIndex[m.Name] = i
	}
	checkPort := func(owner string, port Port) error {
		if _, ok := p.matIndex[port.Material]; !ok {
			return fmt.Errorf("%s: unknown material %q", owner, port.Material)
		}
		if port.Quantity < 1 {
			return fmt.Errorf("%s: quantity for %q must be >= 1", owner, port.Material)
		}
		return nil
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("rule%d", i)
		}
		if err := checkPort(r.Name+" input", r.Input); err != nil {
			return err
		}
		if err := checkPort(r.Name+" output", r.Output); err != nil {
			return err
		}
		for _, a := range r.Aux {
			if err := checkPort(r.Name+" aux", a); err != nil {
				return err
			}
		}
	}
	p.demand = make([]int64, len(p.Materials))
	for _, d := range p.Demands {
		idx, ok := p.matIndex[d.Material]
		if !ok {
			return fmt.Errorf("demand: unknown material %q", d.Material)
		}
		if d.Quantity < 0 {
			return fmt.Errorf("demand for %q must be >= 0", d.Material)
		}
		p.demand[idx] += d.Quantity
	}
	if p.Limits.MaxPackages <= 0 {
		p.Limits.MaxPackages = defaultMaxPackages
	}
	if p.Limits.MaxApplications <= 0 {
		p.Limits.MaxApplications = defaultMaxApplications
	}
	if p.Limits.MaxNodes <= 0 {
		p.Limits.MaxNodes = defaultMaxNodes
	}
	return nil
}

// netMatrix returns the materials x rules net production matrix: column r is
// the net change of every material when rule r is applied once (output
// produced, input and aux consumed).
func (p *Problem) netMatrix() [][]int64 {
	net := make([][]int64, len(p.Materials))
	for i := range net {
		net[i] = make([]int64, len(p.Rules))
	}
	for r, rule := range p.Rules {
		net[p.matIndex[rule.Output.Material]][r] += rule.Output.Quantity
		net[p.matIndex[rule.Input.Material]][r] -= rule.Input.Quantity
		for _, a := range rule.Aux {
			net[p.matIndex[a.Material]][r] -= a.Quantity
		}
	}
	return net
}

// Solve validates the problem, rejects gain-cycle rule sets, and computes a
// minimum-cost plan of whole-package purchases and whole rule applications.
func Solve(p *Problem) (*Solution, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if rules, bad := DetectGainCycle(p); bad {
		return nil, &GainCycleError{Rules: rules}
	}
	res, err := p.buildMILP().solve(p.Limits.MaxNodes)
	if err != nil {
		return nil, err
	}
	if res.x == nil {
		if res.proven {
			return nil, ErrInfeasible
		}
		return nil, ErrNodeLimit
	}
	return p.buildSolution(res), nil
}

func (p *Problem) buildSolution(res milpResult) *Solution {
	nm := len(p.Materials)
	pkgs := make([]int64, nm)
	apps := make([]int64, len(p.Rules))
	for i := 0; i < nm; i++ {
		pkgs[i] = res.x[i].Num().Int64()
	}
	for r := range p.Rules {
		apps[r] = res.x[nm+r].Num().Int64()
	}

	sol := &Solution{TotalCost: res.obj.RatString(), Proven: res.proven, Nodes: res.nodes}
	for i, m := range p.Materials {
		if pkgs[i] == 0 {
			continue
		}
		cost := new(big.Rat).Mul(m.cost, big.NewRat(pkgs[i], 1))
		sol.Purchases = append(sol.Purchases, PurchasePlan{
			Material: m.Name,
			Packages: pkgs[i],
			Units:    pkgs[i] * m.PackageSize,
			Cost:     cost.RatString(),
		})
	}
	for r, rule := range p.Rules {
		if apps[r] > 0 {
			sol.Applications = append(sol.Applications, RuleApplication{Rule: rule.Name, Times: apps[r]})
		}
	}
	for i, m := range p.Materials {
		bal := MaterialBalance{
			Material:  m.Name,
			Purchased: pkgs[i] * m.PackageSize,
			Demand:    p.demand[i],
		}
		for r, rule := range p.Rules {
			if apps[r] == 0 {
				continue
			}
			if rule.Output.Material == m.Name {
				bal.Produced += rule.Output.Quantity * apps[r]
			}
			if rule.Input.Material == m.Name {
				bal.Consumed += rule.Input.Quantity * apps[r]
			}
			for _, a := range rule.Aux {
				if a.Material == m.Name {
					bal.Consumed += a.Quantity * apps[r]
				}
			}
		}
		bal.Leftover = bal.Purchased + bal.Produced - bal.Consumed - bal.Demand
		sol.Balances = append(sol.Balances, bal)
	}
	return sol
}

// String renders a human-readable summary of the solution.
func (s *Solution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total cost: %s\n", s.TotalCost)
	if !s.Proven {
		b.WriteString("WARNING: node limit hit, plan is best-effort, not proven optimal\n")
	}
	b.WriteString("purchases:\n")
	if len(s.Purchases) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, p := range s.Purchases {
		fmt.Fprintf(&b, "  %s: %d packages (%d units), cost %s\n", p.Material, p.Packages, p.Units, p.Cost)
	}
	b.WriteString("substitutions:\n")
	if len(s.Applications) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, a := range s.Applications {
		fmt.Fprintf(&b, "  %s x%d\n", a.Rule, a.Times)
	}
	b.WriteString("balances:\n")
	for _, bal := range s.Balances {
		fmt.Fprintf(&b, "  %s: purchased=%d produced=%d consumed=%d demand=%d leftover=%d\n",
			bal.Material, bal.Purchased, bal.Produced, bal.Consumed, bal.Demand, bal.Leftover)
	}
	return b.String()
}

// sortedRuleNames is a small helper for deterministic diagnostics.
func sortedRuleNames(rules []string) []string {
	out := append([]string(nil), rules...)
	sort.Strings(out)
	return out
}
