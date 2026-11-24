package semijoin_test

import (
	"strings"
	"testing"

	"prescreen.local/ps-13de224b0c/semijoin"
)

func TestValidationRejectsUnknownPageVariable(t *testing.T) {
	problem := &semijoin.Problem{
		Semantic: semijoin.Exists,
		Pages: []semijoin.Page{
			{ID: "bad", Cost: 1, Reveals: []semijoin.PageVariable{{VariableID: "missing", State: semijoin.Final}}},
		},
	}
	_, err := semijoin.Solve(problem)
	if err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Fatalf("err = %v, want unknown variable", err)
	}
}

func TestValidationRejectsTooManyUncertainCounts(t *testing.T) {
	variables := make([]semijoin.Variable, 0, 11)
	for i := 0; i < 11; i++ {
		variables = append(variables, semijoin.Variable{
			ID:    string(rune('a' + i)),
			State: semijoin.Final,
			Kind:  semijoin.KeyCount,
			Key:   "shared",
			Min:   0,
			Max:   1,
		})
	}
	_, err := semijoin.Solve(&semijoin.Problem{Semantic: semijoin.Exists, Variables: variables})
	if err == nil || !strings.Contains(err.Error(), "uncertain counts") {
		t.Fatalf("err = %v, want uncertain count limit", err)
	}
}
