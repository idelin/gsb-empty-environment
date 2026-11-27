package minijit

import "testing"

func TestInterpreterOptimizedEquivalenceTable(t *testing.T) {
	programs := map[string]*Program{
		"inline": buildInlineProgram(),
		"throw":  buildThrowProgram(),
		"hidden": buildHiddenClassProgram(),
	}
	cases := []struct {
		prog  string
		entry string
		args  []Value
	}{
		{"inline", "main", []Value{IntValue(1)}},
		{"inline", "main", []Value{IntValue(0)}},
		{"inline", "main", []Value{IntValue(-7)}},
		{"hidden", "main", nil},
		{"throw", "mainCaught", nil},
	}
	for _, tc := range cases {
		prog := programs[tc.prog]
		base, err := NewInterpreter(prog).Run(tc.entry, tc.args)
		if err != nil {
			t.Fatalf("%s/%s baseline error: %v", tc.prog, tc.entry, err)
		}
		opt, err := NewOptimizedRunner(prog).Run(tc.entry, tc.args)
		if err != nil {
			t.Fatalf("%s/%s optimized error: %v", tc.prog, tc.entry, err)
		}
		if !ResultsEquivalent(base, opt) {
			t.Fatalf("%s/%s graph mismatch\nbase:\n%s\nopt:\n%s",
				tc.prog, tc.entry,
				CanonicalGraph(base).DebugString(),
				CanonicalGraph(opt).DebugString())
		}
	}
}

func TestNoDeoptWhenGuardsHold(t *testing.T) {
	prog := buildInlineProgram()
	// x = 0 cannot overflow MaxInt64 addition, so no guard should fail.
	r := NewOptimizedRunner(prog)
	v, err := r.Run("main", []Value{IntValue(0)})
	if err != nil {
		t.Fatal(err)
	}
	if r.DeoptCount() != 0 {
		t.Fatalf("expected no deoptimization, got %d", r.DeoptCount())
	}
	base := runInterp(t, prog, "main", []Value{IntValue(0)})
	if !ResultsEquivalent(base, v) {
		t.Fatal("non-deoptimized run diverged from baseline")
	}
}
