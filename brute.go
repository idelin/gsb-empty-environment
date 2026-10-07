package recipex

import "sort"

// BruteForceSolve independently verifies Solve by fully enumerating every
// assignment of demands to options (including skipping a demand). It is
// exponential and intended for small instances and cross-checking only.
func BruteForceSolve(demands []Demand, inventory map[string]int64) (Result, error) {
	if err := Validate(demands, inventory); err != nil {
		return Result{}, err
	}
	n := len(demands)
	choice := make([]int, n)
	for i := range choice {
		choice[i] = -1
	}
	best := Result{}
	bestChoice := make([]int, n)
	copy(bestChoice, choice)

	var rec func(i int)
	rec = func(i int) {
		if i == n {
			orders, cost, ok := evaluate(demands, inventory, choice)
			if !ok {
				return
			}
			if orders > best.Orders || (orders == best.Orders && cost < best.Cost) {
				best.Orders = orders
				best.Cost = cost
				copy(bestChoice, choice)
			}
			return
		}
		choice[i] = -1
		rec(i + 1)
		for j := range demands[i].Options {
			choice[i] = j
			rec(i + 1)
		}
		choice[i] = -1
	}
	rec(0)

	used := make(map[string]int64)
	for i, j := range bestChoice {
		if j < 0 {
			continue
		}
		d := demands[i]
		o := d.Options[j]
		matUse := int64(d.Quantity) * int64(o.Ratio)
		auxUse := matUse * int64(o.AuxRatio)
		best.Assignments = append(best.Assignments, Assignment{
			DemandID:    d.ID,
			Quantity:    d.Quantity,
			Option:      o,
			MaterialUse: matUse,
			AuxUse:      auxUse,
			Cost:        int64(d.Quantity) * o.Cost,
		})
		used[o.Material] += matUse
		if o.Aux != "" {
			used[o.Aux] += auxUse
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

// evaluate checks feasibility of a full choice vector and returns its
// (orders, cost). Choice vectors that exceed any inventory are infeasible.
func evaluate(demands []Demand, inventory map[string]int64, choice []int) (int, int64, bool) {
	used := make(map[string]int64)
	orders := 0
	var cost int64
	for i, j := range choice {
		if j < 0 {
			continue
		}
		d := demands[i]
		o := d.Options[j]
		matUse := int64(d.Quantity) * int64(o.Ratio)
		used[o.Material] += matUse
		if o.Aux != "" {
			used[o.Aux] += matUse * int64(o.AuxRatio)
		}
		cost += int64(d.Quantity) * o.Cost
		orders++
	}
	for m, u := range used {
		if u > inventory[m] {
			return 0, 0, false
		}
	}
	return orders, cost, true
}
