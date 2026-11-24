package semijoin

import (
	"sort"
)

func (m *model) buildSuperComponents() {
	pageTouches := make(map[int]map[int]bool)
	for ci, comp := range m.comps {
		if !comp.ambiguous {
			continue
		}
		for pi, page := range m.problem.Pages {
			for _, rv := range page.Reveals {
				id := m.pvIndex[rv]
				if comp.vars[id] {
					if pageTouches[pi] == nil {
						pageTouches[pi] = make(map[int]bool)
					}
					pageTouches[pi][ci] = true
				}
			}
			_ = page
		}
	}

	parent := make([]int, len(m.comps))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		parent[find(a)] = find(b)
	}
	for ci, comp := range m.comps {
		if !comp.ambiguous {
			continue
		}
		for pi := range m.problem.Pages {
			if touches, ok := pageTouches[pi]; ok && touches[ci] {
				for owner := range touches {
					union(ci, owner)
				}
			}
		}
	}

	byRoot := make(map[int]*superComponent)
	for ci, comp := range m.comps {
		if !comp.ambiguous {
			continue
		}
		root := find(ci)
		super := byRoot[root]
		if super == nil {
			super = &superComponent{comps: make(map[int]bool)}
			byRoot[root] = super
		}
		super.comps[ci] = true
		for group := range comp.groups {
			super.groups = append(super.groups, group)
		}
	}

	roots := make([]int, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Ints(roots)
	for _, root := range roots {
		super := byRoot[root]
		sort.Strings(super.groups)
		compIDs := make([]int, 0, len(super.comps))
		for ci := range super.comps {
			compIDs = append(compIDs, ci)
		}
		sort.Ints(compIDs)

		pageSet := make(map[int]bool)
		for _, ci := range compIDs {
			for pi, page := range m.problem.Pages {
				for _, rv := range page.Reveals {
					if m.comps[ci].vars[m.pvIndex[rv]] {
						pageSet[pi] = true
					}
				}
			}
		}
		super.pages = sortedIntSet(pageSet)

		merged := make([]Assignment, 1)
		merged[0] = make(Assignment)
		for _, ci := range compIDs {
			var next []Assignment
			for _, left := range merged {
				for _, candidate := range m.comps[ci].worlds {
					join := make(Assignment, len(left)+len(candidate.values))
					for key, value := range left {
						join[key] = value
					}
					for key, value := range candidate.values {
						join[key] = value
					}
					next = append(next, join)
				}
			}
			merged = next
		}
		super.worlds = make([]world, 0, len(merged))
		for _, values := range merged {
			for _, info := range m.infos {
				if _, ok := values[info.variable.ID]; !ok {
					values[info.variable.ID] = info.variable.Min
				}
			}
			pageValues := make(map[string]PageValues)
			for _, pi := range super.pages {
				page := m.problem.Pages[pi]
				observed := make(PageValues)
				for _, rv := range page.Reveals {
					id := m.pvIndex[rv]
					info := m.infos[id]
					observed[rv] = values[info.variable.ID]
				}
				pageValues[page.ID] = observed
			}
			super.worlds = append(super.worlds, world{values: values, pages: pageValues})
		}
		super.outputs = make([]map[string]int64, len(super.worlds))
		for wi, candidate := range super.worlds {
			output := make(map[string]int64)
			for _, ci := range compIDs {
				for group, delta := range m.delta(m.comps[ci], candidate.values) {
					output[group] = delta
				}
			}
			super.outputs[wi] = output
		}
		m.supers = append(m.supers, super)
	}
}

func sortedIntSet(values map[int]bool) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}
