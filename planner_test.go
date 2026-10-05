package main

import (
	"bytes"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Problem {
	t.Helper()
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	return p
}

func TestParseBasic(t *testing.T) {
	p := mustParse(t, `
props a b c
init a
goal c
action go
  pre a
  add c
  del a
  cost 4
`)
	if len(p.PropNames) != 3 || len(p.Actions) != 1 {
		t.Fatalf("got %d props, %d actions", len(p.PropNames), len(p.Actions))
	}
	a := p.Actions[0]
	if a.Cost != 4 || a.Pre != 1 || a.Add != 4 || a.Del != 1 {
		t.Fatalf("bad action: %+v", a)
	}
	if p.Init != 1 || p.Goal != 4 {
		t.Fatalf("init=%b goal=%b", p.Init, p.Goal)
	}
}

func TestParseRejectsTooManyProps(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("props")
	for i := 0; i < MaxProps+1; i++ {
		sb.WriteString(" p")
		sb.WriteString(strings.Repeat("x", 1))
		sb.WriteString(string(rune('a' + i)))
	}
	if _, err := Parse(strings.NewReader(sb.String())); err == nil {
		t.Fatal("expected error for >20 propositions")
	}
}

func TestParseRejectsNegativeCost(t *testing.T) {
	_, err := Parse(strings.NewReader("props a\naction x\n  cost -1\n"))
	if err == nil {
		t.Fatal("expected error for negative cost")
	}
}

func TestSolveFindsMinCost(t *testing.T) {
	p := mustParse(t, `
props a b c
init a
goal c
action cheap1
  pre a
  add b
  del a
  cost 1
action cheap2
  pre b
  add c
  cost 1
action direct
  pre a
  add c
  cost 5
`)
	res := Solve(p)
	if !res.Reached || res.Cost != 2 {
		t.Fatalf("reached=%v cost=%d", res.Reached, res.Cost)
	}
	if got := strings.Join(res.Plan, ","); got != "cheap1,cheap2" {
		t.Fatalf("plan=%v", res.Plan)
	}
}

func TestZeroCostCycleTerminates(t *testing.T) {
	p := mustParse(t, `
props a b c
init a
goal c
action ping
  pre a
  add b
  del a
  cost 0
action pong
  pre b
  add a
  del b
  cost 0
action finish
  pre b
  add c
  cost 3
`)
	res := Solve(p)
	if !res.Reached || res.Cost != 3 {
		t.Fatalf("reached=%v cost=%d", res.Reached, res.Cost)
	}
	if got := strings.Join(res.Plan, ","); got != "ping,finish" {
		t.Fatalf("plan=%v", res.Plan)
	}
}

func TestUnreachableReportsExplored(t *testing.T) {
	p := mustParse(t, `
props a b
init a
goal b
action spin
  pre a
  add a
  cost 0
`)
	res := Solve(p)
	if res.Reached {
		t.Fatal("should be unreachable")
	}
	if len(res.Explored) != 1 || res.Explored[0].State != p.Init {
		t.Fatalf("explored=%+v", res.Explored)
	}
}

func TestInitAlreadyGoal(t *testing.T) {
	p := mustParse(t, "props a\ninit a\ngoal a\n")
	res := Solve(p)
	if !res.Reached || res.Cost != 0 || len(res.Plan) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

func TestReplayValidPlan(t *testing.T) {
	p := mustParse(t, `
props a b
init a
goal b
action go
  pre a
  add b
  del a
  cost 2
`)
	var out bytes.Buffer
	if err := Replay(p, []string{"go"}, &out); err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if !strings.Contains(out.String(), "goal reached") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestReplayDetectsPreconditionViolation(t *testing.T) {
	p := mustParse(t, `
props a b
init a
goal b
action needs_b
  pre b
  add a
  cost 1
`)
	var out bytes.Buffer
	err := Replay(p, []string{"needs_b"}, &out)
	if err == nil || !strings.Contains(err.Error(), "preconditions") {
		t.Fatalf("expected precondition error, got %v", err)
	}
}

func TestReplayDetectsGoalMiss(t *testing.T) {
	p := mustParse(t, `
props a b
init a
goal b
action noop
  pre a
  cost 0
`)
	var out bytes.Buffer
	err := Replay(p, []string{"noop"}, &out)
	if err == nil || !strings.Contains(err.Error(), "goal") {
		t.Fatalf("expected goal error, got %v", err)
	}
}
