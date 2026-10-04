package fieldauth

import (
	"fmt"
	"sort"
)

// topoSortDerivers 按 Depends 依赖对派生器拓扑排序（Kahn 算法）。
// 仅考虑派生字段之间的依赖；对普通字段的依赖不影响排序。
// 结果按字段名确定化，保证校验行为可复现。
func topoSortDerivers(derivers []Deriver) ([]Deriver, error) {
	byField := make(map[string]Deriver, len(derivers))
	indegree := make(map[string]int, len(derivers))
	dependents := make(map[string][]string, len(derivers))
	for _, d := range derivers {
		if _, dup := byField[d.Field]; dup {
			return nil, fmt.Errorf("fieldauth: duplicate deriver for %q", d.Field)
		}
		byField[d.Field] = d
		indegree[d.Field] = 0
	}
	for _, d := range derivers {
		for _, dep := range d.Depends {
			if _, isDerived := byField[dep]; !isDerived {
				continue
			}
			if dep == d.Field {
				return nil, fmt.Errorf("fieldauth: deriver %q depends on itself", d.Field)
			}
			indegree[d.Field]++
			dependents[dep] = append(dependents[dep], d.Field)
		}
	}
	var ready []string
	for f, deg := range indegree {
		if deg == 0 {
			ready = append(ready, f)
		}
	}
	sort.Strings(ready)
	var order []Deriver
	for len(ready) > 0 {
		f := ready[0]
		ready = ready[1:]
		order = append(order, byField[f])
		next := append([]string(nil), dependents[f]...)
		sort.Strings(next)
		for _, g := range next {
			indegree[g]--
			if indegree[g] == 0 {
				ready = append(ready, g)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(derivers) {
		return nil, fmt.Errorf("fieldauth: deriver dependency cycle detected")
	}
	return order, nil
}
