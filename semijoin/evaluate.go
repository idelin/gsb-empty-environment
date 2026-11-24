package semijoin

import (
	"math/big"
	"sort"
)

func Evaluate(problem *Problem, values Assignment) map[string]int64 {
	m := &model{
		problem: problem,
		byID:    make(map[string]*variableInfo),
		outer:   make(map[string][]OuterRow),
	}
	for i := range problem.Variables {
		info := &variableInfo{variable: problem.Variables[i], index: i}
		m.byID[info.variable.ID] = info
	}
	comp := &component{groups: make(map[string]bool)}
	for _, row := range problem.Outer {
		comp.groups[row.Group] = true
		m.outer[row.Group] = append(m.outer[row.Group], row)
	}
	return m.delta(comp, values)
}

func (m *model) delta(comp *component, values Assignment, only ...State) map[string]int64 {
	result := make(map[string]int64)
	groups := make([]string, 0, len(comp.groups))
	for group := range comp.groups {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		delta := int64(0)
		for _, row := range m.outerRows(group) {
			if len(only) == 0 || only[0] == Initial {
				before := m.predicate(row, values, Initial)
				delta = addProduct(delta, -row.InitialCount, truthInt64(before))
			}
			if len(only) == 0 || only[0] == Final {
				after := m.predicate(row, values, Final)
				delta = addProduct(delta, row.FinalCount+row.Delta, truthInt64(after))
			}
		}
		result[group] = delta
	}
	return result
}

func addProduct(sum int64, left, right int64) int64 {
	value := new(big.Int).Mul(big.NewInt(left), big.NewInt(right))
	value.Add(value, big.NewInt(sum))
	if value.Cmp(big.NewInt(1<<63-1)) > 0 {
		return 1<<63 - 1
	}
	if value.Cmp(big.NewInt(-1<<63)) < 0 {
		return -1 << 63
	}
	return value.Int64()
}

func (m *model) outerRows(group string) []OuterRow {
	if rows, ok := m.outer[group]; ok {
		return rows
	}
	var rows []OuterRow
	for _, row := range m.problem.Outer {
		if row.Group == group {
			rows = append(rows, row)
		}
	}
	return rows
}

func truthInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (m *model) predicate(row OuterRow, values Assignment, state State) bool {
	if !row.Key.Valid {
		return false
	}
	key, _ := canonical(row.Key.Value)
	keyPresent := false
	nullPresent := false
	for id, value := range values {
		if value <= 0 {
			continue
		}
		info := m.byID[id]
		if info.variable.State != state {
			continue
		}
		if info.variable.Kind == KeyCount {
			variableKey, _ := canonical(info.variable.Key)
			if variableKey == key {
				keyPresent = true
			}
		} else if info.variable.Kind == NullCount && info.variable.Shard == row.Shard {
			nullPresent = true
		}
	}
	switch m.problem.Semantic {
	case Exists:
		return keyPresent
	case NotExists:
		return !keyPresent
	case NotNullIn:
		if nullPresent {
			return false
		}
		return !keyPresent
	default:
		return false
	}
}
