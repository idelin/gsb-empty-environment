package netsec

import (
	"path"
	"strings"
	"testing"
)

func findAdded(t *testing.T, r *Report, want Capability) CapabilityDiff {
	t.Helper()
	for _, d := range r.Added {
		if d.Capability == want {
			return d
		}
	}
	t.Fatalf("capability %v not found in additions %v", want, r.Added)
	return CapabilityDiff{}
}

func TestBasicAddition(t *testing.T) {
	oldPlan := Seq{Nodes: []Node{Read{Path: "/etc/a"}}}
	newPlan := Seq{Nodes: []Node{
		Read{Path: "/etc/a"},
		Write{Path: "/etc/b"},
	}}
	r, err := Diff(oldPlan, newPlan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 1 {
		t.Fatalf("expected 1 addition, got %v", r.Added)
	}
	d := findAdded(t, r, Capability{Kind: WritePath, Path: "/etc/b"})
	if len(d.AddedUnder) != 1 || d.AddedUnder[0] != Assignment(0) {
		t.Fatalf("unexpected assignments %v", d.AddedUnder)
	}
	if got := r.FeasiblePaths; len(got) != 2 || got[0] != "/etc/a" || got[1] != "/etc/b" {
		t.Fatalf("unexpected feasible paths %v", got)
	}
}

func TestConditionalAddition(t *testing.T) {
	newPlan := If{
		Cond: Var(0),
		Then: Call{Func: "net.dial"},
	}
	r, err := Diff(nil, newPlan, Options{Conditions: 2})
	if err != nil {
		t.Fatal(err)
	}
	d := findAdded(t, r, Capability{Kind: CallFunc, Func: "net.dial"})
	// Var(0) holds under assignments 01 and 11.
	if len(d.AddedUnder) != 2 || d.AddedUnder[0] != 1 || d.AddedUnder[1] != 3 {
		t.Fatalf("unexpected assignments %v", d.AddedUnder)
	}
	if len(r.ByAssignment) != 2 {
		t.Fatalf("expected 2 assignment reports, got %d", len(r.ByAssignment))
	}
}

func TestUnreachableDangerousExcluded(t *testing.T) {
	dangerous := func(c Capability) bool {
		return c.Kind == WritePath && strings.HasPrefix(c.Path, "/root")
	}
	// Contradictory guard: the write is syntactically present but no
	// assignment reaches it.
	newPlan := If{
		Cond: And{L: Var(0), R: Not{C: Var(0)}},
		Then: Write{Path: "/root/x"},
		Else: Read{Path: "/etc/ok"},
	}
	r, err := Diff(nil, newPlan, Options{Conditions: 1, IsDangerous: dangerous})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Added {
		if d.Capability.Kind == WritePath {
			t.Fatalf("unreachable dangerous write counted as addition: %v", d)
		}
	}
	if len(r.UnreachableDangerous) != 1 ||
		r.UnreachableDangerous[0] != (Capability{Kind: WritePath, Path: "/root/x"}) {
		t.Fatalf("unexpected unreachable dangerous %v", r.UnreachableDangerous)
	}
	if got := r.FeasiblePaths; len(got) != 1 || got[0] != "/etc/ok" {
		t.Fatalf("unexpected feasible paths %v", got)
	}
}

func TestReachableDangerousFlagged(t *testing.T) {
	dangerous := func(c Capability) bool {
		return c.Kind == WritePath && strings.HasPrefix(c.Path, "/root")
	}
	newPlan := If{Cond: Var(0), Then: Write{Path: "/root/x"}}
	r, err := Diff(nil, newPlan, Options{Conditions: 1, IsDangerous: dangerous})
	if err != nil {
		t.Fatal(err)
	}
	d := findAdded(t, r, Capability{Kind: WritePath, Path: "/root/x"})
	if !d.Dangerous {
		t.Fatal("reachable dangerous write not flagged")
	}
	if len(d.AddedUnder) != 1 || d.AddedUnder[0] != 1 {
		t.Fatalf("unexpected assignments %v", d.AddedUnder)
	}
	if len(r.UnreachableDangerous) != 0 {
		t.Fatalf("reachable write misreported as unreachable: %v", r.UnreachableDangerous)
	}
}

func TestNormalizationIdentity(t *testing.T) {
	normalize := func(p string) string { return path.Clean(p) }
	oldPlan := Write{Path: "/var/log/../log/app"}
	newPlan := Write{Path: "/var/log/app"}
	r, err := Diff(oldPlan, newPlan, Options{Normalize: normalize})
	if err != nil {
		t.Fatal(err)
	}
	if r.HasAdditions() {
		t.Fatalf("normalized-equal paths reported as addition: %v", r.Added)
	}

	// Without normalization the raw spellings differ and the diff appears.
	r, err = Diff(oldPlan, newPlan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	findAdded(t, r, Capability{Kind: WritePath, Path: "/var/log/app"})
}

func TestLoopUnroll(t *testing.T) {
	newPlan := Loop{Count: 3, Body: Seq{Nodes: []Node{
		Write{Path: "/tmp/f"},
		Call{Func: "fsync"},
	}}}
	r, err := Diff(nil, newPlan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 2 {
		t.Fatalf("expected 2 additions, got %v", r.Added)
	}
	findAdded(t, r, Capability{Kind: WritePath, Path: "/tmp/f"})
	findAddedCall := findAdded(t, r, Capability{Kind: CallFunc, Func: "fsync"})
	if len(findAddedCall.AddedUnder) != 1 {
		t.Fatalf("unexpected assignments %v", findAddedCall.AddedUnder)
	}
}

func TestZeroCountLoopUnreachable(t *testing.T) {
	dangerous := func(Capability) bool { return true }
	newPlan := Loop{Count: 0, Body: Write{Path: "/never"}}
	r, err := Diff(nil, newPlan, Options{IsDangerous: dangerous})
	if err != nil {
		t.Fatal(err)
	}
	if r.HasAdditions() {
		t.Fatalf("zero-count loop body counted: %v", r.Added)
	}
	if len(r.UnreachableDangerous) != 1 {
		t.Fatalf("expected syntactic dangerous capability, got %v", r.UnreachableDangerous)
	}
}

func TestOldPlanCapabilityNotAdded(t *testing.T) {
	// The old plan already writes /etc/c under Var(0); the new plan writes
	// it unconditionally. The addition is effective only where the old
	// plan lacked it.
	oldPlan := If{Cond: Var(0), Then: Write{Path: "/etc/c"}}
	newPlan := Write{Path: "/etc/c"}
	r, err := Diff(oldPlan, newPlan, Options{Conditions: 1})
	if err != nil {
		t.Fatal(err)
	}
	d := findAdded(t, r, Capability{Kind: WritePath, Path: "/etc/c"})
	if len(d.AddedUnder) != 1 || d.AddedUnder[0] != 0 {
		t.Fatalf("unexpected assignments %v", d.AddedUnder)
	}
}

func TestConditionLimit(t *testing.T) {
	if _, err := Diff(nil, nil, Options{Conditions: MaxConditions + 1}); err == nil {
		t.Fatal("expected error for too many conditions")
	}
	if _, err := Diff(nil, If{Cond: Var(5), Then: Read{Path: "/x"}}, Options{Conditions: 2}); err == nil {
		t.Fatal("expected error for undeclared condition variable")
	}
	if _, err := Diff(nil, nil, Options{Conditions: MaxConditions}); err != nil {
		t.Fatalf("max conditions should be accepted: %v", err)
	}
}

func TestStepLimit(t *testing.T) {
	plan := Loop{Count: 100, Body: Loop{Count: 100, Body: Read{Path: "/x"}}}
	if _, err := Diff(nil, plan, Options{MaxSteps: 1000}); err == nil {
		t.Fatal("expected step limit error")
	}
	if _, err := Diff(nil, Loop{Count: -1, Body: nil}, Options{}); err == nil {
		t.Fatal("expected negative loop count error")
	}
}

func TestPerAssignmentFeasiblePaths(t *testing.T) {
	newPlan := Seq{Nodes: []Node{
		Read{Path: "/always"},
		If{Cond: Var(0), Then: Write{Path: "/sometimes"}},
	}}
	r, err := Diff(nil, newPlan, Options{Conditions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ByAssignment) != 2 {
		t.Fatalf("expected 2 assignment reports, got %d", len(r.ByAssignment))
	}
	for _, ar := range r.ByAssignment {
		want := []string{"/always"}
		if ar.Assignment.Bit(0) {
			want = []string{"/always", "/sometimes"}
		}
		if strings.Join(ar.FeasiblePaths, ",") != strings.Join(want, ",") {
			t.Fatalf("assignment %v: feasible paths %v, want %v", ar.Assignment, ar.FeasiblePaths, want)
		}
	}
}
