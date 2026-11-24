package semijoin_test

import (
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

func TestImpossibleWitnessForUnreadNull(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.NotNullIn,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 1, FinalCount: 1},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 0},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 0},
			{ID: "ni", Shard: 0, State: semijoin.Initial, Kind: semijoin.NullCount, Min: 0, Max: 0},
			{ID: "nf", Shard: 0, State: semijoin.Final, Kind: semijoin.NullCount, Min: 0, Max: 1},
		},
		Pages: []semijoin.Page{
			{ID: "key", Cost: 1, Reveals: []semijoin.PageVariable{{VariableID: "kf", State: semijoin.Final}}},
		},
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if result.Determined {
		t.Fatal("expected undetermined problem")
	}
	if result.MinimumWorstReadCost != -1 || result.Witness == nil {
		t.Fatalf("got cost=%d witness=%v, want -1 and witness", result.MinimumWorstReadCost, result.Witness)
	}
	worlds := enumerateTestWorlds(t, problem)
	verifyWitness(t, problem, worlds, result)
}
