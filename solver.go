package mealsolver

import (
	"fmt"
	"sort"
)

// inf 为内部放松约束时使用的“无穷”整数。
const inf = int(1) << 30

// maxPlansPerDay 防止单日可行方案枚举爆炸。
const maxPlansPerDay = 200000

// bound 为内部区间表示，min/max 可被放松为 ±inf。
type bound struct{ min, max int }

// relaxedConfig 支持按天、按营养素、按方向逐条放松，供冲突分析使用。
type relaxedConfig struct {
	days        int
	mealsPerDay int
	bounds      []map[string]bound
	banned      map[string]bool
	maxRepeat   int
	maxDishes   int
}

func newRelaxed(c Config) relaxedConfig {
	rc := relaxedConfig{
		days:        c.Days,
		mealsPerDay: c.MealsPerDay,
		bounds:      make([]map[string]bound, c.Days),
		banned:      make(map[string]bool, len(c.Allergens)),
		maxRepeat:   c.MaxConsecutiveRepeat,
		maxDishes:   c.MaxDishes,
	}
	for _, a := range c.Allergens {
		rc.banned[a] = true
	}
	for d := 0; d < c.Days; d++ {
		m := make(map[string]bound, len(c.NutrientBounds))
		for n, b := range c.NutrientBounds {
			m[n] = bound{min: b.Min, max: b.Max}
		}
		rc.bounds[d] = m
	}
	return rc
}

// dayPlan 为某一天所有餐位的一种可行分配（餐位无序，按多重集处理）。
type dayPlan struct {
	dishIdx []int
	tierIdx []int
	dishes  []int // 去重后的菜品下标
	totals  map[string]int
	cost    int
}

type solver struct {
	rc        relaxedConfig
	dishes    []Dish // 已过过敏禁用与空档位过滤
	nutrients []string
	maxAdd    map[string]int
	minAdd    map[string]int
	plans     [][]dayPlan // 按天分组，各自按成本升序
	suffixMin []int       // suffixMin[d] = 第 d..days-1 天最低成本之和
	best      int
	bestSeq   []int
	findOne   bool
	found     bool
	err       error
}

func newSolver(rc relaxedConfig, dishes []Dish, findOne bool) *solver {
	eligible := make([]Dish, 0, len(dishes))
	for _, d := range dishes {
		if len(d.Tiers) == 0 {
			continue
		}
		banned := false
		for _, a := range d.Allergens {
			if rc.banned[a] {
				banned = true
				break
			}
		}
		if !banned {
			eligible = append(eligible, d)
		}
	}
	set := map[string]bool{}
	for _, m := range rc.bounds {
		for n := range m {
			set[n] = true
		}
	}
	nutrients := make([]string, 0, len(set))
	for n := range set {
		nutrients = append(nutrients, n)
	}
	sort.Strings(nutrients)
	return &solver{rc: rc, dishes: eligible, nutrients: nutrients, findOne: findOne, best: inf}
}

func (s *solver) run() {
	s.enumerate()
	if s.err != nil {
		return
	}
	s.suffixMin = make([]int, s.rc.days+1)
	for d := s.rc.days - 1; d >= 0; d-- {
		if len(s.plans[d]) == 0 {
			return // 某一天无可行方案，整体无解
		}
		s.suffixMin[d] = s.suffixMin[d+1] + s.plans[d][0].cost
	}
	s.dfs(0, map[int]int{}, map[int]bool{}, 0, make([]int, s.rc.days))
}

// enumerate 为每一天分别枚举满足当日营养区间的所有餐位分配。
// 每日区间独立判断，绝不跨天取平均。
func (s *solver) enumerate() {
	s.maxAdd = map[string]int{}
	s.minAdd = map[string]int{}
	for _, n := range s.nutrients {
		s.minAdd[n] = inf
	}
	for _, d := range s.dishes {
		for _, t := range d.Tiers {
			for _, n := range s.nutrients {
				v := t.Nutrition[n]
				if v > s.maxAdd[n] {
					s.maxAdd[n] = v
				}
				if v < s.minAdd[n] {
					s.minAdd[n] = v
				}
			}
		}
	}
	s.plans = make([][]dayPlan, s.rc.days)
	for day := 0; day < s.rc.days; day++ {
		s.plans[day] = s.enumDay(s.rc.bounds[day])
		if s.err != nil {
			return
		}
	}
}

func (s *solver) enumDay(bounds map[string]bound) []dayPlan {
	m := s.rc.mealsPerDay
	var plans []dayPlan
	dishIdx := make([]int, m)
	tierIdx := make([]int, m)
	totals := make(map[string]int, len(s.nutrients))
	var rec func(slot, start, cost int)
	rec = func(slot, start, cost int) {
		if s.err != nil {
			return
		}
		rem := m - slot
		for _, n := range s.nutrients {
			bb, ok := bounds[n]
			if !ok {
				continue
			}
			t := totals[n]
			if t > bb.max ||
				t+rem*s.maxAdd[n] < bb.min ||
				t+rem*s.minAdd[n] > bb.max {
				return
			}
		}
		if slot == m {
			if len(plans) >= maxPlansPerDay {
				s.err = fmt.Errorf("单日可行方案数超过上限 %d，请收紧营养区间或减少菜品", maxPlansPerDay)
				return
			}
			p := dayPlan{
				dishIdx: append([]int(nil), dishIdx...),
				tierIdx: append([]int(nil), tierIdx...),
				totals:  copyTotals(totals),
				cost:    cost,
			}
			seen := map[int]bool{}
			for _, di := range dishIdx {
				if !seen[di] {
					seen[di] = true
					p.dishes = append(p.dishes, di)
				}
			}
			plans = append(plans, p)
			return
		}
		for di := start; di < len(s.dishes); di++ {
			d := s.dishes[di]
			for ti := range d.Tiers {
				t := d.Tiers[ti]
				dishIdx[slot] = di
				tierIdx[slot] = ti
				for _, n := range s.nutrients {
					totals[n] += t.Nutrition[n]
				}
				rec(slot+1, di, cost+t.Cost)
				for _, n := range s.nutrients {
					totals[n] -= t.Nutrition[n]
				}
			}
		}
	}
	rec(0, 0, 0)
	sort.Slice(plans, func(i, j int) bool { return plans[i].cost < plans[j].cost })
	return plans
}

// dfs 按天做分支定界：下界为已花成本 + 剩余天数各自最低成本之和。
func (s *solver) dfs(day int, prevStreaks map[int]int, used map[int]bool, cost int, seq []int) {
	if s.findOne && s.found {
		return
	}
	if cost+s.suffixMin[day] >= s.best {
		return
	}
	if day == s.rc.days {
		s.best = cost
		s.bestSeq = append([]int(nil), seq...)
		s.found = true
		return
	}
	for i := range s.plans[day] {
		if s.findOne && s.found {
			return
		}
		p := &s.plans[day][i]
		if cost+p.cost+s.suffixMin[day+1] >= s.best {
			break // 当日方案按成本升序，后续只会更贵
		}
		ok := true
		streaks := make(map[int]int, len(p.dishes))
		for _, di := range p.dishes {
			st := prevStreaks[di] + 1
			if st > s.rc.maxRepeat {
				ok = false
				break
			}
			streaks[di] = st
		}
		if !ok {
			continue
		}
		var added []int
		for _, di := range p.dishes {
			if !used[di] {
				used[di] = true
				added = append(added, di)
			}
		}
		if len(used) <= s.rc.maxDishes {
			seq[day] = i
			s.dfs(day+1, streaks, used, cost+p.cost, seq)
		}
		for _, di := range added {
			delete(used, di)
		}
	}
}

func (s *solver) result() Result {
	res := Result{Status: StatusOptimal, TotalCost: s.best}
	for day, pi := range s.bestSeq {
		p := s.plans[day][pi]
		for slot := range p.dishIdx {
			d := s.dishes[p.dishIdx[slot]]
			res.Choices = append(res.Choices, MealChoice{
				Day:      day + 1,
				Meal:     slot + 1,
				DishID:   d.ID,
				DishName: d.Name,
				Tier:     d.Tiers[p.tierIdx[slot]].Name,
			})
		}
		res.DailyTotals = append(res.DailyTotals, p.totals)
	}
	return res
}

func copyTotals(t map[string]int) map[string]int {
	cp := make(map[string]int, len(t))
	for k, v := range t {
		cp[k] = v
	}
	return cp
}

func solveRelaxed(rc relaxedConfig, dishes []Dish, findOne bool) (*solver, error) {
	s := newSolver(rc, dishes, findOne)
	s.run()
	if s.err != nil {
		return s, s.err
	}
	return s, nil
}

// feasible 为冲突分析使用的可行性预言机。
func feasible(rc relaxedConfig, dishes []Dish) bool {
	s, err := solveRelaxed(rc, dishes, true)
	return err == nil && s.found
}

// Solve 求解五日餐单：每日营养落入整数区间、过敏禁用、连续重复与菜品总数受限，
// 目标为总成本最小。无解时返回极小冲突约束子集。
func Solve(cfg Config, dishes []Dish) Result {
	c := cfg.withDefaults()
	if err := validate(c, dishes); err != nil {
		return Result{Status: StatusError, Err: err.Error()}
	}
	s, err := solveRelaxed(newRelaxed(c), dishes, false)
	if err != nil {
		return Result{Status: StatusError, Err: err.Error()}
	}
	if !s.found {
		return Result{Status: StatusInfeasible, Conflicts: FindConflicts(c, dishes)}
	}
	return s.result()
}

func validate(c Config, dishes []Dish) error {
	if c.Days <= 0 || c.MealsPerDay <= 0 {
		return fmt.Errorf("天数与每日餐位数必须为正整数")
	}
	if c.MaxDishes < 1 {
		return fmt.Errorf("菜品总数上限必须至少为 1")
	}
	seen := map[string]bool{}
	for _, d := range dishes {
		if d.ID == "" {
			return fmt.Errorf("菜品 ID 不能为空")
		}
		if seen[d.ID] {
			return fmt.Errorf("菜品 ID 重复: %s", d.ID)
		}
		seen[d.ID] = true
		for _, t := range d.Tiers {
			if t.Cost < 0 {
				return fmt.Errorf("菜品 %s 档位 %s 成本不能为负", d.ID, t.Name)
			}
		}
	}
	for n, b := range c.NutrientBounds {
		if b.Min > b.Max {
			return fmt.Errorf("营养素 %s 区间无效: min(%d) > max(%d)", n, b.Min, b.Max)
		}
	}
	return nil
}
