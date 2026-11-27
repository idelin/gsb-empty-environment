package minijit

import (
	"strings"
	"testing"
)

// f(o): mutates o's hidden class then hits a guard expecting the old class.
// The optimized tier deoptimizes at the guard; the dynamic tier resumes and
// returns 42.
func buildHiddenClassProgram() *Program {
	prog := NewProgram()

	f := newBuilder("f", 1, 1)
	f.loadLocal(0).loadConst(strConst("B")).transition()
	f.loadLocal(0).loadConst(strConst("A")).guardClass()
	f.loadConst(intConst(42)).ret()
	prog.Add(f.function())

	// main(): create o, call f, read o.k
	main := newBuilder("main", 0, 1)
	main.loadConst(strConst("A")).newObject().storeLocal(0)
	main.loadLocal(0).loadConst(intConst(1)).loadConst(strConst("k")).setField()
	main.loadConst(FunctionValue(f.function())).loadLocal(0).call(1).pop()
	main.loadLocal(0).loadConst(strConst("k")).getField().ret()
	prog.Add(main.function())
	return prog
}

func TestHiddenClassGuardDeopt(t *testing.T) {
	prog := buildHiddenClassProgram()

	base := runInterp(t, prog, "main", nil)

	runner := NewOptimizedRunner(prog)
	var reason string
	runner.OnDeopt = func(r string, depth int) { reason = r }
	opt, err := runner.Run("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if runner.DeoptCount() != 1 {
		t.Fatalf("want 1 deopt, got %d", runner.DeoptCount())
	}
	if !strings.HasPrefix(reason, "hidden class guard") {
		t.Fatalf("unexpected deopt reason %q", reason)
	}
	if !ResultsEquivalent(base, opt) {
		t.Fatal("hidden class deopt result diverged from interpreter")
	}
	if opt.AsInt() != 1 {
		t.Fatalf("field read after deopt = %s, want 1", opt)
	}

	// Force the uninlined path: same observable result, no deopt required.
	runner2 := NewOptimizedRunner(prog)
	runner2.MaxInlineDepth = 0
	opt2, err := runner2.Run("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ResultsEquivalent(base, opt2) {
		t.Fatal("uninlined path diverged from interpreter")
	}
}
