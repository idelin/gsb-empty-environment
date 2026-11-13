package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Analyze exhaustively searches insertion of zero through MaxPlugins of the
// supplied plugin IDs into globalOrder. It returns every minimum-size plan
// that makes relocation bind an untrusted export when RequireTrusted is true.
//
// Analyze has no layout budget. Use AnalyzeBudget when a bound is required.
func Analyze(modules []Module, globalOrder, plugins []string, relocation Relocation) (Audit, error) {
	return AnalyzeBudget(modules, globalOrder, plugins, relocation, UnlimitedBudget)
}

// AnalyzeBudget is Analyze with a limit on the number of distinct final global
// orders examined. A budget of zero examines nothing. A negative budget is
// unlimited.
//
// The search is level-by-level: zero plugins, then one, then two, then three.
// If the budget expires before a level and all later levels are exhausted, the
// result is AuditUnknown. This is deliberate: returning a partial plan list
// could misstate either the minimum or the set of equal-size plans.
func AnalyzeBudget(
	modules []Module,
	globalOrder, plugins []string,
	relocation Relocation,
	budget int64,
) (Audit, error) {
	c, err := newCatalog(modules, globalOrder)
	if err != nil {
		return Audit{}, err
	}
	if err := c.validateRelocation(relocation); err != nil {
		return Audit{}, err
	}
	pluginIDs, pluginSet, err := c.validatePlugins(globalOrder, plugins)
	if err != nil {
		return Audit{}, err
	}

	audit := Audit{
		Status:     AuditImpossible,
		MinPlugins: -1,
		Budget:     budget,
		Relocation: relocation,
	}
	if !relocation.RequireTrusted {
		return audit, nil
	}

	maxInsertions := len(pluginIDs)
	if maxInsertions > MaxPlugins {
		maxInsertions = MaxPlugins
	}
	if remaining := MaxModules - len(globalOrder); remaining < maxInsertions {
		maxInsertions = remaining
	}
	if maxInsertions < 0 {
		maxInsertions = 0
	}

	state := searchState{
		catalog:    c,
		relocation: relocation,
		plugins:    pluginIDs,
		pluginSet:  pluginSet,
		budget:     budget,
		audit:      &audit,
	}

	for count := 0; count <= maxInsertions; count++ {
		state.vulnerable = nil
		state.enumerateLevel(globalOrder, count)
		if state.exhausted {
			audit.Status = AuditUnknown
			audit.MinPlugins = -1
			audit.Plans = nil
			audit.TotalPlans = 0
			audit.Truncated = false
			return audit, nil
		}
		if len(state.vulnerable) == 0 {
			continue
		}

		sortPlans(state.vulnerable)
		audit.Status = AuditFound
		audit.MinPlugins = count
		audit.TotalPlans = len(state.vulnerable)
		audit.Truncated = len(state.vulnerable) > MaxReportedPlans
		if audit.Truncated {
			state.vulnerable = state.vulnerable[:MaxReportedPlans]
		}
		audit.Plans = state.vulnerable
		return audit, nil
	}
	return audit, nil
}

func (c *catalog) validatePlugins(globalOrder, plugins []string) ([]string, map[string]bool, error) {
	if len(plugins) > MaxPlugins {
		return nil, nil, invalidf("%d plugins supplied; maximum is %d", len(plugins), MaxPlugins)
	}
	inGlobal := make(map[string]bool, len(globalOrder))
	for _, id := range globalOrder {
		inGlobal[id] = true
	}

	seen := make(map[string]bool, len(plugins))
	ids := make([]string, 0, len(plugins))
	for i, id := range plugins {
		if _, exists := c.modules[id]; !exists {
			return nil, nil, invalidf("plugin at index %d references unknown module %q", i, id)
		}
		if seen[id] {
			return nil, nil, invalidf("duplicate plugin ID %q", id)
		}
		if inGlobal[id] {
			return nil, nil, invalidf("plugin %q is already in the global order", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, seen, nil
}

type searchState struct {
	catalog    *catalog
	relocation Relocation
	plugins    []string
	pluginSet  map[string]bool
	budget     int64
	examined   int64
	exhausted  bool

	vulnerable []Plan
	audit      *Audit
}

func (s *searchState) enumerateLevel(base []string, count int) {
	if count == 0 {
		s.visit(base)
		return
	}

	for _, subset := range stringCombinations(s.plugins, count) {
		positions := positionCombinations(len(base)+count, count)
		for _, slots := range positions {
			for _, order := range stringPermutations(subset) {
				finalOrder := make([]string, len(base)+count)
				pluginAt := make(map[int]string, count)
				for i, position := range slots {
					pluginAt[position] = order[i]
				}
				next := 0
				for i := range finalOrder {
					if plugin, exists := pluginAt[i]; exists {
						finalOrder[i] = plugin
						continue
					}
					finalOrder[i] = base[next]
					next++
				}
				if !s.visit(finalOrder) {
					return
				}
			}
		}
	}
}

func (s *searchState) visit(order []string) bool {
	if s.budget >= 0 && s.examined >= s.budget {
		s.exhausted = true
		return false
	}
	s.examined++
	s.audit.Examined = s.examined

	binding := s.catalog.resolve(order, s.relocation)
	if !binding.Found || binding.TargetTrusted {
		return true
	}

	plan := Plan{
		Insertions: pluginInsertions(order, s.pluginSet),
		FinalOrder: append([]string(nil), order...),
		Binding:    binding,
	}
	s.vulnerable = append(s.vulnerable, plan)
	return true
}

func pluginInsertions(order []string, plugins map[string]bool) []Insertion {
	insertions := make([]Insertion, 0, len(plugins))
	for position, id := range order {
		if plugins[id] {
			insertions = append(insertions, Insertion{ModuleID: id, Position: position})
		}
	}
	sort.Slice(insertions, func(i, j int) bool {
		if insertions[i].ModuleID != insertions[j].ModuleID {
			return insertions[i].ModuleID < insertions[j].ModuleID
		}
		return insertions[i].Position < insertions[j].Position
	})
	return insertions
}

func sortPlans(plans []Plan) {
	sort.SliceStable(plans, func(i, j int) bool {
		left, right := plans[i].Insertions, plans[j].Insertions
		for k := 0; k < len(left) && k < len(right); k++ {
			if left[k].ModuleID != right[k].ModuleID {
				return left[k].ModuleID < right[k].ModuleID
			}
			if left[k].Position != right[k].Position {
				return left[k].Position < right[k].Position
			}
		}
		return len(left) < len(right)
	})
}

func stringCombinations(items []string, count int) [][]string {
	if count == 0 {
		return [][]string{{}}
	}
	var result [][]string
	var walk func(start int, current []string)
	walk = func(start int, current []string) {
		if len(current) == count {
			result = append(result, append([]string(nil), current...))
			return
		}
		remaining := count - len(current)
		for i := start; i <= len(items)-remaining; i++ {
			walk(i+1, append(current, items[i]))
		}
	}
	walk(0, nil)
	return result
}

func stringPermutations(items []string) [][]string {
	if len(items) == 0 {
		return [][]string{{}}
	}
	var result [][]string
	var walk func(current []string, used []bool)
	walk = func(current []string, used []bool) {
		if len(current) == len(items) {
			result = append(result, append([]string(nil), current...))
			return
		}
		for i := range items {
			if used[i] {
				continue
			}
			used[i] = true
			walk(append(current, items[i]), used)
			used[i] = false
		}
	}
	walk(nil, make([]bool, len(items)))
	return result
}

func positionCombinations(total, count int) [][]int {
	var result [][]int
	current := make([]int, count)
	var walk func(start, depth int)
	walk = func(start, depth int) {
		if depth == count {
			result = append(result, append([]int(nil), current...))
			return
		}
		for position := start; position <= total-(count-depth); position++ {
			current[depth] = position
			walk(position+1, depth+1)
		}
	}
	walk(0, 0)
	return result
}

// Report renders the audit status, plan count, truncation flag, and each
// included plan's complete lookup.
func (a Audit) Report() string {
	var out strings.Builder
	fmt.Fprintf(&out, "状态：%s\n", a.Status)
	fmt.Fprintf(&out, "重定位：%s %s@%s\n", a.Relocation.Origin, a.Relocation.Name, a.Relocation.Version)
	fmt.Fprintf(&out, "已检查布局：%d", a.Examined)
	if a.Budget >= 0 {
		fmt.Fprintf(&out, "/%d", a.Budget)
	}
	out.WriteByte('\n')

	switch a.Status {
	case AuditUnknown:
		out.WriteString("预算耗尽，返回未知；未输出可能不完整的方案集合。\n")
		return out.String()
	case AuditImpossible:
		out.WriteString("穷举完成：不存在使可信要求绑定到不可信导出的插件方案。\n")
		return out.String()
	}

	fmt.Fprintf(&out, "最少插件数：%d\n", a.MinPlugins)
	fmt.Fprintf(&out, "等长方案总数：%d\n", a.TotalPlans)
	fmt.Fprintf(&out, "已输出方案数：%d\n", len(a.Plans))
	truncated := "否"
	if a.Truncated {
		truncated = fmt.Sprintf("是（仅输出按插件ID及位置排序的前 %d 个）", MaxReportedPlans)
	}
	fmt.Fprintf(&out, "是否截断：%s\n", truncated)

	for i, plan := range a.Plans {
		fmt.Fprintf(&out, "方案 %d：\n", i+1)
		if len(plan.Insertions) == 0 {
			out.WriteString("  插入：无\n")
		} else {
			parts := make([]string, 0, len(plan.Insertions))
			for _, insertion := range plan.Insertions {
				parts = append(parts, fmt.Sprintf("%s@%d", insertion.ModuleID, insertion.Position))
			}
			fmt.Fprintf(&out, "  插入：%s\n", strings.Join(parts, ", "))
		}
		fmt.Fprintf(&out, "  最终全局顺序：%s\n", strings.Join(plan.FinalOrder, " -> "))
		for _, line := range strings.Split(strings.TrimSuffix(plan.Binding.Report(), "\n"), "\n") {
			fmt.Fprintf(&out, "  %s\n", line)
		}
	}
	return out.String()
}
