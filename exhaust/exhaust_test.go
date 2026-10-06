package exhaust

import (
	"testing"

	"configdiag/instance"
)

func toyInstance(t *testing.T) *instance.Instance {
	t.Helper()
	inst, err := instance.Parse([]byte(`{
	  "variables": [
	    {"name": "a", "domain": ["0", "1"]},
	    {"name": "b", "domain": ["0", "1"]}
	  ],
	  "constraints": [
	    {"name": "c1", "vars": ["a"], "allowed": [["0"]]},
	    {"name": "c2", "vars": ["a"], "allowed": [["1"]]},
	    {"name": "c3", "vars": ["b"], "allowed": [["0"]]},
	    {"name": "c4", "vars": ["b"], "allowed": [["1"]]},
	    {"name": "c5", "vars": ["a", "b"], "allowed": [["0", "0"], ["1", "1"]]}
	  ]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return inst
}

func TestVerifyMUSAccepts(t *testing.T) {
	v, err := VerifyMUS(toyInstance(t), []string{"c3", "c4"}, 0)
	if err != nil {
		t.Fatalf("VerifyMUS: %v", err)
	}
	if !v.OK {
		t.Fatalf("verification failed: %+v", v)
	}
	if !v.WholeUnsat {
		t.Error("whole set should be unsatisfiable")
	}
	if v.WholeAssignments != 2 {
		t.Errorf("whole assignments = %d, want 2 (scope is just b)", v.WholeAssignments)
	}
	if len(v.Removals) != 2 {
		t.Fatalf("got %d removal checks, want 2", len(v.Removals))
	}
	for _, r := range v.Removals {
		if !r.Satisfiable {
			t.Errorf("removal of %s should be satisfiable", r.Dropped)
		}
		if r.Witness == nil {
			t.Errorf("removal of %s: missing witness", r.Dropped)
		}
	}
}

func TestVerifyMUSRejectsSatisfiableSet(t *testing.T) {
	v, err := VerifyMUS(toyInstance(t), []string{"c3"}, 0)
	if err != nil {
		t.Fatalf("VerifyMUS: %v", err)
	}
	if v.OK {
		t.Error("a satisfiable set must not verify as an MUS")
	}
	if v.WholeUnsat {
		t.Error("whole set {c3} is satisfiable")
	}
}

func TestVerifyMUSRejectsNonMinimal(t *testing.T) {
	// {c3, c4, c5} is unsatisfiable but c5 is redundant.
	v, err := VerifyMUS(toyInstance(t), []string{"c3", "c4", "c5"}, 0)
	if err != nil {
		t.Fatalf("VerifyMUS: %v", err)
	}
	if v.OK {
		t.Error("a non-minimal unsatisfiable set must not verify as an MUS")
	}
	for _, r := range v.Removals {
		if r.Dropped == "c5" && r.Satisfiable {
			t.Error("dropping c5 leaves {c3, c4}, which is still unsatisfiable")
		}
	}
}

func TestVerifyMUSUnknownConstraint(t *testing.T) {
	if _, err := VerifyMUS(toyInstance(t), []string{"nope"}, 0); err == nil {
		t.Error("expected error for unknown constraint")
	}
}

func TestVerifyMUSEmpty(t *testing.T) {
	if _, err := VerifyMUS(toyInstance(t), nil, 0); err == nil {
		t.Error("expected error for empty MUS")
	}
}

func TestCheckCountsAssignments(t *testing.T) {
	s, err := Check(toyInstance(t), []string{"c1"}, 0)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if s.Assignments != 2 || s.Satisfying != 1 {
		t.Errorf("got %d/%d satisfying, want 1/2", s.Satisfying, s.Assignments)
	}
	if s.Witness["a"] != "0" {
		t.Errorf("witness = %v, want a=0", s.Witness)
	}
}

func TestCheckUnsat(t *testing.T) {
	s, err := Check(toyInstance(t), []string{"c1", "c2"}, 0)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if s.Satisfying != 0 || s.Witness != nil {
		t.Errorf("got %d satisfying, witness %v; want 0, nil", s.Satisfying, s.Witness)
	}
}

func TestCheckRespectsLimit(t *testing.T) {
	if _, err := Check(toyInstance(t), []string{"c1"}, 1); err == nil {
		t.Error("expected limit error")
	}
}
