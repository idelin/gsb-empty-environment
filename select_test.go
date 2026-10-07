package testselect

import (
	"reflect"
	"strings"
	"testing"
)

func selectIDs(t *testing.T, tests []Test, faults []string) *Result {
	t.Helper()
	res, err := Select(tests, faults)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	return res
}

func TestMinCostSelection(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "a", Cost: 5, Covers: []string{"f1"}},
		{ID: "b", Cost: 3, Covers: []string{"f1", "f2"}},
		{ID: "c", Cost: 2, Covers: []string{"f2"}},
	}, []string{"f1", "f2"})
	if !reflect.DeepEqual(res.Selected, []string{"b"}) {
		t.Fatalf("Selected = %v, want [b]", res.Selected)
	}
	if res.TotalCost != 3 {
		t.Fatalf("TotalCost = %d, want 3", res.TotalCost)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("Missing = %v, want none", res.Missing)
	}
}

func TestPrerequisiteClosureBilled(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "setup", Cost: 4, Covers: nil},
		{ID: "main", Cost: 1, Covers: []string{"f1"}, Requires: []string{"setup"}},
		{ID: "solo", Cost: 6, Covers: []string{"f1"}},
	}, []string{"f1"})
	// main(1)+setup(4)=5 beats solo(6); the prerequisite is billed.
	if !reflect.DeepEqual(res.Selected, []string{"main", "setup"}) {
		t.Fatalf("Selected = %v, want [main setup]", res.Selected)
	}
	if res.TotalCost != 5 {
		t.Fatalf("TotalCost = %d, want 5", res.TotalCost)
	}
}

func TestTransitivePrerequisites(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "t1", Cost: 1, Requires: []string{"t2"}},
		{ID: "t2", Cost: 1, Requires: []string{"t3"}},
		{ID: "t3", Cost: 1, Covers: []string{"f1"}},
	}, []string{"f1"})
	if !reflect.DeepEqual(res.Selected, []string{"t3"}) {
		t.Fatalf("Selected = %v, want [t3]", res.Selected)
	}
	res = selectIDs(t, []Test{
		{ID: "t1", Cost: 1, Covers: []string{"f1"}, Requires: []string{"t2"}},
		{ID: "t2", Cost: 2, Requires: []string{"t3"}},
		{ID: "t3", Cost: 4},
	}, []string{"f1"})
	if !reflect.DeepEqual(res.Selected, []string{"t1", "t2", "t3"}) {
		t.Fatalf("Selected = %v, want [t1 t2 t3]", res.Selected)
	}
	if res.TotalCost != 7 {
		t.Fatalf("TotalCost = %d, want 7", res.TotalCost)
	}
}

func TestMutualExclusion(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "a", Cost: 1, Covers: []string{"f1"}, Excludes: []string{"b"}},
		{ID: "b", Cost: 10, Covers: []string{"f1", "f2"}},
		{ID: "c", Cost: 5, Covers: []string{"f2"}},
	}, []string{"f1", "f2"})
	// a and b cannot coexist, so f2 must come from c.
	if !reflect.DeepEqual(res.Selected, []string{"a", "c"}) {
		t.Fatalf("Selected = %v, want [a c]", res.Selected)
	}
	if res.TotalCost != 6 {
		t.Fatalf("TotalCost = %d, want 6", res.TotalCost)
	}
}

func TestExclusionIsSymmetric(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "a", Cost: 1, Covers: []string{"f1"}},
		{ID: "b", Cost: 1, Covers: []string{"f2"}, Excludes: []string{"a"}},
	}, []string{"f1", "f2"})
	// a and b cannot coexist, so only one fault is coverable; the
	// equal-cost tie is broken by ID, selecting a.
	if !reflect.DeepEqual(res.Selected, []string{"a"}) {
		t.Fatalf("Selected = %v, want [a]", res.Selected)
	}
	if !reflect.DeepEqual(res.Missing, []string{"f2"}) {
		t.Fatalf("Missing = %v, want [f2]", res.Missing)
	}
}

func TestExclusionViaPrerequisite(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "a", Cost: 1, Covers: []string{"f1"}, Requires: []string{"env"}},
		{ID: "env", Cost: 1, Excludes: []string{"b"}},
		{ID: "b", Cost: 1, Covers: []string{"f2"}},
	}, []string{"f1", "f2"})
	// a drags in env, which conflicts with b: full coverage is impossible.
	if len(res.Missing) != 1 {
		t.Fatalf("Missing = %v, want exactly one missing fault", res.Missing)
	}
}

func TestMissingFaults(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "a", Cost: 1, Covers: []string{"f1"}},
	}, []string{"f1", "f2", "f3"})
	if !reflect.DeepEqual(res.Missing, []string{"f2", "f3"}) {
		t.Fatalf("Missing = %v, want [f2 f3]", res.Missing)
	}
	if !reflect.DeepEqual(res.Selected, []string{"a"}) {
		t.Fatalf("Selected = %v, want [a]", res.Selected)
	}
}

func TestDuplicateCoverage(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "cheap", Cost: 2, Covers: []string{"f1", "f2"}},
		{ID: "pricey", Cost: 9, Covers: []string{"f1", "f2"}},
	}, []string{"f1", "f2"})
	if !reflect.DeepEqual(res.Selected, []string{"cheap"}) {
		t.Fatalf("Selected = %v, want [cheap]", res.Selected)
	}
	// Overlapping coverage is still required when one test alone is not enough.
	res = selectIDs(t, []Test{
		{ID: "x", Cost: 2, Covers: []string{"f1", "f2"}},
		{ID: "y", Cost: 2, Covers: []string{"f2", "f3"}},
	}, []string{"f1", "f2", "f3"})
	if !reflect.DeepEqual(res.Selected, []string{"x", "y"}) {
		t.Fatalf("Selected = %v, want [x y]", res.Selected)
	}
	if res.TotalCost != 4 {
		t.Fatalf("TotalCost = %d, want 4", res.TotalCost)
	}
}

func TestTieBreakByID(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "zeta", Cost: 3, Covers: []string{"f1"}},
		{ID: "alpha", Cost: 3, Covers: []string{"f1"}},
		{ID: "mid", Cost: 3, Covers: []string{"f1"}},
	}, []string{"f1"})
	if !reflect.DeepEqual(res.Selected, []string{"alpha"}) {
		t.Fatalf("Selected = %v, want [alpha]", res.Selected)
	}
	// Equal cost across multi-test sets: smaller sorted ID list wins.
	res = selectIDs(t, []Test{
		{ID: "b1", Cost: 1, Covers: []string{"f1"}},
		{ID: "b2", Cost: 2, Covers: []string{"f2"}},
		{ID: "a1", Cost: 1, Covers: []string{"f1"}},
		{ID: "a2", Cost: 2, Covers: []string{"f2"}},
	}, []string{"f1", "f2"})
	if !reflect.DeepEqual(res.Selected, []string{"a1", "a2"}) {
		t.Fatalf("Selected = %v, want [a1 a2]", res.Selected)
	}
}

func TestPrefersMoreCoverageOverCost(t *testing.T) {
	res := selectIDs(t, []Test{
		{ID: "cheap", Cost: 1, Covers: []string{"f1"}},
		{ID: "full", Cost: 10, Covers: []string{"f1", "f2"}},
	}, []string{"f1", "f2"})
	if !reflect.DeepEqual(res.Selected, []string{"full"}) {
		t.Fatalf("Selected = %v, want [full]", res.Selected)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("Missing = %v, want none", res.Missing)
	}
}

func TestNoTests(t *testing.T) {
	res := selectIDs(t, nil, []string{"f1"})
	if len(res.Selected) != 0 || res.TotalCost != 0 {
		t.Fatalf("Selected = %v cost = %d, want empty", res.Selected, res.TotalCost)
	}
	if !reflect.DeepEqual(res.Missing, []string{"f1"}) {
		t.Fatalf("Missing = %v, want [f1]", res.Missing)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		tests []Test
		want  string
	}{
		{"empty id", []Test{{ID: "", Cost: 1}}, "empty ID"},
		{"negative cost", []Test{{ID: "a", Cost: -1}}, "negative cost"},
		{"duplicate id", []Test{{ID: "a"}, {ID: "a"}}, "duplicate"},
		{"unknown require", []Test{{ID: "a", Requires: []string{"ghost"}}}, "unknown test"},
		{"unknown exclude", []Test{{ID: "a", Excludes: []string{"ghost"}}}, "unknown test"},
	}
	for _, tc := range cases {
		if _, err := Select(tc.tests, []string{"f1"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want substring %q", tc.name, err, tc.want)
		}
	}
	tooMany := make([]Test, MaxTests+1)
	for i := range tooMany {
		tooMany[i] = Test{ID: strings.Repeat("t", i+1), Cost: 1}
	}
	if _, err := Select(tooMany, nil); err == nil || !strings.Contains(err.Error(), "exceed the limit") {
		t.Errorf("too many tests: err = %v, want limit error", err)
	}
}

func TestExactlyMaxTests(t *testing.T) {
	tests := make([]Test, MaxTests)
	faults := make([]string, MaxTests)
	for i := range tests {
		id := strings.Repeat("x", i+1)
		tests[i] = Test{ID: id, Cost: 1, Covers: []string{id}}
		faults[i] = id
	}
	res := selectIDs(t, tests, faults)
	if len(res.Selected) != MaxTests || res.TotalCost != int64(MaxTests) {
		t.Fatalf("Selected %d tests cost %d, want %d tests cost %d",
			len(res.Selected), res.TotalCost, MaxTests, MaxTests)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("Missing = %v, want none", res.Missing)
	}
}
