package main

import (
	"strings"
	"testing"
)

func mustCompile(t *testing.T, p *Problem) *Problem {
	t.Helper()
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

// 两条路径到达目标，应选择成本更低的一条。
func TestSolveChoosesCheapest(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "b", "g"},
		Initial:      []string{"a"},
		Goal:         []string{"g"},
		Actions: []Action{
			{Name: "expensive", Cost: 10, Pre: []string{"a"}, Add: []string{"g"}},
			{Name: "step1", Cost: 1, Pre: []string{"a"}, Add: []string{"b"}},
			{Name: "step2", Cost: 1, Pre: []string{"b"}, Add: []string{"g"}},
		},
	})
	res := Solve(p)
	if !res.Found {
		t.Fatal("expected plan")
	}
	if res.Plan.Cost != 2 {
		t.Fatalf("cost = %d, want 2", res.Plan.Cost)
	}
	want := []string{"step1", "step2"}
	if strings.Join(res.Plan.Steps, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", res.Plan.Steps, want)
	}
	if _, err := Replay(p, res.Plan); err != nil {
		t.Fatalf("replay of found plan failed: %v", err)
	}
}

// 零成本环必须终止：a<->b 均为零成本动作。
func TestZeroCostCycleTerminates(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "b", "g"},
		Initial:      []string{"a"},
		Goal:         []string{"g"},
		Actions: []Action{
			{Name: "to-b", Cost: 0, Pre: []string{"a"}, Add: []string{"b"}, Del: []string{"a"}},
			{Name: "to-a", Cost: 0, Pre: []string{"b"}, Add: []string{"a"}, Del: []string{"b"}},
			{Name: "finish", Cost: 0, Pre: []string{"b"}, Add: []string{"g"}},
		},
	})
	res := Solve(p) // 若零成本环导致无限搜索，此测试将超时
	if !res.Found || res.Plan.Cost != 0 {
		t.Fatalf("found=%v cost=%d", res.Found, res.Plan.Cost)
	}
	if _, err := Replay(p, res.Plan); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

// 不可达时应返回已探索状态摘要。
func TestUnreachableSummary(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "b", "g"},
		Initial:      []string{"a"},
		Goal:         []string{"g"},
		Actions: []Action{
			{Name: "wander", Cost: 3, Pre: []string{"a"}, Add: []string{"b"}},
		},
	})
	res := Solve(p)
	if res.Found {
		t.Fatal("expected unreachable")
	}
	if len(res.Explored) == 0 {
		t.Fatal("expected explored states")
	}
	summary := p.UnreachableSummary(res, 10)
	if !strings.Contains(summary, "目标不可达") || !strings.Contains(summary, "已探索状态数") {
		t.Fatalf("summary missing info:\n%s", summary)
	}
	if !strings.Contains(summary, "+g") {
		t.Fatalf("summary should report unmet goal proposition:\n%s", summary)
	}
}

// 回放器必须拒绝前置条件不满足的计划。
func TestReplayRejectsBadPrecondition(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "g"},
		Initial:      []string{"a"},
		Goal:         []string{"g"},
		Actions: []Action{
			{Name: "need-g", Cost: 1, Pre: []string{"g"}, Add: []string{"a"}},
		},
	})
	_, err := Replay(p, Plan{Steps: []string{"need-g"}})
	if err == nil || !strings.Contains(err.Error(), "前置条件") {
		t.Fatalf("expected precondition failure, got %v", err)
	}
}

// 回放器必须拒绝未达成目标的计划。
func TestReplayRejectsGoalNotMet(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "g"},
		Initial:      []string{"a"},
		Goal:         []string{"g"},
		Actions: []Action{
			{Name: "noop", Cost: 1, Pre: []string{"a"}, Add: []string{"a"}},
		},
	})
	_, err := Replay(p, Plan{Steps: []string{"noop"}})
	if err == nil || !strings.Contains(err.Error(), "目标未达成") {
		t.Fatalf("expected goal failure, got %v", err)
	}
}

// 超过 20 个命题必须报错。
func TestTooManyPropositions(t *testing.T) {
	props := make([]string, 21)
	for i := range props {
		props[i] = string(rune('a' + i))
	}
	p := &Problem{Propositions: props}
	if err := p.compile(); err == nil {
		t.Fatal("expected error for >20 propositions")
	}
}

// 负成本动作必须被拒绝。
func TestNegativeCostRejected(t *testing.T) {
	p := &Problem{
		Propositions: []string{"a"},
		Actions:      []Action{{Name: "bad", Cost: -1}},
	}
	if err := p.compile(); err == nil {
		t.Fatal("expected error for negative cost")
	}
}

// 负前置条件与负目标应被正确处理。
func TestNegativeConditions(t *testing.T) {
	p := mustCompile(t, &Problem{
		Propositions: []string{"a", "b"},
		Initial:      []string{"a"},
		GoalNeg:      []string{"a"},
		Actions: []Action{
			{Name: "clear", Cost: 1, Pre: []string{"a"}, PreNeg: []string{"b"}, Del: []string{"a"}},
		},
	})
	res := Solve(p)
	if !res.Found || res.Plan.Cost != 1 {
		t.Fatalf("found=%v cost=%d", res.Found, res.Plan.Cost)
	}
	if _, err := Replay(p, res.Plan); err != nil {
		t.Fatalf("replay: %v", err)
	}
}
