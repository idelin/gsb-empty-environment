package solver

import (
	"fmt"
	"math"
	"sort"
)

// relaxSet marks which constraint groups are temporarily relaxed while
// searching for a minimal conflicting subset.
type relaxSet struct {
	noMin    map[string]bool // nutrient -> drop daily lower bound
	noMax    map[string]bool // nutrient -> drop daily upper bound
	unban    map[string]bool // allergen -> allow again
	noRepeat bool            // drop the consecutive-repeat limit
	noTotal  bool            // drop the total servings cap
	noPerDay bool            // drop the per-day servings cap
}

func newRelaxSet() *relaxSet {
	return &relaxSet{
		noMin: map[string]bool{},
		noMax: map[string]bool{},
		unban: map[string]bool{},
	}
}

func (r *relaxSet) apply(cfg Config) Config {
	nut := make(map[string]NutrientRange, len(cfg.Nutrients))
	for k, v := range cfg.Nutrients {
		nut[k] = v
	}
	cfg.Nutrients = nut
	for name := range r.noMin {
		nr := cfg.Nutrients[name]
		nr.Min = 0
		cfg.Nutrients[name] = nr
	}
	for name := range r.noMax {
		nr := cfg.Nutrients[name]
		nr.Max = math.Inf(1)
		cfg.Nutrients[name] = nr
	}
	if len(r.unban) > 0 {
		kept := cfg.BannedAllergens[:0:0]
		for _, a := range cfg.BannedAllergens {
			if !r.unban[a] {
				kept = append(kept, a)
			}
		}
		cfg.BannedAllergens = kept
	}
	if r.noRepeat {
		cfg.MaxConsecutiveDays = 0
	}
	if r.noTotal {
		cfg.MaxTotalServings = 0
	}
	if r.noPerDay {
		cfg.MaxServingsPerDay = 0
	}
	return cfg
}

// constraint is one relaxable requirement of the problem.
type constraint struct {
	desc  string
	relax func(r *relaxSet)
}

func constraintsOf(cfg Config) []constraint {
	var cons []constraint
	names := make([]string, 0, len(cfg.Nutrients))
	for name := range cfg.Nutrients {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		nr := cfg.Nutrients[name]
		if nr.Min > 0 {
			n := name
			cons = append(cons, constraint{
				desc:  fmt.Sprintf("每日营养下限: %s >= %v", n, nr.Min),
				relax: func(r *relaxSet) { r.noMin[n] = true },
			})
		}
		if !math.IsInf(nr.Max, 1) {
			n := name
			cons = append(cons, constraint{
				desc:  fmt.Sprintf("每日营养上限: %s <= %v", n, nr.Max),
				relax: func(r *relaxSet) { r.noMax[n] = true },
			})
		}
	}
	for _, a := range cfg.BannedAllergens {
		a := a
		cons = append(cons, constraint{
			desc:  fmt.Sprintf("过敏禁用: 含过敏原 %q 的菜不可使用", a),
			relax: func(r *relaxSet) { r.unban[a] = true },
		})
	}
	if cfg.MaxConsecutiveDays > 0 {
		cons = append(cons, constraint{
			desc:  fmt.Sprintf("连续重复限制: 同一道菜最多连续出现 %d 天", cfg.MaxConsecutiveDays),
			relax: func(r *relaxSet) { r.noRepeat = true },
		})
	}
	if cfg.MaxTotalServings > 0 {
		cons = append(cons, constraint{
			desc:  fmt.Sprintf("总量限制: 全程菜品总数 <= %d", cfg.MaxTotalServings),
			relax: func(r *relaxSet) { r.noTotal = true },
		})
	}
	if cfg.MaxServingsPerDay > 0 {
		cons = append(cons, constraint{
			desc:  fmt.Sprintf("单日限量: 每日菜品数 <= %d", cfg.MaxServingsPerDay),
			relax: func(r *relaxSet) { r.noPerDay = true },
		})
	}
	return cons
}

// FindConflictSubset analyses an infeasible config and returns a minimal
// subset of constraints that is still infeasible on its own (an irreducible
// infeasible subset, computed by deletion filtering): removing every member
// of the returned subset at once makes the problem feasible, while removing
// all-but-one does not.
//
// It returns an error if the config is actually feasible.
func FindConflictSubset(cfg Config) ([]string, error) {
	cfg = cfg.withDefaults()
	if ok, err := Feasible(cfg); err != nil {
		return nil, err
	} else if ok {
		return nil, fmt.Errorf("mealsolver: config is feasible, no conflict subset exists")
	}

	cons := constraintsOf(cfg)
	removed := make([]bool, len(cons))

	build := func(extra int) Config {
		r := newRelaxSet()
		for i, c := range cons {
			if removed[i] || i == extra {
				c.relax(r)
			}
		}
		return r.apply(cfg)
	}

	// Deletion filtering: a constraint whose removal keeps the problem
	// infeasible is not part of the conflict and is dropped for good.
	for i := range cons {
		ok, err := Feasible(build(i))
		if err != nil {
			return nil, err
		}
		if !ok {
			removed[i] = true
		}
	}

	var subset []string
	for i, c := range cons {
		if !removed[i] {
			subset = append(subset, c.desc)
		}
	}
	return subset, nil
}
