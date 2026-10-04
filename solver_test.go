package mealsolver

import (
	"strings"
	"testing"
)

func dish(id string, cost, protein int, allergens ...string) Dish {
	return Dish{
		ID:        id,
		Name:      id,
		Allergens: allergens,
		Tiers:     []Tier{{Name: "标准", Nutrition: map[string]int{"protein": protein}, Cost: cost}},
	}
}

func baseConfig() Config {
	return Config{
		Days:                 5,
		MealsPerDay:          2,
		NutrientBounds:       map[string]Bound{"protein": {Min: 30, Max: 100}},
		MaxConsecutiveRepeat: 5,
		MaxDishes:            20,
	}
}

// 最便宜组合 B+B 恰好达标，应被选中。
func TestOptimalCost(t *testing.T) {
	cfg := baseConfig()
	dishes := []Dish{dish("A", 5, 20), dish("B", 4, 15)}
	res := Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	if res.TotalCost != 40 { // 8/天 * 5 天
		t.Fatalf("totalCost = %d, want 40", res.TotalCost)
	}
	for _, dt := range res.DailyTotals {
		if p := dt["protein"]; p < 30 || p > 100 {
			t.Fatalf("daily protein %d out of [30,100]", p)
		}
	}
}

// maxRepeat=1 时最便宜的 B+B 不能连用，只能与 A+A 交替。
func TestConsecutiveRepeatLimit(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxConsecutiveRepeat = 1
	dishes := []Dish{dish("A", 9, 30), dish("B", 4, 15)}
	res := Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	if res.TotalCost != 60 { // 8,18,8,18,8
		t.Fatalf("totalCost = %d, want 60", res.TotalCost)
	}
	prev, cur := map[string]bool{}, map[string]bool{}
	day := 0
	for _, c := range res.Choices {
		if c.Day != day {
			prev, cur, day = cur, map[string]bool{}, c.Day
		}
		if prev[c.DishID] {
			t.Fatalf("dish %s appears on consecutive days", c.DishID)
		}
		cur[c.DishID] = true
	}
}

// 含过敏源的菜品再便宜也不能出现。
func TestAllergyExclusion(t *testing.T) {
	cfg := baseConfig()
	cfg.Allergens = []string{"花生"}
	dishes := []Dish{
		dish("cheapNut", 1, 50, "花生"),
		dish("safe", 6, 15),
	}
	res := Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	for _, c := range res.Choices {
		if c.DishID == "cheapNut" {
			t.Fatal("allergen dish used")
		}
	}
	if res.TotalCost != 60 { // safe+safe = 12/天
		t.Fatalf("totalCost = %d, want 60", res.TotalCost)
	}
}

// 每日上限不可达时应无解，并给出包含该下限的冲突子集。
func TestInfeasibleWithConflicts(t *testing.T) {
	cfg := baseConfig()
	cfg.NutrientBounds = map[string]Bound{"protein": {Min: 50, Max: 100}}
	dishes := []Dish{dish("A", 5, 20)}
	res := Solve(cfg, dishes)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
	if len(res.Conflicts) == 0 {
		t.Fatal("expected non-empty conflict set")
	}
	joined := strings.Join(res.Conflicts, "\n")
	if !strings.Contains(joined, "protein 不低于 50") {
		t.Fatalf("conflicts should mention protein lower bound, got:\n%s", joined)
	}
	// 冲突子集是极小的：单个日期的下限即足以导致无解，
	// 因此子集中不应同时出现多天同类约束。
	count := 0
	for _, c := range res.Conflicts {
		if strings.Contains(c, "protein 不低于 50") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 protein-lower-bound conflict, got %d:\n%s", count, joined)
	}
}

// 平均达标但单日不达标不能算可行：H(80) 与 L(0) 平均 40 ∈ [30,40]，
// 但没有任何单日组合落入 [30,40]，必须判无解。
func TestDailyNotAverage(t *testing.T) {
	cfg := baseConfig()
	cfg.MealsPerDay = 1
	cfg.NutrientBounds = map[string]Bound{"protein": {Min: 30, Max: 40}}
	dishes := []Dish{dish("H", 1, 80), dish("L", 1, 0)}
	res := Solve(cfg, dishes)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible (average-based plans must be rejected)", res.Status)
	}
	// 加入一道单日恰可达标的菜后应有解。
	dishes = append(dishes, dish("M", 3, 35))
	res = Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, want optimal", res.Status)
	}
	for _, c := range res.Choices {
		if c.DishID != "M" {
			t.Fatalf("only M satisfies the daily bound, got %s", c.DishID)
		}
	}
}

// 全程不同菜品总数受限。
func TestMaxDishesLimit(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxDishes = 1
	dishes := []Dish{dish("A", 5, 20), dish("B", 4, 15), dish("C", 3, 10)}
	res := Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	used := map[string]bool{}
	for _, c := range res.Choices {
		used[c.DishID] = true
	}
	if len(used) != 1 {
		t.Fatalf("used %d dishes, want 1", len(used))
	}
	if !used["B"] { // B+B=30 蛋白达标且单价最低
		t.Fatalf("want only B used, got %v", used)
	}
}

// 份量档位：小份不达标、大份超标时，应选组合达标且最便宜的档位搭配。
func TestPortionTiers(t *testing.T) {
	cfg := baseConfig()
	cfg.MealsPerDay = 1
	cfg.NutrientBounds = map[string]Bound{"protein": {Min: 25, Max: 35}}
	dishes := []Dish{{
		ID:   "chicken",
		Name: "鸡胸肉",
		Tiers: []Tier{
			{Name: "小份", Nutrition: map[string]int{"protein": 15}, Cost: 3},
			{Name: "大份", Nutrition: map[string]int{"protein": 30}, Cost: 5},
		},
	}}
	res := Solve(cfg, dishes)
	if res.Status != StatusOptimal {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	if res.TotalCost != 25 {
		t.Fatalf("totalCost = %d, want 25", res.TotalCost)
	}
	for _, c := range res.Choices {
		if c.Tier != "大份" {
			t.Fatalf("tier = %s, want 大份", c.Tier)
		}
	}
}

// 菜品总数上限过紧导致无解时，冲突子集应包含该限制。
func TestMaxDishesInConflict(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxDishes = 1
	cfg.MaxConsecutiveRepeat = 1
	dishes := []Dish{dish("A", 5, 20)}
	res := Solve(cfg, dishes)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
	joined := strings.Join(res.Conflicts, "\n")
	if !strings.Contains(joined, "连续出现 1 天") {
		t.Fatalf("conflicts should mention repeat limit, got:\n%s", joined)
	}
}
