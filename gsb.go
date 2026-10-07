// Package gsb solves production-recipe substitution problems: each demand of
// an order may be satisfied by one of several substitute materials at an
// integer ratio, and every use of a substitute additionally consumes shared
// auxiliary materials. Given limited inventory and at most MaxDemands
// demands, Solve maximizes the number of complete orders and, among all
// solutions achieving that maximum, minimizes total material cost.
package gsb

import (
	"errors"
	"fmt"
)

// MaxDemands is the maximum number of demands (order lines) per problem.
const MaxDemands = 10

// Material is a stocked resource: a substitute material or an auxiliary.
type Material struct {
	Name     string
	Stock    int64 // available units
	UnitCost int64 // cost per consumed unit
}

// AuxUse is an auxiliary material consumed when a substitute is used.
type AuxUse struct {
	Material int   // index into Problem.Materials
	Amount   int64 // units consumed per demand unit, >= 1
}

// Option is one way to satisfy one unit of a demand.
type Option struct {
	Material int      // index into Problem.Materials
	Ratio    int64    // units of Material consumed per demand unit, >= 1
	Aux      []AuxUse // auxiliary consumption per demand unit
}

// Demand is one order line: Quantity units are required per complete order.
type Demand struct {
	Name     string
	Quantity int64 // units required per complete order, >= 1
	Options  []Option
}

// Problem is a full recipe-plus-inventory instance.
type Problem struct {
	Materials []Material
	Demands   []Demand
}

// Validate reports whether the problem is well formed.
func (p *Problem) Validate() error {
	if len(p.Demands) == 0 {
		return errors.New("gsb: problem has no demands")
	}
	if len(p.Demands) > MaxDemands {
		return fmt.Errorf("gsb: %d demands exceed the limit of %d", len(p.Demands), MaxDemands)
	}
	for i, m := range p.Materials {
		if m.Stock < 0 {
			return fmt.Errorf("gsb: material %d has negative stock %d", i, m.Stock)
		}
		if m.UnitCost < 0 {
			return fmt.Errorf("gsb: material %d has negative unit cost %d", i, m.UnitCost)
		}
	}
	for d, dem := range p.Demands {
		if dem.Quantity < 1 {
			return fmt.Errorf("gsb: demand %d has quantity %d, want >= 1", d, dem.Quantity)
		}
		for o, opt := range dem.Options {
			if opt.Material < 0 || opt.Material >= len(p.Materials) {
				return fmt.Errorf("gsb: demand %d option %d references material %d, have %d materials", d, o, opt.Material, len(p.Materials))
			}
			if opt.Ratio < 1 {
				return fmt.Errorf("gsb: demand %d option %d has ratio %d, want >= 1", d, o, opt.Ratio)
			}
			for _, a := range opt.Aux {
				if a.Material < 0 || a.Material >= len(p.Materials) {
					return fmt.Errorf("gsb: demand %d option %d references aux material %d, have %d materials", d, o, a.Material, len(p.Materials))
				}
				if a.Amount < 1 {
					return fmt.Errorf("gsb: demand %d option %d has aux amount %d, want >= 1", d, o, a.Amount)
				}
			}
		}
	}
	return nil
}

// Line is one consumption-evidence record: Units demand-units of Demand
// fulfilled via that demand's Option.
type Line struct {
	Demand int
	Option int
	Units  int64
}

// Usage is the per-material consumption evidence.
type Usage struct {
	Material int
	Used     int64
	Stock    int64
}

// Solution is the result of Solve or BruteForce.
type Solution struct {
	CompleteOrders int64  // maximized number of complete orders
	Cost           int64  // minimized total material cost at CompleteOrders
	Lines          []Line // per-demand consumption evidence
	Usage          []Usage
}
