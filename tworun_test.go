package iflow

import "testing"

// Two-run observational check: run the same program with two different
// secret inputs (public inputs fixed) and compare sink observations.
// A differing sink is a concrete witness of a leak for that input pair.
// These checks validate the simple examples dynamically; they do not and
// cannot establish non-interference in general.

func mustRun(t *testing.T, prog *Program, inputs map[string]int64) map[string]int64 {
	t.Helper()
	out, err := Run(prog, inputs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out
}

func TestTwoRunExplicitLeak(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			Assign("x", V("secret")),
			Sink("out", "public", V("x")),
		},
	}
	run1 := mustRun(t, prog, map[string]int64{"secret": 1})
	run2 := mustRun(t, prog, map[string]int64{"secret": 2})
	diffs := CompareRuns(run1, run2)
	if len(diffs) != 1 || diffs[0].Sink != "out" {
		t.Fatalf("expected observable difference at sink out, got %v", diffs)
	}
	// The static analysis independently flags the same sink.
	if Analyze(prog, lat).Sinks[0].Allowed {
		t.Fatal("static analysis should deny this sink")
	}
}

func TestTwoRunImplicitLeak(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			Assign("x", Const(0)),
			If(Bin("gt", V("secret"), Const(0)),
				[]Instr{Assign("x", Const(1))}, nil),
			Sink("out", "public", V("x")),
		},
	}
	run1 := mustRun(t, prog, map[string]int64{"secret": 0})
	run2 := mustRun(t, prog, map[string]int64{"secret": 5})
	if len(CompareRuns(run1, run2)) != 1 {
		t.Fatal("implicit leak should be observable across the two runs")
	}
	if Analyze(prog, lat).Sinks[0].Allowed {
		t.Fatal("static analysis should deny this sink")
	}
}

func TestTwoRunNonInterferingProgram(t *testing.T) {
	lat := chainLattice(t)
	// The secret only influences a dead temporary; the sink observes a
	// constant. The two runs agree, while the static analysis still denies
	// the sink because the pc label is not dropped at branch end. This
	// documents the deliberate over-approximation: the analysis is
	// conservative and does NOT claim to decide non-interference precisely.
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			If(Bin("gt", V("secret"), Const(0)),
				[]Instr{Assign("tmp", Const(1))}, nil),
			Assign("result", Const(42)),
			Sink("out", "public", V("result")),
		},
	}
	run1 := mustRun(t, prog, map[string]int64{"secret": 0})
	run2 := mustRun(t, prog, map[string]int64{"secret": 9})
	if len(CompareRuns(run1, run2)) != 0 {
		t.Fatal("runs should be indistinguishable at the sink")
	}
	if Analyze(prog, lat).Sinks[0].Allowed {
		t.Fatal("conservative analysis still denies (documented false positive)")
	}
}
