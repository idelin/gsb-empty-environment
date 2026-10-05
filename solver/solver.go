package solver

import (
	"fmt"
	"math"
	"sort"
)

const eps = 1e-9

// choice assigns Count servings of one Tier of a dish within a single day.
type choice struct {
	dish  int
	count int
	tier  float64
}

// dayOption is one feasible menu for a single day.
type dayOption struct {
	choices  []choice
	totals   []float64
	cost     float64
	servings int
}

// core is the internal solver state for one (possibly relaxed) config.
type core struct {
	cfg    Config
	names  []string // sorted nutrient names
	mins   []float64
	maxs   []float64
	dishes []Dish // allergy-filtered
	opts   []dayOption
}

func newCore(cfg Config) (core, error) {
	cfg = cfg.withDefaults()
	c := core{cfg: cfg}
	for name := range cfg.Nutrients {
		c.names = append(c.names, name)
	}
	sort.Strings(c.names)
	for _, name := range c.names {
		r := cfg.Nutrients[name]
		if r.Min < 0 || r.Max < r.Min {
			return c, fmt.Errorf("mealsolver: invalid range for nutrient %q: [%v, %v]", name, r.Min, r.Max)
		}
		c.mins = append(c.mins, r.Min)
		c.maxs = append(c.maxs, r.Max)
	}
	banned := map[string]bool{}
	for _, a := range cfg.BannedAllergens {
		banned[a] = true
	}
	for _, d := range cfg.Dishes {
		if d.Name == "" {
			return c, fmt.Errorf("mealsolver: dish with empty name")
		}
		if d.UnitCost < 0 {
			return c, fmt.Errorf("mealsolver: dish %q has negative cost", d.Name)
		}
		hit := false
		for _, a := range d.Allergens {
			if banned[a] {
				hit = true
				break
			}
		}
		if hit {
			continue // allergy ban: dish is forbidden
		}
		if len(d.Tiers) == 0 {
			d.Tiers = []float64{1}
		}
		for _, t := range d.Tiers {
			if t <= 0 {
				return c, fmt.Errorf("mealsolver: dish %q has non-positive portion tier %v", d.Name, t)
			}
		}
		c.dishes = append(c.dishes, d)
	}
	if len(c.dishes) == 0 {
		return c, fmt.Errorf("mealsolver: no dishes available after allergy filtering")
	}
	return c, nil
}

// generate enumerates every feasible single-day menu, sorted by cost.
func (c *core) generate() error {
	n := len(c.dishes)
	m := len(c.names)
	cur := dayOption{totals: make([]float64, m)}
	var opts []dayOption

	var rec func(i int) error
	rec = func(i int) error {
		if len(opts) > c.cfg.MaxDayOptions {
			return fmt.Errorf("mealsolver: more than %d daily combinations; tighten nutrient ranges or reduce dishes/tiers", c.cfg.MaxDayOptions)
		}
		if i == n {
			if cur.servings == 0 {
				return nil
			}
			for k := 0; k < m; k++ {
				if cur.totals[k] < c.mins[k]-eps {
					return nil // daily lower bound violated
				}
			}
			opts = append(opts, dayOption{
				choices:  append([]choice(nil), cur.choices...),
				totals:   append([]float64(nil), cur.totals...),
				cost:     cur.cost,
				servings: cur.servings,
			})
			return nil
		}
		if err := rec(i + 1); err != nil {
			return err
		}
		d := c.dishes[i]
		for cnt := 1; cnt <= c.cfg.MaxPerDishPerDay; cnt++ {
			for _, tier := range d.Tiers {
				if c.cfg.MaxServingsPerDay > 0 && cur.servings+cnt > c.cfg.MaxServingsPerDay {
					continue
				}
				add := make([]float64, m)
				over := false
				for k := 0; k < m; k++ {
					add[k] = float64(cnt) * tier * d.Nutrition[c.names[k]]
					if cur.totals[k]+add[k] > c.maxs[k]+eps {
						over = true // daily upper bound violated
						break
					}
				}
				if over {
					continue
				}
				for k := 0; k < m; k++ {
					cur.totals[k] += add[k]
				}
				cur.choices = append(cur.choices, choice{dish: i, count: cnt, tier: tier})
				cur.cost += float64(cnt) * tier * d.UnitCost
				cur.servings += cnt
				if err := rec(i + 1); err != nil {
					return err
				}
				cur.servings -= cnt
				cur.cost -= float64(cnt) * tier * d.UnitCost
				cur.choices = cur.choices[:len(cur.choices)-1]
				for k := 0; k < m; k++ {
					cur.totals[k] -= add[k]
				}
			}
		}
		return nil
	}
	if err := rec(0); err != nil {
		return err
	}
	sort.SliceStable(opts, func(a, b int) bool { return opts[a].cost < opts[b].cost })
	c.opts = opts
	return nil
}

// search runs branch & bound across days. If feasOnly is set it stops at the
// first feasible plan; otherwise it returns a minimum-cost plan.
func (c *core) search(feasOnly bool) ([]int, bool) {
	days := c.cfg.Days
	if len(c.opts) == 0 {
		return nil, false
	}
	minDay := c.opts[0].cost

	best := math.Inf(1)
	var bestSel []int
	sel := make([]int, 0, days)

	lastDay := make([]int, len(c.dishes))
	runLen := make([]int, len(c.dishes))
	used := make([]int, len(c.dishes))
	for i := range lastDay {
		lastDay[i] = -1 << 30
	}
	totalServings := 0
	var cost float64

	type undo struct {
		dish                     int
		prevLast, prevRun, count int
	}

	var dfs func(day int) bool // returns true to abort early (feasOnly hit)
	dfs = func(day int) bool {
		if day == days {
			if feasOnly {
				return true
			}
			if cost < best-eps {
				best = cost
				bestSel = append([]int(nil), sel...)
			}
			return false
		}
		if cost+float64(days-day)*minDay >= best-eps {
			return false
		}
		for oi := range c.opts {
			o := &c.opts[oi]
			if cost+o.cost+float64(days-day-1)*minDay >= best-eps {
				break // options sorted by cost: no later option can help
			}
			if c.cfg.MaxTotalServings > 0 && totalServings+o.servings > c.cfg.MaxTotalServings {
				continue
			}
			ok := true
			for _, ch := range o.choices {
				run := 1
				if lastDay[ch.dish] == day-1 {
					run = runLen[ch.dish] + 1
				}
				if c.cfg.MaxConsecutiveDays > 0 && run > c.cfg.MaxConsecutiveDays {
					ok = false
					break
				}
				if mt := c.dishes[ch.dish].MaxTimes; mt > 0 && used[ch.dish]+ch.count > mt {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			touched := make([]undo, 0, len(o.choices))
			for _, ch := range o.choices {
				touched = append(touched, undo{ch.dish, lastDay[ch.dish], runLen[ch.dish], ch.count})
				if lastDay[ch.dish] == day-1 {
					runLen[ch.dish]++
				} else {
					runLen[ch.dish] = 1
				}
				lastDay[ch.dish] = day
				used[ch.dish] += ch.count
			}
			totalServings += o.servings
			cost += o.cost
			sel = append(sel, oi)
			stop := dfs(day + 1)
			sel = sel[:len(sel)-1]
			cost -= o.cost
			totalServings -= o.servings
			for i := len(touched) - 1; i >= 0; i-- {
				u := touched[i]
				lastDay[u.dish] = u.prevLast
				runLen[u.dish] = u.prevRun
				used[u.dish] -= u.count
			}
			if stop {
				return true
			}
		}
		return false
	}

	if feasOnly {
		if dfs(0) {
			return sel, true
		}
		return nil, false
	}
	dfs(0)
	return bestSel, bestSel != nil
}

func (c *core) buildPlan(sel []int) *Plan {
	plan := &Plan{Days: make([]DayPlan, len(sel))}
	for day, oi := range sel {
		o := c.opts[oi]
		dp := DayPlan{Totals: map[string]float64{}, Cost: o.cost}
		for _, ch := range o.choices {
			d := c.dishes[ch.dish]
			dp.Servings = append(dp.Servings, Serving{
				Dish:  d.Name,
				Count: ch.count,
				Tier:  ch.tier,
				Cost:  float64(ch.count) * ch.tier * d.UnitCost,
			})
		}
		for k, name := range c.names {
			dp.Totals[name] = o.totals[k]
		}
		plan.Days[day] = dp
		plan.TotalCost += o.cost
	}
	return plan
}

func solveInternal(cfg Config, feasOnly bool) (*Plan, bool, error) {
	c, err := newCore(cfg)
	if err != nil {
		return nil, false, err
	}
	if err := c.generate(); err != nil {
		return nil, false, err
	}
	sel, ok := c.search(feasOnly)
	if !ok {
		return nil, false, nil
	}
	if feasOnly {
		return nil, true, nil
	}
	return c.buildPlan(sel), true, nil
}

// Solve returns a minimum-cost plan satisfying every daily constraint.
// It returns ErrInfeasible when no such plan exists.
func Solve(cfg Config) (*Plan, error) {
	plan, ok, err := solveInternal(cfg, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInfeasible
	}
	return plan, nil
}

// Feasible reports whether any plan satisfies every constraint.
func Feasible(cfg Config) (bool, error) {
	_, ok, err := solveInternal(cfg, true)
	return ok, err
}
