package solver

import (
	"math"
	"testing"
)

func baseConfig() Config {
	return Config{
		Days: 5,
		Nutrients: map[string]NutrientRange{
			"protein": {Min: 50, Max: 120},
			"carbs":   {Min: 60, Max: 150},
		},
		Dishes: []Dish{
			{Name: "chicken", UnitCost: 12, Tiers: []float64{0.5, 1, 1.5},
				Nutrition: map[string]float64{"protein": 25, "carbs": 8}},
			{Name: "pasta", UnitCost: 18, Tiers: []float64{0.5, 1},
				Nutrition: map[string]float64{"protein": 22, "carbs": 45}},
			{Name: "rice", UnitCost: 8, Tiers: []float64{0.5, 1, 1.5},
				Nutrition: map[string]float64{"protein": 8, "carbs": 40}},
			{Name: "peanut-sandwich", UnitCost: 1, Allergens: []string{"peanut"}, Tiers: []float64{1},
				Nutrition: map[string]float64{"protein": 60, "carbs": 80}},
		},
		BannedAllergens:    []string{"peanut"},
		MaxConsecutiveDays: 2,
		MaxTotalServings:   20,
	}
}

func checkPlan(t *testing.T, cfg Config, p *Plan) {
	t.Helper()
	cfg = cfg.withDefaults()
	if len(p.Days) != cfg.Days {
		t.Fatalf("plan has %d days, want %d", len(p.Days), cfg.Days)
	}
	totalServings := 0
	lastDay := map[string]int{}
	run := map[string]int{}
	for d, day := range p.Days {
		for name, r := range cfg.Nutrients {
			got := day.Totals[name]
			if got < r.Min-eps || got > r.Max+eps {
				t.Errorf("day %d nutrient %s = %v, outside daily range [%v, %v]", d+1, name, got, r.Min, r.Max)
			}
		}
		for _, s := range day.Servings {
			totalServings += s.Count
			for _, dish := range cfg.Dishes {
				if dish.Name != s.Dish {
					continue
				}
				for _, a := range dish.Allergens {
					for _, banned := range cfg.BannedAllergens {
						if a == banned {
							t.Errorf("day %d uses banned-allergen dish %q", d+1, s.Dish)
						}
					}
				}
			}
			if lastDay[s.Dish] == d-1 {
				run[s.Dish]++
			} else {
				run[s.Dish] = 1
			}
			lastDay[s.Dish] = d
			if cfg.MaxConsecutiveDays > 0 && run[s.Dish] > cfg.MaxConsecutiveDays {
				t.Errorf("dish %q appears %d consecutive days, limit %d", s.Dish, run[s.Dish], cfg.MaxConsecutiveDays)
			}
		}
	}
	if cfg.MaxTotalServings > 0 && totalServings > cfg.MaxTotalServings {
		t.Errorf("total servings %d exceeds limit %d", totalServings, cfg.MaxTotalServings)
	}
}

func TestSolveFeasible(t *testing.T) {
	cfg := baseConfig()
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	checkPlan(t, cfg, plan)
	if plan.TotalCost <= 0 {
		t.Errorf("non-positive total cost %v", plan.TotalCost)
	}
}

func TestOptimalityWithRepeatLimit(t *testing.T) {
	cfg := Config{
		Days:               3,
		Nutrients:          map[string]NutrientRange{"protein": {Min: 10, Max: 10}},
		MaxConsecutiveDays: 1,
		Dishes: []Dish{
			{Name: "cheap", UnitCost: 1, Nutrition: map[string]float64{"protein": 10}},
			{Name: "dear", UnitCost: 5, Nutrition: map[string]float64{"protein": 10}},
		},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// Must alternate cheap/dear/cheap: 1 + 5 + 1 = 7.
	if math.Abs(plan.TotalCost-7) > eps {
		t.Fatalf("total cost = %v, want 7", plan.TotalCost)
	}
	checkPlan(t, cfg, plan)
}

func TestPortionTiers(t *testing.T) {
	cfg := Config{
		Days:      1,
		Nutrients: map[string]NutrientRange{"protein": {Min: 15, Max: 15}},
		Dishes: []Dish{
			{Name: "tiered", UnitCost: 4, Tiers: []float64{1, 1.5},
				Nutrition: map[string]float64{"protein": 10}},
			{Name: "flat", UnitCost: 100, Tiers: []float64{1},
				Nutrition: map[string]float64{"protein": 15}},
		},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// 1.5x tier of "tiered" = 15 protein at cost 6, far cheaper than "flat".
	if math.Abs(plan.TotalCost-6) > eps {
		t.Fatalf("total cost = %v, want 6", plan.TotalCost)
	}
	s := plan.Days[0].Servings[0]
	if s.Dish != "tiered" || s.Tier != 1.5 {
		t.Fatalf("got %+v, want tiered x1 at tier 1.5", s)
	}
}

func TestDailyNotAverage(t *testing.T) {
	// Averaging over days would pass (mean 50 within [50,60]) but no single
	// day can satisfy the range, so the instance must be infeasible.
	cfg := Config{
		Days:      2,
		Nutrients: map[string]NutrientRange{"protein": {Min: 50, Max: 60}},
		Dishes: []Dish{
			{Name: "heavy", UnitCost: 1, Nutrition: map[string]float64{"protein": 100}},
			{Name: "light", UnitCost: 1, Nutrition: map[string]float64{"protein": 0}},
		},
	}
	if _, err := Solve(cfg); err != ErrInfeasible {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
}

func TestAllergyBanInfeasible(t *testing.T) {
	cfg := Config{
		Days:      1,
		Nutrients: map[string]NutrientRange{"protein": {Min: 10, Max: 50}},
		Dishes: []Dish{
			{Name: "shrimp", UnitCost: 1, Allergens: []string{"shellfish"},
				Nutrition: map[string]float64{"protein": 20}},
		},
		BannedAllergens: []string{"shellfish"},
	}
	if _, err := Solve(cfg); err == nil {
		t.Fatal("expected error, got plan")
	}
}

func TestConflictSubsetSingle(t *testing.T) {
	// Daily protein ceiling is 2 servings * 30 = 60 < 200: only the protein
	// lower bound conflicts; every other constraint is removable.
	cfg := Config{
		Days:               5,
		Nutrients:          map[string]NutrientRange{"protein": {Min: 200, Max: 400}},
		BannedAllergens:    []string{"soy"},
		MaxConsecutiveDays: 2,
		MaxTotalServings:   20,
		Dishes: []Dish{
			{Name: "chicken", UnitCost: 10, Nutrition: map[string]float64{"protein": 30}},
			{Name: "tofu", UnitCost: 4, Allergens: []string{"soy"},
				Nutrition: map[string]float64{"protein": 25}},
			{Name: "rice", UnitCost: 2, Nutrition: map[string]float64{"protein": 4}},
		},
	}
	if _, err := Solve(cfg); err != ErrInfeasible {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	subset, err := FindConflictSubset(cfg)
	if err != nil {
		t.Fatalf("FindConflictSubset: %v", err)
	}
	if len(subset) != 1 {
		t.Fatalf("subset = %v, want exactly 1 constraint", subset)
	}
}

func TestConflictSubsetPair(t *testing.T) {
	// Per-day protein ceiling is 2 servings/day * 40 = 80 < 100. Relaxing
	// either the protein lower bound or the per-day serving cap alone
	// restores feasibility, so both must appear in the conflict subset.
	cfg := Config{
		Days:              2,
		Nutrients:         map[string]NutrientRange{"protein": {Min: 100, Max: 1000}},
		MaxServingsPerDay: 2,
		Dishes: []Dish{
			{Name: "beef", UnitCost: 9, Nutrition: map[string]float64{"protein": 40}},
			{Name: "chicken", UnitCost: 5, Nutrition: map[string]float64{"protein": 30}},
		},
	}
	subset, err := FindConflictSubset(cfg)
	if err != nil {
		t.Fatalf("FindConflictSubset: %v", err)
	}
	if len(subset) != 2 {
		t.Fatalf("subset = %v, want exactly 2 constraints", subset)
	}

	// The subset alone (all other constraints relaxed) must stay infeasible.
	keep := cfg
	keep.Nutrients = map[string]NutrientRange{"protein": {Min: 100, Max: math.Inf(1)}}
	if ok, err := Feasible(keep); err != nil || ok {
		t.Fatalf("subset-only config: ok=%v err=%v, want infeasible", ok, err)
	}
	// Minimality: additionally relaxing either member restores feasibility.
	relaxMin := keep
	relaxMin.Nutrients = map[string]NutrientRange{"protein": {Min: 0, Max: math.Inf(1)}}
	if ok, err := Feasible(relaxMin); err != nil || !ok {
		t.Fatalf("relaxing protein lower bound: ok=%v err=%v, want feasible", ok, err)
	}
	relaxCap := keep
	relaxCap.MaxServingsPerDay = 0
	if ok, err := Feasible(relaxCap); err != nil || !ok {
		t.Fatalf("relaxing per-day cap: ok=%v err=%v, want feasible", ok, err)
	}
}

func TestConflictSubsetOnFeasibleConfig(t *testing.T) {
	if _, err := FindConflictSubset(baseConfig()); err == nil {
		t.Fatal("expected error for feasible config")
	}
}
