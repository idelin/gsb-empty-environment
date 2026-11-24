package semijoin

import (
	"fmt"
	"math/big"
	"sort"
)

func buildModel(problem *Problem) (*model, error) {
	m := &model{
		problem: problem,
		byID:    make(map[string]*variableInfo),
		pvIndex: make(map[PageVariable]int),
		edges:   make(map[int]map[int]bool),
		outer:   make(map[string][]OuterRow),
	}
	for _, row := range problem.Outer {
		m.outer[row.Group] = append(m.outer[row.Group], row)
	}

	m.infos = make([]*variableInfo, len(problem.Variables))
	for i, v := range problem.Variables {
		info := &variableInfo{variable: v, index: i}
		width := v.Max - v.Min
		if width < 0 {
			return nil, fmt.Errorf("variable %s has invalid interval", v.ID)
		}
		info.uncertain = width > 0 && !(v.Min > 0)
		if info.uncertain && width > 4 {
			return nil, fmt.Errorf("uncertain variable %s has range wider than 0..4", v.ID)
		}
		if info.uncertain {
			info.domain = make([]int64, 0, width+1)
			for value := v.Min; value <= v.Max; value++ {
				info.domain = append(info.domain, value)
			}
		} else {
			info.domain = []int64{v.Min, v.Max}
		}
		m.infos[i] = info
		m.byID[v.ID] = info
		m.edges[i] = make(map[int]bool)
	}

	keyVars := make(map[canonicalKey][]int)
	nullShards := make(map[int][]int)
	for i, info := range m.infos {
		v := info.variable
		if v.Kind == KeyCount {
			key, _ := canonical(v.Key)
			keyVars[key] = append(keyVars[key], i)
		} else {
			nullShards[v.Shard] = append(nullShards[v.Shard], i)
		}
	}
	connectAll(m, keyVars)
	for _, ids := range nullShards {
		connectAll(m, map[canonicalKey][]int{{kind: 0}: ids})
	}

	keyIndex := make(map[canonicalKey]map[State][]int)
	nullIndex := make(map[int]map[State][]int)
	for i, info := range m.infos {
		v := info.variable
		if v.Kind == KeyCount {
			key, _ := canonical(v.Key)
			if keyIndex[key] == nil {
				keyIndex[key] = make(map[State][]int)
			}
			keyIndex[key][v.State] = append(keyIndex[key][v.State], i)
		} else {
			if nullIndex[v.Shard] == nil {
				nullIndex[v.Shard] = make(map[State][]int)
			}
			nullIndex[v.Shard][v.State] = append(nullIndex[v.Shard][v.State], i)
		}
	}

	for _, row := range problem.Outer {
		if !row.Key.Valid {
			continue
		}
		key, _ := canonical(row.Key.Value)
		for _, state := range []State{Initial, Final} {
			ids := keyIndex[key][state]
			for _, id := range ids {
				for _, other := range ids {
					m.edges[id][other] = true
					m.edges[other][id] = true
				}
			}
		}
		if problem.Semantic == NotNullIn && row.Key.Valid {
			for state, keyIDs := range keyIndex[key] {
				nullIDs := nullIndex[row.Shard][state]
				for _, id := range keyIDs {
					for _, other := range nullIDs {
						m.edges[id][other] = true
						m.edges[other][id] = true
					}
				}
			}
			for _, ids := range nullIndex[row.Shard] {
				for _, id := range ids {
					for _, other := range ids {
						m.edges[id][other] = true
						m.edges[other][id] = true
					}
				}
			}
		}
	}

	for ci, constraint := range problem.Constraints {
		var ids []int
		for _, term := range constraint.Terms {
			id := m.byID[term.VariableID].index
			for _, other := range ids {
				m.edges[id][other] = true
				m.edges[other][id] = true
			}
			ids = append(ids, id)
		}
		_ = ci
	}

	for _, page := range problem.Pages {
		var ids []int
		for _, rv := range page.Reveals {
			info := m.byID[rv.VariableID]
			if info.variable.State != rv.State {
				return nil, fmt.Errorf("page %s state does not match variable %s", page.ID, rv.VariableID)
			}
			id := info.index
			m.pvIndex[rv] = id
			ids = append(ids, id)
		}
		for _, id := range ids {
			for _, other := range ids {
				m.edges[id][other] = true
				m.edges[other][id] = true
			}
		}
	}

	visited := make([]bool, len(m.infos))
	for start := range m.infos {
		if visited[start] {
			continue
		}
		comp := &component{
			vars:   make(map[int]bool),
			keys:   make(map[canonicalKey]bool),
			nulls:  make(map[int]bool),
			groups: make(map[string]bool),
		}
		stack := []int{start}
		visited[start] = true
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp.vars[id] = true
			info := m.infos[id]
			if info.variable.Kind == KeyCount {
				key, _ := canonical(info.variable.Key)
				comp.keys[key] = true
			} else {
				comp.nulls[info.variable.Shard] = true
			}
			for next := range m.edges[id] {
				if !visited[next] {
					visited[next] = true
					stack = append(stack, next)
				}
			}
		}

		for ci, constraint := range problem.Constraints {
			for _, term := range constraint.Terms {
				if comp.vars[m.byID[term.VariableID].index] {
					comp.cons = append(comp.cons, ci)
					break
				}
			}
		}
		for _, row := range problem.Outer {
			if !row.Key.Valid {
				comp.groups[row.Group] = true
				continue
			}
			key, _ := canonical(row.Key.Value)
			if comp.keys[key] || (problem.Semantic == NotNullIn && comp.nulls[row.Shard]) {
				comp.groups[row.Group] = true
			}
		}
		m.comps = append(m.comps, comp)
	}
	if len(m.comps) == 0 {
		comp := &component{
			vars:   make(map[int]bool),
			keys:   make(map[canonicalKey]bool),
			nulls:  make(map[int]bool),
			groups: make(map[string]bool),
		}
		for _, row := range problem.Outer {
			comp.groups[row.Group] = true
		}
		m.comps = append(m.comps, comp)
	}
	return m, nil
}

func connectAll(m *model, groups map[canonicalKey][]int) {
	for _, ids := range groups {
		for _, id := range ids {
			for _, other := range ids {
				m.edges[id][other] = true
				m.edges[other][id] = true
			}
		}
	}
}

func (m *model) enumerateComponents() error {
	for ci, comp := range m.comps {
		uncertain := 0
		var ids []int
		values := make(Assignment)
		for id := range comp.vars {
			info := m.infos[id]
			if info.uncertain {
				uncertain++
				ids = append(ids, id)
			} else {
				if info.variable.Min == info.variable.Max {
					values[info.variable.ID] = info.variable.Min
				}
			}
		}
		if uncertain > 10 {
			return fmt.Errorf("conflict component %d has %d uncertain counts", ci, uncertain)
		}
		sort.Ints(ids)
		if err := m.enumerate(comp, ids, 0, values); err != nil {
			return err
		}
		if len(comp.worlds) == 0 {
			return fmt.Errorf("conflict component %d has no feasible database", ci)
		}
		comp.outputs = make([]map[string]int64, len(comp.worlds))
		for wi, candidate := range comp.worlds {
			comp.outputs[wi] = m.delta(comp, candidate.values)
		}
		comp.ambiguous = outputsDiffer(comp.outputs)
	}
	return nil
}

func (m *model) enumerate(comp *component, ids []int, pos int, values Assignment) error {
	if pos == len(ids) {
		if m.constraintsSatisfied(comp, values) {
			copied := make(Assignment, len(values))
			for key, value := range values {
				copied[key] = value
			}
			comp.worlds = append(comp.worlds, world{values: copied})
		}
		return nil
	}
	info := m.infos[ids[pos]]
	for _, value := range info.domain {
		values[info.variable.ID] = value
		if err := m.enumerate(comp, ids, pos+1, values); err != nil {
			return err
		}
	}
	delete(values, info.variable.ID)
	return nil
}

func (m *model) constraintsSatisfied(comp *component, values Assignment) bool {
	for _, ci := range comp.cons {
		constraint := m.problem.Constraints[ci]
		low := big.NewInt(0)
		high := big.NewInt(0)
		for _, term := range constraint.Terms {
			info := m.byID[term.VariableID]
			value, ok := values[term.VariableID]
			coef := big.NewInt(term.Coefficient)
			if !ok {
				if info.uncertain {
					return false
				}
				a := new(big.Int).Mul(coef, big.NewInt(info.variable.Min))
				b := new(big.Int).Mul(coef, big.NewInt(info.variable.Max))
				if a.Cmp(b) > 0 {
					a, b = b, a
				}
				low.Add(low, a)
				high.Add(high, b)
				continue
			}
			termValue := new(big.Int).Mul(coef, big.NewInt(value))
			low.Add(low, termValue)
			high.Add(high, termValue)
		}
		constant := big.NewInt(constraint.Constant)
		switch constraint.Op {
		case "=":
			if low.Cmp(constant) > 0 || high.Cmp(constant) < 0 {
				return false
			}
		case "<=":
			if low.Cmp(constant) > 0 {
				return false
			}
		case ">=":
			if high.Cmp(constant) < 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func outputsDiffer(outputs []map[string]int64) bool {
	if len(outputs) <= 1 {
		return false
	}
	first := outputs[0]
	for _, output := range outputs[1:] {
		if len(first) != len(output) {
			return true
		}
		for group, value := range first {
			if output[group] != value {
				return true
			}
		}
	}
	return false
}
