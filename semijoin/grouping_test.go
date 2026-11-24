package semijoin_test

import (
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

func TestMultipleRowsInOneGroupAndNoInnerVariables(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "a", Valid: true}, InitialCount: 2, FinalCount: 2},
			{Group: "g", Shard: 0, Key: semijoin.Nullable{}, InitialCount: 3, FinalCount: 3},
			{Group: "h", Shard: 0, Key: semijoin.Nullable{Value: "b", Valid: true}, InitialCount: 1, FinalCount: 1},
		},
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !result.Determined || result.MinimumWorstReadCost != 0 {
		t.Fatalf("got determined=%v cost=%d, want zero-cost deterministic", result.Determined, result.MinimumWorstReadCost)
	}
	worlds := enumerateTestWorlds(t, problem)
	want := interpreterDelta(problem, worlds[0])
	if !mapsEqualInt64(result.DeterministicOutputs, want) {
		t.Fatalf("outputs = %v, want %v", result.DeterministicOutputs, want)
	}
}

func TestSignedOuterUpdatesCanCancelWithinBatch(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 4, FinalCount: 4, Delta: -3},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 1, Max: 1},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 1, Max: 1},
		},
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if result.DeterministicOutputs["g"] != -3 {
		t.Fatalf("delta = %d, want -3", result.DeterministicOutputs["g"])
	}
}
