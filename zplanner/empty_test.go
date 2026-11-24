package zplanner

import "testing"

func TestPlanEmptyOps(t *testing.T) {
	p, err := Plan(InitialState{Table: emptyTable()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 0 || p.Cost != (Cost{}) {
		t.Fatalf("unexpected %+v", p)
	}
}
