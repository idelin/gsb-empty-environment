// Package solver implements a 5-day meal plan solver.
//
// The solver enforces, for every single day (never on average):
//   - daily nutrition totals within integer [min, max] ranges,
//   - allergy bans (dishes containing banned allergens are forbidden),
//   - a limit on how many consecutive days the same dish may appear,
//   - a cap on the total number of servings across the whole plan (<= 20),
//
// and minimizes total cost. Each dish may declare several portion tiers.
package solver

import "errors"

// ErrInfeasible is returned by Solve when no plan satisfies all constraints.
// Use FindConflictSubset to obtain a minimal conflicting constraint subset.
var ErrInfeasible = errors.New("mealsolver: no feasible meal plan")

// NutrientRange is the daily integer range [Min, Max] for one nutrient.
type NutrientRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Dish describes one cookable dish.
type Dish struct {
	Name      string             `json:"name"`
	Allergens []string           `json:"allergens,omitempty"`
	UnitCost  float64            `json:"unitCost"`           // cost of one 1.0x portion
	Nutrition map[string]float64 `json:"nutrition"`          // nutrients per 1.0x portion
	Tiers     []float64          `json:"tiers,omitempty"`    // allowed portion multipliers, default [1.0]
	MaxTimes  int                `json:"maxTimes,omitempty"` // max appearances in the whole plan, 0 = unlimited
}

// Config is the full problem definition.
type Config struct {
	Days               int                      `json:"days"`      // default 5
	Nutrients          map[string]NutrientRange `json:"nutrients"` // daily integer ranges
	Dishes             []Dish                   `json:"dishes"`
	BannedAllergens    []string                 `json:"bannedAllergens,omitempty"`
	MaxConsecutiveDays int                      `json:"maxConsecutiveDays,omitempty"` // repeat limit, 0 = unlimited
	MaxServingsPerDay  int                      `json:"maxServingsPerDay,omitempty"`  // 0 = unlimited
	MaxTotalServings   int                      `json:"maxTotalServings,omitempty"`   // default 20
	MaxPerDishPerDay   int                      `json:"maxPerDishPerDay,omitempty"`   // default 2
	MaxDayOptions      int                      `json:"maxDayOptions,omitempty"`      // solver safeguard, default 200000
}

func (c Config) withDefaults() Config {
	if c.Days <= 0 {
		c.Days = 5
	}
	if c.MaxTotalServings <= 0 {
		c.MaxTotalServings = 20
	}
	if c.MaxPerDishPerDay <= 0 {
		c.MaxPerDishPerDay = 2
	}
	if c.MaxDayOptions <= 0 {
		c.MaxDayOptions = 200000
	}
	return c
}

// Serving is one plated dish: Count portions of size Tier of Dish.
type Serving struct {
	Dish  string  `json:"dish"`
	Count int     `json:"count"`
	Tier  float64 `json:"tier"`
	Cost  float64 `json:"cost"`
}

// DayPlan is the menu of a single day.
type DayPlan struct {
	Servings []Serving          `json:"servings"`
	Totals   map[string]float64 `json:"totals"`
	Cost     float64            `json:"cost"`
}

// Plan is a full multi-day meal plan.
type Plan struct {
	Days      []DayPlan `json:"days"`
	TotalCost float64   `json:"totalCost"`
}
