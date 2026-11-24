package semijoin_test

import (
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

func TestIndependentComponentsWorstCostsAdd(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "a", Shard: 0, Key: semijoin.Nullable{Value: "a", Valid: true}, InitialCount: 1, FinalCount: 1},
			{Group: "b", Shard: 0, Key: semijoin.Nullable{Value: "b", Valid: true}, InitialCount: 1, FinalCount: 1},
		},
		Variables: []semijoin.Variable{
			{ID: "ai", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "a", Min: 0, Max: 1},
			{ID: "af", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "a", Min: 0, Max: 1},
			{ID: "bi", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "b", Min: 0, Max: 1},
			{ID: "bf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "b", Min: 0, Max: 1},
		},
		Pages: []semijoin.Page{
			{ID: "a-page", Cost: 3, Reveals: []semijoin.PageVariable{
				{VariableID: "ai", State: semijoin.Initial},
				{VariableID: "af", State: semijoin.Final},
			}},
			{ID: "b-page", Cost: 5, Reveals: []semijoin.PageVariable{
				{VariableID: "bi", State: semijoin.Initial},
				{VariableID: "bf", State: semijoin.Final},
			}},
		},
	}
	worlds := enumerateTestWorlds(t, problem)
	expected, possible := bruteForceOptimal(problem, worlds)
	if !possible {
		t.Fatal("expected problem to be determinable")
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if result.MinimumWorstReadCost != expected || expected != 8 {
		t.Fatalf("cost = %d, want brute-force %d / 8", result.MinimumWorstReadCost, expected)
	}
}
