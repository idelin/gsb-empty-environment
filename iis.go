package mealsolver

import (
	"fmt"
	"sort"
)

// FindConflicts 在问题无解时，用删除过滤法提取一个关于约束移除极小的
// 冲突约束子集（IIS）：子集整体不可解，但再去掉其中任意一条即可解。
// 约束粒度为“第 d 天某营养素的上/下限”、单条过敏禁用、连续重复限制、
// 菜品总数上限。若问题实际可解则返回 nil。
func FindConflicts(cfg Config, dishes []Dish) []string {
	c := cfg.withDefaults()
	if err := validate(c, dishes); err != nil {
		return nil
	}
	rc := newRelaxed(c)
	if feasible(rc, dishes) {
		return nil
	}
	if len(dishes) == 0 {
		return []string{"菜品列表为空，没有任何可用菜品"}
	}

	type constraint struct {
		desc  string
		relax func() func() // 放松该约束，返回撤销函数
	}
	var cs []constraint
	nutrients := make([]string, 0, len(c.NutrientBounds))
	for n := range c.NutrientBounds {
		nutrients = append(nutrients, n)
	}
	sort.Strings(nutrients)
	for d := 0; d < c.Days; d++ {
		for _, n := range nutrients {
			day, name := d, n
			cs = append(cs, constraint{
				desc: fmt.Sprintf("第%d天 %s 不低于 %d", day+1, name, c.NutrientBounds[name].Min),
				relax: func() func() {
					old := rc.bounds[day][name]
					rc.bounds[day][name] = bound{min: -inf, max: old.max}
					return func() { rc.bounds[day][name] = old }
				},
			})
			cs = append(cs, constraint{
				desc: fmt.Sprintf("第%d天 %s 不高于 %d", day+1, name, c.NutrientBounds[name].Max),
				relax: func() func() {
					old := rc.bounds[day][name]
					rc.bounds[day][name] = bound{min: old.min, max: inf}
					return func() { rc.bounds[day][name] = old }
				},
			})
		}
	}
	for _, a := range c.Allergens {
		allergen := a
		cs = append(cs, constraint{
			desc: fmt.Sprintf("过敏禁用: %s", allergen),
			relax: func() func() {
				delete(rc.banned, allergen)
				return func() { rc.banned[allergen] = true }
			},
		})
	}
	if c.MaxConsecutiveRepeat < c.Days {
		cs = append(cs, constraint{
			desc: fmt.Sprintf("同一道菜最多连续出现 %d 天", c.MaxConsecutiveRepeat),
			relax: func() func() {
				old := rc.maxRepeat
				rc.maxRepeat = c.Days
				return func() { rc.maxRepeat = old }
			},
		})
	}
	cs = append(cs, constraint{
		desc: fmt.Sprintf("全程不同菜品总数不超过 %d 种", c.MaxDishes),
		relax: func() func() {
			old := rc.maxDishes
			rc.maxDishes = inf
			return func() { rc.maxDishes = old }
		},
	})

	var conflicts []string
	for _, con := range cs {
		undo := con.relax()
		if feasible(rc, dishes) {
			undo() // 去掉该约束即可解，说明它在冲突子集中
			conflicts = append(conflicts, con.desc)
		}
		// 否则保持放松：该约束对当前冲突子集不是必要的
	}
	return conflicts
}
