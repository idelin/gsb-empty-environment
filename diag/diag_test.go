package diag

import (
	"reflect"
	"strings"
	"testing"

	"configdiag/instance"
)

// toyInstance has two independent conflicts: {c1, c2} on a and {c3, c4}
// on b, plus a connecting constraint c5. Variables a, b range over {0, 1}.
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

func TestFindMUSInputOrder(t *testing.T) {
	res, err := FindMUS(toyInstance(t))
	if err != nil {
		t.Fatalf("FindMUS: %v", err)
	}
	if res.Status != "unsatisfiable" {
		t.Fatalf("status = %q, want unsatisfiable", res.Status)
	}
	// Deletion in input order: c1 and c2 are dropped because {c3, c4}
	// alone is already unsatisfiable; c3 and c4 are kept because each
	// removal leaves a satisfiable set; c5 is dropped at the end.
	wantMUS := []string{"c3", "c4"}
	if !reflect.DeepEqual(res.MUS, wantMUS) {
		t.Errorf("MUS = %v, want %v", res.MUS, wantMUS)
	}
	wantKept := []bool{false, false, true, true, false}
	if len(res.Steps) != len(wantKept) {
		t.Fatalf("got %d steps, want %d", len(res.Steps), len(wantKept))
	}
	for i, s := range res.Steps {
		if s.Kept != wantKept[i] {
			t.Errorf("step %d (%s): kept = %v, want %v", i, s.Constraint, s.Kept, wantKept[i])
		}
	}
}

func TestFindMUSSatisfiable(t *testing.T) {
	inst, err := instance.Parse([]byte(`{
	  "variables": [{"name": "a", "domain": ["0", "1"]}],
	  "constraints": [{"name": "c1", "vars": ["a"], "allowed": [["0"]]}]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := FindMUS(inst)
	if err != nil {
		t.Fatalf("FindMUS: %v", err)
	}
	if res.Status != "satisfiable" {
		t.Fatalf("status = %q, want satisfiable", res.Status)
	}
	if len(res.MUS) != 0 {
		t.Errorf("MUS = %v, want empty", res.MUS)
	}
}

func TestFindMUSSingleContradiction(t *testing.T) {
	inst, err := instance.Parse([]byte(`{
	  "variables": [{"name": "a", "domain": ["0", "1"]}],
	  "constraints": [
	    {"name": "impossible", "vars": ["a"], "allowed": []},
	    {"name": "anything", "vars": ["a"], "allowed": [["0"], ["1"]]}
	  ]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := FindMUS(inst)
	if err != nil {
		t.Fatalf("FindMUS: %v", err)
	}
	want := []string{"impossible"}
	if !reflect.DeepEqual(res.MUS, want) {
		t.Errorf("MUS = %v, want %v", res.MUS, want)
	}
}

func TestFindMUSRejectsTooManyConstraints(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"variables": [{"name": "a", "domain": ["0"]}], "constraints": [`)
	for i := 0; i < MaxConstraints+1; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`{"name": "c` + strings.Repeat("x", i+1) + `", "vars": ["a"], "allowed": [["0"]]}`)
	}
	b.WriteString(`]}`)
	inst, err := instance.Parse([]byte(b.String()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := FindMUS(inst); err == nil {
		t.Fatal("FindMUS accepted more than MaxConstraints constraints")
	}
}

func TestFirstSatisfyingEmptySet(t *testing.T) {
	inst := toyInstance(t)
	c := instance.Compile(inst)
	if _, ok := FirstSatisfying(c, nil); !ok {
		t.Error("empty constraint set should be satisfiable")
	}
}
