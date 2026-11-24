package semijoin_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

func keyPair(key string, shard int) []semijoin.Variable {
	return []semijoin.Variable{
		{ID: "ki", Shard: shard, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: key, Min: 0, Max: 2},
		{ID: "kf", Shard: shard, State: semijoin.Final, Kind: semijoin.KeyCount, Key: key, Min: 0, Max: 2},
	}
}

func nullPair(shard int) []semijoin.Variable {
	idPrefix := fmt.Sprintf("n%d", shard)
	return []semijoin.Variable{
		{ID: idPrefix + "i", Shard: shard, State: semijoin.Initial, Kind: semijoin.NullCount, Min: 0, Max: 1},
		{ID: idPrefix + "f", Shard: shard, State: semijoin.Final, Kind: semijoin.NullCount, Min: 0, Max: 1},
	}
}

func enumerateTestWorlds(t *testing.T, problem *semijoin.Problem) []testWorld {
	t.Helper()
	variables := append([]semijoin.Variable(nil), problem.Variables...)
	sort.Slice(variables, func(i, j int) bool { return variables[i].ID < variables[j].ID })
	values := map[string]int64{}
	var worlds []testWorld
	var walk func(int)
	walk = func(pos int) {
		if pos == len(variables) {
			if testConstraints(problem.Constraints, values) {
				worlds = append(worlds, testWorld{
					values: copyInt64Map(values),
					rows:   materializeRows(problem, values),
				})
			}
			return
		}
		variable := variables[pos]
		for value := variable.Min; value <= variable.Max; value++ {
			values[variable.ID] = value
			walk(pos + 1)
		}
		delete(values, variable.ID)
	}
	walk(0)
	if len(worlds) == 0 {
		t.Fatal("test setup produced no feasible worlds")
	}
	return worlds
}

func testConstraints(constraints []semijoin.LinearConstraint, values map[string]int64) bool {
	for _, constraint := range constraints {
		sum := int64(0)
		for _, term := range constraint.Terms {
			sum += term.Coefficient * values[term.VariableID]
		}
		switch constraint.Op {
		case "=":
			if sum != constraint.Constant {
				return false
			}
		case "<=":
			if sum > constraint.Constant {
				return false
			}
		case ">=":
			if sum < constraint.Constant {
				return false
			}
		}
	}
	return true
}

func materializeRows(problem *semijoin.Problem, values map[string]int64) map[semijoin.State][]testInnerRow {
	result := map[semijoin.State][]testInnerRow{}
	for _, variable := range problem.Variables {
		for i := int64(0); i < values[variable.ID]; i++ {
			row := testInnerRow{shard: variable.Shard}
			if variable.Kind == semijoin.KeyCount {
				row.key = semijoin.Nullable{Value: variable.Key, Valid: true}
			}
			result[variable.State] = append(result[variable.State], row)
		}
	}
	return result
}

func interpreterDelta(problem *semijoin.Problem, world testWorld) map[string]int64 {
	result := map[string]int64{}
	for _, outer := range problem.Outer {
		before := sqlPredicate(problem.Semantic, outer, world.rows[semijoin.Initial])
		after := sqlPredicate(problem.Semantic, outer, world.rows[semijoin.Final])
		delta := -outer.InitialCount * boolInt64(before)
		delta += (outer.FinalCount + outer.Delta) * boolInt64(after)
		result[outer.Group] = delta
	}
	return result
}

func sqlPredicate(semantic semijoin.Semantic, outer semijoin.OuterRow, rows []testInnerRow) bool {
	for _, row := range rows {
		if row.shard != outer.Shard {
			continue
		}
		match, unknown := sqlEquals(outer.Key, row.key)
		switch semantic {
		case semijoin.Exists:
			if match {
				return true
			}
		case semijoin.NotExists:
			if unknown {
				return false
			}
			if match {
				return false
			}
		case semijoin.NotNullIn:
			if unknown || match {
				return false
			}
		}
	}
	return semantic != semijoin.Exists
}

func sqlEquals(left, right semijoin.Nullable) (match bool, unknown bool) {
	if !left.Valid || !right.Valid {
		return false, true
	}
	return fmt.Sprint(left.Value) == fmt.Sprint(right.Value), false
}

func bruteForceOptimal(problem *semijoin.Problem, worlds []testWorld) (int64, bool) {
	used := map[string]bool{}
	return bruteForceRecursive(problem, worlds, used, 0)
}

func bruteForceRecursive(problem *semijoin.Problem, worlds []testWorld, used map[string]bool, spent int64) (int64, bool) {
	if uniformOutputs(problem, worlds) {
		return spent, true
	}
	best := int64(-1)
	for _, page := range problem.Pages {
		if used[page.ID] {
			continue
		}
		groups := map[string][]testWorld{}
		for _, world := range worlds {
			key := pageKey(problem, page, world)
			groups[key] = append(groups[key], world)
		}
		if len(groups) == 1 {
			continue
		}
		used[page.ID] = true
		var branchCost int64
		possible := true
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cost, ok := bruteForceRecursive(problem, groups[key], used, spent+page.Cost)
			if !ok {
				possible = false
				break
			}
			if cost > branchCost {
				branchCost = cost
			}
		}
		delete(used, page.ID)
		if possible && (best < 0 || branchCost < best) {
			best = branchCost
		}
	}
	return best, best >= 0
}

func uniformOutputs(problem *semijoin.Problem, worlds []testWorld) bool {
	if len(worlds) == 0 {
		return true
	}
	first := interpreterDelta(problem, worlds[0])
	for _, world := range worlds[1:] {
		if !mapsEqualInt64(first, interpreterDelta(problem, world)) {
			return false
		}
	}
	return true
}

func pageKey(problem *semijoin.Problem, page semijoin.Page, world testWorld) string {
	parts := make([]string, 0, len(page.Reveals))
	for _, reveal := range page.Reveals {
		variable := findVariable(problem, reveal.VariableID)
		parts = append(parts, fmt.Sprintf("%s=%d", reveal.VariableID, world.values[variable.ID]))
	}
	return strings.Join(parts, "|")
}

func findVariable(problem *semijoin.Problem, id string) semijoin.Variable {
	for _, variable := range problem.Variables {
		if variable.ID == id {
			return variable
		}
	}
	return semijoin.Variable{}
}

func verifyPolicies(t *testing.T, problem *semijoin.Problem, worlds []testWorld, result *semijoin.Result) {
	t.Helper()
	for _, policy := range result.Policies {
		for _, world := range worlds {
			cost, output, ok := followPolicy(problem, policy, world)
			if !ok {
				t.Fatalf("policy did not classify a feasible world")
			}
			want := interpreterDelta(problem, world)
			for _, group := range policy.Groups {
				if output[group] != want[group] {
					t.Fatalf("policy output for %s = %d, want %d", group, output[group], want[group])
				}
			}
			if cost > policy.Worst {
				t.Fatalf("observed policy cost %d exceeds worst %d", cost, policy.Worst)
			}
		}
	}
}

func followPolicy(problem *semijoin.Problem, policy semijoin.Policy, world testWorld) (int64, map[string]int64, bool) {
	node := policy.Root
	cost := int64(0)
	for !node.Leaf {
		page := findPage(problem, node.ReadPage)
		cost += page.Cost
		observed := semijoin.PageValues{}
		for _, reveal := range page.Reveals {
			observed[reveal] = world.values[reveal.VariableID]
		}
		var next *semijoin.PolicyNode
		for _, branch := range node.Branches {
			if pageValuesEqual(branch.Observed, observed) {
				next = branch.Child
				break
			}
		}
		if next == nil {
			return 0, nil, false
		}
		node = next
	}
	return cost, node.Remaining, true
}

func findPage(problem *semijoin.Problem, id string) semijoin.Page {
	for _, page := range problem.Pages {
		if page.ID == id {
			return page
		}
	}
	return semijoin.Page{}
}

func verifyWitness(t *testing.T, problem *semijoin.Problem, worlds []testWorld, result *semijoin.Result) {
	t.Helper()
	if result.Witness == nil {
		t.Fatal("expected minimal witness")
	}
	first := findTestWorld(worlds, result.Witness.First.Variables)
	second := findTestWorld(worlds, result.Witness.Second.Variables)
	for _, pageID := range result.Witness.Pages {
		page := findPage(problem, pageID)
		for _, reveal := range page.Reveals {
			if first.values[reveal.VariableID] != second.values[reveal.VariableID] {
				t.Fatalf("witness differs on readable page %s", pageID)
			}
		}
	}
	firstOutput := interpreterDelta(problem, first)
	secondOutput := interpreterDelta(problem, second)
	if mapsEqualInt64(firstOutput, secondOutput) {
		t.Fatal("witness outputs do not differ")
	}
	if !mapsEqualInt64(result.Witness.FirstOutput, firstOutput) || !mapsEqualInt64(result.Witness.SecondOutput, secondOutput) {
		t.Fatal("witness output maps do not match the SQL interpreter")
	}
}

func findTestWorld(worlds []testWorld, values map[string]int64) testWorld {
	for _, world := range worlds {
		if mapsEqualInt64(world.values, values) {
			return world
		}
	}
	return testWorld{}
}

func mapsEqualInt64(left, right map[string]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func pageValuesEqual(left, right semijoin.PageValues) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func copyInt64Map(values map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
