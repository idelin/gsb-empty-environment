package symbolbind

import (
	"fmt"
	"sort"
)

const defaultAttackLimit = 20
const defaultMaxSchemes int64 = 1_000_000

func FindMinimalAttacks(input Input, relocation Relocation, options AttackOptions) (*AttackResult, error) {
	if err := Validate(input); err != nil {
		return nil, err
	}
	if !relocation.RequireTrusted {
		return nil, fmt.Errorf("attack search requires a relocation with RequireTrusted=true")
	}

	limit := options.Limit
	if limit <= 0 {
		limit = defaultAttackLimit
	}
	maxSchemes := options.MaxSchemes
	if maxSchemes <= 0 {
		maxSchemes = defaultMaxSchemes
	}

	budget := maxSchemes
	baselineTrace, err := Bind(input, relocation)
	if err != nil {
		return nil, err
	}
	budget--
	if baselineTrace.Untrusted {
		return &AttackResult{
			Status:      StatusAttack,
			PluginCount: 0,
			Limit:       limit,
			Schemes: []Scheme{{
				Insertions:  []Insertion{},
				GlobalOrder: append([]string(nil), input.GlobalOrder...),
				Trace:       baselineTrace,
			}},
			Reason: "the host already binds the trusted-required relocation to an untrusted actual definer",
		}, nil
	}
	if budget == 0 {
		return unknownResult(limit, "search budget exhausted before all plugin placements could be checked"), nil
	}

	plugins := append([]Module(nil), input.Plugins...)
	sort.Slice(plugins, func(left, right int) bool {
		return plugins[left].ID < plugins[right].ID
	})
	maxPlugins := 3
	if len(plugins) < maxPlugins {
		maxPlugins = len(plugins)
	}
	if available := 16 - len(input.Modules); available < maxPlugins {
		maxPlugins = available
	}

	for count := 1; count <= maxPlugins; count++ {
		attacks := make([]Scheme, 0)
		truncated := false
		budgetExhausted := false
		var enumerationErr error

		forEachSubset(plugins, count, func(chosen []Module) {
			if budgetExhausted || enumerationErr != nil {
				return
			}
			forEachPermutation(chosen, func(ordered []Module) {
				forEachInsertion(input.GlobalOrder, ordered, func(globalOrder []string, positions []int) {
					if budgetExhausted || enumerationErr != nil {
						return
					}
					if budget == 0 {
						budgetExhausted = true
						return
					}
					budget--

					trace, err := BindWithGlobal(input, globalOrder, relocation)
					if err != nil {
						enumerationErr = err
						budgetExhausted = true
						return
					}
					if trace.Untrusted {
						insertions := make([]Insertion, 0, len(ordered))
						for index, plugin := range ordered {
							insertions = append(insertions, Insertion{
								PluginID: plugin.ID,
								Position: positions[index],
							})
						}
						scheme := Scheme{
							Insertions:  insertions,
							GlobalOrder: append([]string(nil), globalOrder...),
							Trace:       trace,
						}
						if len(attacks) >= limit {
							truncated = true
							return
						}
						attacks = append(attacks, scheme)
					}
				})
			})
		})

		if enumerationErr != nil {
			return nil, enumerationErr
		}
		if budgetExhausted {
			return unknownResult(limit, "search budget exhausted before all equal-length plugin placements could be checked"), nil
		}
		if len(attacks) > 0 {
			sortSchemes(attacks)
			return &AttackResult{
				Status:      StatusAttack,
				PluginCount: count,
				Schemes:     attacks,
				Limit:       limit,
				Truncated:   truncated,
				Reason:      fmt.Sprintf("minimum successful attack uses %d plugin(s); ties are ordered by plugin ID and then insertion position", count),
			}, nil
		}
	}

	return &AttackResult{
		Status:      StatusNoAttack,
		PluginCount: 0,
		Limit:       limit,
		Reason:      "no placement of up to three plugins makes the trusted-required relocation bind an untrusted actual definer",
	}, nil
}

func forEachSubset(plugins []Module, count int, visit func([]Module)) {
	indices := make([]int, count)
	var walk func(start, depth int)
	walk = func(start, depth int) {
		if depth == count {
			chosen := make([]Module, count)
			for index, value := range indices {
				chosen[index] = plugins[value]
			}
			visit(chosen)
			return
		}
		remaining := count - depth
		for index := start; index <= len(plugins)-remaining; index++ {
			indices[depth] = index
			walk(index+1, depth+1)
		}
	}
	walk(0, 0)
}

func forEachPermutation(plugins []Module, visit func([]Module)) {
	ordered := append([]Module(nil), plugins...)
	var generate func(start int)
	generate = func(start int) {
		if start == len(ordered) {
			visit(append([]Module(nil), ordered...))
			return
		}
		for index := start; index < len(ordered); index++ {
			ordered[start], ordered[index] = ordered[index], ordered[start]
			generate(start + 1)
			ordered[start], ordered[index] = ordered[index], ordered[start]
		}
	}
	generate(0)
}

func forEachInsertion(baseline []string, plugins []Module, visit func([]string, []int)) {
	positions := make([]int, len(plugins))
	var walk func(pluginIndex, minPosition int)
	walk = func(pluginIndex, minPosition int) {
		if pluginIndex == len(plugins) {
			globalOrder := insertAtPositions(baseline, plugins, positions)
			visit(globalOrder, append([]int(nil), positions...))
			return
		}
		for position := minPosition; position <= len(baseline); position++ {
			positions[pluginIndex] = position
			walk(pluginIndex+1, position)
		}
	}
	walk(0, 0)
}

func insertAtPositions(baseline []string, plugins []Module, positions []int) []string {
	globalOrder := make([]string, 0, len(baseline)+len(plugins))
	pluginIndex := 0
	for position, moduleID := range baseline {
		for pluginIndex < len(plugins) && positions[pluginIndex] == position {
			globalOrder = append(globalOrder, plugins[pluginIndex].ID)
			pluginIndex++
		}
		globalOrder = append(globalOrder, moduleID)
	}
	for pluginIndex < len(plugins) {
		globalOrder = append(globalOrder, plugins[pluginIndex].ID)
		pluginIndex++
	}
	return globalOrder
}

func sortSchemes(schemes []Scheme) {
	sort.Slice(schemes, func(left, right int) bool {
		leftInsertions := schemes[left].Insertions
		rightInsertions := schemes[right].Insertions
		for index := 0; index < len(leftInsertions) && index < len(rightInsertions); index++ {
			if leftInsertions[index].PluginID != rightInsertions[index].PluginID {
				return leftInsertions[index].PluginID < rightInsertions[index].PluginID
			}
			if leftInsertions[index].Position != rightInsertions[index].Position {
				return leftInsertions[index].Position < rightInsertions[index].Position
			}
		}
		return false
	})
}

func unknownResult(limit int, reason string) *AttackResult {
	return &AttackResult{
		Status: StatusUnknown,
		Limit:  limit,
		Reason: reason,
	}
}
