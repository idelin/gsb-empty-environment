// Package recipex solves production-recipe substitution planning.
//
// Model: each demand (order line) of Quantity units can be satisfied by one
// of several substitute options. Option j consumes Ratio units of a main
// material per demand unit (integer ratio), and additionally AuxRatio units
// of an auxiliary material per unit of main material consumed. Inventory is
// finite. The solver maximizes the number of fully satisfied demands, then
// minimizes total cost, and reports per-material consumption evidence.
package recipex

import (
	"errors"
	"fmt"
	"sort"
)

// MaxDemands is the supported upper bound on the number of demands.
const MaxDemands = 10

// Option is one substitute way to satisfy a demand.
type Option struct {
	Material string // main substitute material consumed
	Ratio    int    // units of Material per unit of demand, integer >= 1
	Aux      string // auxiliary material additionally consumed; "" means none
	AuxRatio int    // units of Aux per unit of Material consumed, >= 0
	Cost     int64  // cost per unit of demand satisfied via this option
}

// Demand is a single order line.
type Demand struct {
	ID       string
	Quantity int
	Options  []Option
}

// Assignment records how one demand is satisfied.
type Assignment struct {
	DemandID    string
	Quantity    int
	Option      Option
	MaterialUse int64 // Quantity * Ratio
	AuxUse      int64 // Quantity * Ratio * AuxRatio
	Cost        int64 // Quantity * Option.Cost
}

// Consumption is the evidence line for one material.
type Consumption struct {
	Material  string
	Used      int64
	Remaining int64
}

// Result is the solver output.
type Result struct {
	Orders      int           // number of fully satisfied demands
	Cost        int64         // total cost of the optimal plan
	Assignments []Assignment  // one per satisfied demand
	Evidence    []Consumption // per-material usage, sorted by material
}

// Validate checks structural constraints of the problem.
func Validate(demands []Demand, inventory map[string]int64) error {
	if len(demands) == 0 {
		return errors.New("recipex: no demands")
	}
	if len(demands) > MaxDemands {
		return fmt.Errorf("recipex: %d demands exceed limit %d", len(demands), MaxDemands)
	}
	seen := make(map[string]bool)
	for _, d := range demands {
		if d.ID == "" {
			return errors.New("recipex: demand with empty ID")
		}
		if seen[d.ID] {
			return fmt.Errorf("recipex: duplicate demand ID %q", d.ID)
		}
		seen[d.ID] = true
		if d.Quantity <= 0 {
			return fmt.Errorf("recipex: demand %q has non-positive quantity %d", d.ID, d.Quantity)
		}
		if len(d.Options) == 0 {
			return fmt.Errorf("recipex: demand %q has no substitute options", d.ID)
		}
		for j, o := range d.Options {
			if o.Material == "" {
				return fmt.Errorf("recipex: demand %q option %d has empty material", d.ID, j)
			}
			if o.Ratio < 1 {
				return fmt.Errorf("recipex: demand %q option %d has ratio %d < 1", d.ID, j, o.Ratio)
			}
			if o.Aux == "" && o.AuxRatio != 0 {
				return fmt.Errorf("recipex: demand %q option %d sets aux ratio without aux material", d.ID, j)
			}
			if o.Aux != "" && o.AuxRatio < 1 {
				return fmt.Errorf("recipex: demand %q option %d has aux ratio %d < 1", d.ID, j, o.AuxRatio)
			}
			if o.Cost < 0 {
				return fmt.Errorf("recipex: demand %q option %d has negative cost", d.ID, j)
			}
		}
	}
	for m, q := range inventory {
		if q < 0 {
			return fmt.Errorf("recipex: negative inventory %d for material %q", q, m)
		}
	}
	return nil
}

// Solve finds a plan maximizing the number of fully satisfied demands and,
// among those, minimizing total cost. Inventory limits are never exceeded.
func Solve(demands []Demand, inventory map[string]int64) (Result, error) {
	if err := Validate(demands, inventory); err != nil {
		return Result{}, err
	}
	n := len(demands)

	// Precompute per-option consumption.
	type optUse struct {
		matUse int64
		auxUse int64
		cost   int64
	}
	uses := make([][]optUse, n)
	for i, d := range demands {
		uses[i] = make([]optUse, len(d.Options))
		for j, o := range d.Options {
			uses[i][j] = optUse{
				matUse: int64(d.Quantity) * int64(o.Ratio),
				auxUse: int64(d.Quantity) * int64(o.Ratio) * int64(o.AuxRatio),
				cost:   int64(d.Quantity) * o.Cost,
			}
		}
	}

	remaining := make(map[string]int64, len(inventory))
	for m, q := range inventory {
		remaining[m] = q
	}

	best := Result{Orders: 0, Cost: 0}
	choice := make([]int, n) // -1 = demand not satisfied
	bestChoice := make([]int, n)
	for i := range choice {
		choice[i] = -1
		bestChoice[i] = -1
	}

	var dfs func(i, orders int, cost int64)
	dfs = func(i, orders int, cost int64) {
		// Bound: even satisfying every remaining demand cannot beat best.
		if orders+n-i < best.Orders {
			return
		}
		if i == n {
			if orders > best.Orders || (orders == best.Orders && cost < best.Cost) {
				best.Orders = orders
				best.Cost = cost
				copy(bestChoice, choice)
			}
			return
		}
		d := demands[i]
		// Try each substitute option.
		for j, o := range d.Options {
			u := uses[i][j]
			if remaining[o.Material] < u.matUse {
				continue
			}
			if o.Aux != "" && remaining[o.Aux] < u.auxUse {
				continue
			}
			remaining[o.Material] -= u.matUse
			if o.Aux != "" {
				remaining[o.Aux] -= u.auxUse
			}
			choice[i] = j
			dfs(i+1, orders+1, cost+u.cost)
			choice[i] = -1
			remaining[o.Material] += u.matUse
			if o.Aux != "" {
				remaining[o.Aux] += u.auxUse
			}
		}
		// Skip this demand.
		dfs(i+1, orders, cost)
	}
	dfs(0, 0, 0)

	// Materialize assignments and evidence from bestChoice.
	used := make(map[string]int64)
	for i, j := range bestChoice {
		if j < 0 {
			continue
		}
		d := demands[i]
		o := d.Options[j]
		u := uses[i][j]
		best.Assignments = append(best.Assignments, Assignment{
			DemandID:    d.ID,
			Quantity:    d.Quantity,
			Option:      o,
			MaterialUse: u.matUse,
			AuxUse:      u.auxUse,
			Cost:        u.cost,
		})
		used[o.Material] += u.matUse
		if o.Aux != "" {
			used[o.Aux] += u.auxUse
		}
	}
	mats := make(map[string]bool)
	for m := range inventory {
		mats[m] = true
	}
	for m := range used {
		mats[m] = true
	}
	names := make([]string, 0, len(mats))
	for m := range mats {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		best.Evidence = append(best.Evidence, Consumption{
			Material:  m,
			Used:      used[m],
			Remaining: inventory[m] - used[m],
		})
	}
	return best, nil
}
