package plancmp

import (
	"reflect"
	"strings"
	"testing"
)

const (
	v0 = State(1) << 0
	v1 = State(1) << 1
	v2 = State(1) << 2
)

func TestMinimalSupplement(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "a", Set: v0},
			{Name: "b", Deps: []string{"a"}, Set: v1},
			{Name: "c", Set: v2},
		},
		GoalRequire: v1,
	}
	res, err := Diff(&Input{
		Initial:   v0,
		Executed:  []string{"a"},
		Committed: v0,
		New:       plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable || !res.Verified {
		t.Fatalf("expected reachable and verified, got %+v", res)
	}
	if !reflect.DeepEqual(res.Added, []string{"b"}) {
		t.Fatalf("expected minimal supplement [b], got %v", res.Added)
	}
	if res.Final != v0|v1 {
		t.Fatalf("expected final state %b, got %b", v0|v1, res.Final)
	}
}

func TestDependencyOrder(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "b", Deps: []string{"a"}, Require: v0, Set: v1},
			{Name: "a", Set: v0},
		},
		GoalRequire: v1,
	}
	res, err := Diff(&Input{New: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"a", "b"}) {
		t.Fatalf("expected ordered supplement [a b], got %v", res.Added)
	}
}

func TestMinimalityPrefersShorterPath(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "y", Set: v0},
			{Name: "z", Deps: []string{"y"}, Set: v1},
			{Name: "x", Set: v0 | v1},
		},
		GoalRequire: v0 | v1,
	}
	res, err := Diff(&Input{New: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"x"}) {
		t.Fatalf("expected single-op supplement [x], got %v", res.Added)
	}
}

func TestCommitmentCannotBeReset(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "reset", Clear: v0, Set: v1},
		},
		GoalRequire: v1,
	}
	res, err := Diff(&Input{
		Initial:   v0,
		Executed:  []string{"old"},
		Committed: v0,
		New:       plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reachable {
		t.Fatalf("expected unreachable, got %+v", res)
	}
	if !reflect.DeepEqual(res.Explored, []State{v0}) {
		t.Fatalf("expected explored range [%b], got %v", v0, res.Explored)
	}
}

func TestUnreachableReturnsCompleteSearchRange(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "a", Set: v0},
			{Name: "b", Deps: []string{"a"}, Require: v2, Set: v1},
		},
		GoalRequire: v1,
	}
	res, err := Diff(&Input{New: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reachable {
		t.Fatalf("expected unreachable, got %+v", res)
	}
	want := []State{0, v0}
	if !reflect.DeepEqual(res.Explored, want) {
		t.Fatalf("expected explored range %v, got %v", want, res.Explored)
	}
}

func TestRequireAllOps(t *testing.T) {
	plan := &Plan{
		Ops: []Op{
			{Name: "a", Set: v0},
			{Name: "b", Set: v1},
		},
		GoalRequire: v0,
		RequireAll:  true,
	}
	res, err := Diff(&Input{New: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable || len(res.Added) != 2 {
		t.Fatalf("expected both operations required, got %+v", res)
	}
}

func TestAlreadyAtGoal(t *testing.T) {
	plan := &Plan{
		Ops:         []Op{{Name: "a", Set: v0}},
		GoalRequire: v0,
	}
	res, err := Diff(&Input{Initial: v0, New: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable || len(res.Added) != 0 || res.Final != v0 {
		t.Fatalf("expected empty supplement, got %+v", res)
	}
}

func TestReplayDetectsSilentReset(t *testing.T) {
	plan := &Plan{Ops: []Op{{Name: "reset", Clear: v0, Set: v1}}}
	_, err := Replay(v0, v0, plan, nil, []string{"reset"})
	if err == nil || !strings.Contains(err.Error(), "silently resets") {
		t.Fatalf("expected silent reset violation, got %v", err)
	}
}

func TestReplayDetectsDependencyViolation(t *testing.T) {
	plan := &Plan{Ops: []Op{
		{Name: "a", Set: v0},
		{Name: "b", Deps: []string{"a"}, Set: v1},
	}}
	_, err := Replay(0, 0, plan, nil, []string{"b"})
	if err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("expected dependency violation, got %v", err)
	}
}

func TestReplayAcceptsExecutedPrefix(t *testing.T) {
	plan := &Plan{Ops: []Op{
		{Name: "a", Set: v0},
		{Name: "b", Deps: []string{"a"}, Require: v0, Set: v1},
	}}
	final, err := Replay(v0, v0, plan, []string{"a"}, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if final != v0|v1 {
		t.Fatalf("expected final state %b, got %b", v0|v1, final)
	}
}

func TestValidationRejectsTooManyVariables(t *testing.T) {
	plan := &Plan{Ops: []Op{{Name: "a", Set: State(1) << MaxVars}}}
	if _, err := Diff(&Input{New: plan}); err == nil {
		t.Fatal("expected validation error for variable beyond MaxVars")
	}
	if _, err := Diff(&Input{Initial: State(1) << MaxVars, New: &Plan{}}); err == nil {
		t.Fatal("expected validation error for initial state beyond MaxVars")
	}
}

func TestValidationRejectsUnknownDependency(t *testing.T) {
	plan := &Plan{Ops: []Op{{Name: "a", Deps: []string{"ghost"}}}}
	if _, err := Diff(&Input{New: plan}); err == nil {
		t.Fatal("expected validation error for unknown dependency")
	}
}
