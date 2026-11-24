package semijoin_test

import (
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

type testInnerRow struct {
	shard int
	key   semijoin.Nullable
}

type testWorld struct {
	values map[string]int64
	rows   map[semijoin.State][]testInnerRow
}

func TestSemanticsAndExhaustiveReadTrees(t *testing.T) {
	tests := []struct {
		name        string
		semantic    semijoin.Semantic
		outer       []semijoin.OuterRow
		variables   []semijoin.Variable
		constraints []semijoin.LinearConstraint
		pages       []semijoin.Page
	}{
		{
			name:     "exists",
			semantic: semijoin.Exists,
			outer: []semijoin.OuterRow{
				{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 2, FinalCount: 2},
			},
			variables: keyPair("k", 0),
			pages: []semijoin.Page{
				{ID: "initial", Cost: 5, Reveals: []semijoin.PageVariable{{VariableID: "ki", State: semijoin.Initial}}},
				{ID: "final", Cost: 7, Reveals: []semijoin.PageVariable{{VariableID: "kf", State: semijoin.Final}}},
			},
		},
		{
			name:     "not exists",
			semantic: semijoin.NotExists,
			outer: []semijoin.OuterRow{
				{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 3, FinalCount: 3},
			},
			variables: keyPair("k", 0),
			pages: []semijoin.Page{
				{ID: "final", Cost: 4, Reveals: []semijoin.PageVariable{{VariableID: "kf", State: semijoin.Final}}},
			},
		},
		{
			name:     "not in null is not global delete",
			semantic: semijoin.NotNullIn,
			outer: []semijoin.OuterRow{
				{Group: "same-shard", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 2, FinalCount: 2},
				{Group: "other-shard", Shard: 1, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 2, FinalCount: 2},
			},
			variables: append(append(keyPair("k", 0), nullPair(0)...), nullPair(1)...),
			pages: []semijoin.Page{
				{ID: "final-null", Cost: 6, Reveals: []semijoin.PageVariable{{VariableID: "n0f", State: semijoin.Final}}},
			},
		},
		{
			name:     "outer null key is unknown for all correlated predicates",
			semantic: semijoin.Exists,
			outer: []semijoin.OuterRow{
				{Group: "null-key", Shard: 0, Key: semijoin.Nullable{}, InitialCount: 4, FinalCount: 4},
			},
			variables: keyPair("k", 0),
		},
		{
			name:     "linear shared constraint",
			semantic: semijoin.Exists,
			outer: []semijoin.OuterRow{
				{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 1, FinalCount: 1},
			},
			variables: keyPair("k", 0),
			constraints: []semijoin.LinearConstraint{{
				Terms: []semijoin.LinearTerm{
					{Coefficient: 1, VariableID: "ki"},
					{Coefficient: 1, VariableID: "kf"},
				},
				Constant: 2,
				Op:       "=",
			}},
			pages: []semijoin.Page{
				{ID: "initial", Cost: 3, Reveals: []semijoin.PageVariable{{VariableID: "ki", State: semijoin.Initial}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problem := &semijoin.Problem{
				Semantic:    tt.semantic,
				Outer:       tt.outer,
				Variables:   tt.variables,
				Constraints: tt.constraints,
				Pages:       tt.pages,
			}
			worlds := enumerateTestWorlds(t, problem)
			expectedCost, expectedPossible := bruteForceOptimal(problem, worlds)
			result, err := semijoin.Solve(problem)
			if err != nil {
				t.Fatalf("Solve returned error: %v", err)
			}
			if result.Determined != expectedPossible {
				t.Fatalf("Determined = %v, want %v", result.Determined, expectedPossible)
			}
			if !expectedPossible {
				verifyWitness(t, problem, worlds, result)
				return
			}
			if result.MinimumWorstReadCost != expectedCost {
				t.Fatalf("cost = %d, want %d", result.MinimumWorstReadCost, expectedCost)
			}
			verifyPolicies(t, problem, worlds, result)
			for _, world := range worlds {
				if !uniformOutputs(problem, worlds) {
					continue
				}
				got := interpreterDelta(problem, world)
				for group, want := range got {
					if deterministic, ok := result.DeterministicOutputs[group]; ok && deterministic != want {
						t.Fatalf("deterministic delta for %s = %d, want %d", group, deterministic, want)
					}
				}
			}
		})
	}
}

func TestBatchCancellationAndWideIntervals(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "net-zero", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 5, FinalCount: 5, Delta: -2},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 0},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 9, Max: 9},
		},
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !result.Determined || result.MinimumWorstReadCost != 0 {
		t.Fatalf("got determined=%v cost=%d, want deterministic zero-cost", result.Determined, result.MinimumWorstReadCost)
	}
	worlds := enumerateTestWorlds(t, problem)
	want := int64(3)
	for _, world := range worlds {
		if got := interpreterDelta(problem, world)["net-zero"]; got != want {
			t.Fatalf("delta = %d, want %d", got, want)
		}
	}
}

func TestWideDeterministicIntervalStaysAbstract(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 1, FinalCount: 1},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 5, Max: 90},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 5, Max: 90},
		},
	}
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !result.Determined || result.MinimumWorstReadCost != 0 {
		t.Fatalf("got determined=%v cost=%d, want deterministic zero-cost", result.Determined, result.MinimumWorstReadCost)
	}
}

func TestEvaluateMatchesInterpreter(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.NotExists,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 2, FinalCount: 3, Delta: 1},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 1, Max: 1},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 0},
		},
	}
	worlds := enumerateTestWorlds(t, problem)
	values := worlds[0].values
	got := semijoin.Evaluate(problem, values)
	want := interpreterDelta(problem, worlds[0])
	if !mapsEqualInt64(got, want) {
		t.Fatalf("Evaluate = %v, want %v", got, want)
	}
}

func TestLinearConstraintReducesFeasibleWorlds(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Outer: []semijoin.OuterRow{
			{Group: "g", Shard: 0, Key: semijoin.Nullable{Value: "k", Valid: true}, InitialCount: 1, FinalCount: 1},
		},
		Variables: []semijoin.Variable{
			{ID: "ki", Shard: 0, State: semijoin.Initial, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 2},
			{ID: "kf", Shard: 0, State: semijoin.Final, Kind: semijoin.KeyCount, Key: "k", Min: 0, Max: 2},
		},
		Constraints: []semijoin.LinearConstraint{{
			Terms: []semijoin.LinearTerm{
				{Coefficient: 1, VariableID: "ki"},
				{Coefficient: -1, VariableID: "kf"},
			},
			Constant: 0,
			Op:       "=",
		}},
		Pages: []semijoin.Page{
			{ID: "initial", Cost: 2, Reveals: []semijoin.PageVariable{{VariableID: "ki", State: semijoin.Initial}}},
		},
	}
	worlds := enumerateTestWorlds(t, problem)
	expectedCost, possible := bruteForceOptimal(problem, worlds)
	result, err := semijoin.Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !result.Determined || !possible || result.MinimumWorstReadCost != expectedCost {
		t.Fatalf("got determined=%v cost=%d, want possible brute-force cost %d", result.Determined, result.MinimumWorstReadCost, expectedCost)
	}
	if len(worlds) != 3 {
		t.Fatalf("feasible worlds = %d, want 3 constrained worlds", len(worlds))
	}
}
