package netsec

import (
	"errors"
	"testing"
)

func TestAnalyzeFindsIrrecoverableDeadEnd(t *testing.T) {
	// Bit 0 must be set before bit 1 (the lock). Locking first leaves a
	// reachable state 0b10 from which bit 0 can never be set.
	p := Problem{
		Bits:     2,
		Initial:  0,
		Target:   0b11,
		LockMask: 0b10,
		Ops: []Operation{
			{Name: "grant", Mask: 0b01, Value: 0b01, RequireMask: 0b10, RequireValue: 0},
			{Name: "lock", Mask: 0b10, Value: 0b10},
		},
	}

	analysis, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Complete {
		t.Fatal("analysis should be complete for two bits")
	}
	if !analysis.TargetReachable {
		t.Fatal("target should be reachable")
	}
	if !analysis.DeadEnd(0b10) {
		t.Fatalf("0b10 should be a dead end; dead ends = %#b", analysis.DeadEnds)
	}
	if analysis.DeadEnd(0) || analysis.DeadEnd(0b11) {
		t.Fatalf("unexpected dead ends: %#b", analysis.DeadEnds)
	}
	if analysis.Reachable != 4 { // 00, 01, 10, 11
		t.Fatalf("reachable = %d, want 4", analysis.Reachable)
	}
}

func TestAnalyzeMatchesPlanReachability(t *testing.T) {
	for width := uint(1); width <= 6; width++ {
		for seed := uint32(0); seed < 64; seed++ {
			p := randomProblem(width, seed)
			result, err := Plan(p)
			if err != nil {
				t.Fatalf("width=%d seed=%d: %v", width, seed, err)
			}
			analysis, err := Analyze(p)
			if err != nil {
				t.Fatalf("width=%d seed=%d: %v", width, seed, err)
			}
			if result.Complete() != analysis.TargetReachable {
				t.Fatalf("width=%d seed=%d: plan=%s analysis=%v problem=%+v",
					width, seed, result.Status, analysis.TargetReachable, p)
			}
			if result.Complete() {
				assertPlanValid(t, p, result)
				// Every state on a shortest plan must be able to reach the
				// goal; a valid shortest plan cannot pass through a dead end.
				for _, state := range result.States {
					if analysis.DeadEnd(state) {
						t.Fatalf("width=%d seed=%d: plan passes through dead end %#b",
							width, seed, state)
					}
				}
			} else if len(analysis.DeadEnds) == 0 && analysis.Reachable > 0 {
				// If nothing is a dead end but the target is unreachable,
				// the initial state itself must be the dead end.
				if !analysis.DeadEnd(p.Initial) {
					t.Fatalf("width=%d seed=%d: unreachable without a dead end", width, seed)
				}
			}
		}
	}
}

func TestPlanShortestAgainstIndependentBFS(t *testing.T) {
	for width := uint(1); width <= 6; width++ {
		for seed := uint32(0); seed < 40; seed++ {
			p := randomProblem(width, seed)
			result, err := Plan(p)
			if err != nil {
				t.Fatalf("width=%d seed=%d: %v", width, seed, err)
			}
			wantSteps, wantOK := bruteShortest(p)
			if result.Complete() != wantOK {
				t.Fatalf("width=%d seed=%d: plan=%s brute=%v", width, seed, result.Status, wantOK)
			}
			if wantOK && len(result.Steps) != wantSteps {
				t.Fatalf("width=%d seed=%d: steps=%d want=%d", width, seed, len(result.Steps), wantSteps)
			}
		}
	}
}

func TestAnalyzeForbiddenInitial(t *testing.T) {
	p := Problem{
		Bits:      3,
		Initial:   0b010,
		Target:    0,
		Forbidden: []Condition{{Mask: 0b010, Value: 0b010}},
	}
	analysis, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.TargetReachable {
		t.Fatal("forbidden initial state must not reach target")
	}
	if analysis.Reachable != 0 {
		t.Fatalf("reachable = %d, want 0", analysis.Reachable)
	}
	if analysis.Reason != "initial state is forbidden" {
		t.Fatalf("reason = %q", analysis.Reason)
	}
}

func TestAnalyzeLimit(t *testing.T) {
	p := Problem{Bits: 10, Target: 1}
	if _, err := AnalyzeLimit(p, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := AnalyzeLimit(p, 1024); err != nil {
		t.Fatal(err)
	}
}

func TestFindDeadEnds(t *testing.T) {
	p := Problem{
		Bits:     2,
		Initial:  0,
		Target:   0b11,
		LockMask: 0b10,
		Ops: []Operation{
			{Name: "grant", Mask: 0b01, Value: 0b01, RequireMask: 0b10, RequireValue: 0},
			{Name: "lock", Mask: 0b10, Value: 0b10},
		},
	}
	got, err := FindDeadEnds(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 0b10 {
		t.Fatalf("dead ends = %#b, want [0b10]", got)
	}
}

// randomProblem builds a deterministic small problem with overlapping masks,
// locks, preconditions and forbidden states.
func randomProblem(width uint, seed uint32) Problem {
	full := uint32(1)<<width - 1
	next := func() uint32 {
		seed = seed*1664525 + 1013904223
		return seed
	}
	opCount := 1 + int(next()%5)
	ops := make([]Operation, opCount)
	for i := range ops {
		mask := next() & full
		ops[i] = Operation{
			Name:         "op",
			Mask:         mask,
			Value:        next() & mask,
			RequireMask:  next() & full,
			RequireValue: next() & full,
		}
	}
	lockMask := next() & full
	initial := next() & full
	target := next() & full
	p := Problem{
		Bits:     width,
		Initial:  initial,
		Target:   target,
		LockMask: lockMask,
		Ops:      ops,
	}
	if next()%2 == 0 {
		p.Forbidden = []Condition{{
			Mask:  next() & full,
			Value: next() & full,
		}}
	}
	return p
}

func bruteShortest(p Problem) (int, bool) {
	n, err := p.normalize()
	if err != nil || n.forbidden(p.Initial) {
		return 0, false
	}
	if n.goal(p.Initial) {
		return 0, true
	}
	distance := map[uint32]int{p.Initial: 0}
	queue := []uint32{p.Initial}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for _, op := range n.problem.Ops {
			next, ok := applyOp(state, op, n.locks, n.problem.Locks, n.globalFreeze)
			if !ok || next == state || n.forbidden(next) {
				continue
			}
			if _, seen := distance[next]; seen {
				continue
			}
			distance[next] = distance[state] + 1
			if n.goal(next) {
				return distance[next], true
			}
			queue = append(queue, next)
		}
	}
	return 0, false
}
