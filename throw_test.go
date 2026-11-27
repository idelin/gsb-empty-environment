package minijit

import (
	"errors"
	"testing"
)

// f3 overflows (deopt) then throws, with no local handler.
// f2 wraps f3 without a handler.
// f1 has a try range covering the f2 call and catches at the bottom.
// main has its own try around f1 for the uncaught variant.
func buildThrowProgram() *Program {
	prog := NewProgram()

	f3 := newBuilder("f3", 1, 1)
	f3.loadLocal(0).loadConst(intConst(maxInt64())).add() // deopt point
	f3.loadConst(strConst("boom")).throw()
	f3.loadConst(strConst("unreachable")).ret()
	prog.Add(f3.function())

	f2 := newBuilder("f2", 1, 1)
	f2.loadConst(FunctionValue(f3.function())).loadLocal(0).call(1).ret()
	prog.Add(f2.function())

	// f1(doCatch): local0=doCatch, local1=caught
	f1 := newBuilder("f1", 1, 2)
	tryStart := f1.here()
	f1.loadConst(FunctionValue(f2.function())).loadLocal(0).call(1).storeLocal(1)
	f1.loadConst(strConst("no-throw")).ret()
	tryEnd := f1.here()
	// catch target:
	catchPC := f1.here()
	// The thrown value is already on top of the operand stack; return it.
	f1.ret()
	f1.handler(tryStart, tryEnd, catchPC)
	prog.Add(f1.function())

	mainCaught := newBuilder("mainCaught", 0, 1)
	mainCaught.loadConst(FunctionValue(f1.function())).loadConst(BoolValue(true)).call(1).ret()
	prog.Add(mainCaught.function())

	// f1Uncaught never registers a handler.
	f1U := newBuilder("f1Uncaught", 1, 1)
	f1U.loadConst(FunctionValue(f2.function())).loadLocal(0).call(1).ret()
	prog.Add(f1U.function())

	mainUncaught := newBuilder("mainUncaught", 0, 1)
	mainUncaught.loadConst(FunctionValue(f1U.function())).loadConst(BoolValue(false)).call(1).ret()
	prog.Add(mainUncaught.function())
	return prog
}

func TestDeoptThenThrowCaughtAcrossReconstructedFrames(t *testing.T) {
	prog := buildThrowProgram()

	var deoptDepth int
	runner := NewOptimizedRunner(prog)
	runner.OnDeopt = func(reason string, depth int) { deoptDepth = depth }
	v, err := runner.Run("mainCaught", nil)
	if err != nil {
		t.Fatalf("exception should have been caught: %v", err)
	}
	if runner.DeoptCount() != 1 {
		t.Fatalf("want 1 deopt, got %d", runner.DeoptCount())
	}
	if deoptDepth < 4 {
		t.Fatalf("throw must unwind reconstructed 4+ frames, saw depth %d", deoptDepth)
	}
	if !v.IsString() || v.AsString() != "boom" {
		t.Fatalf("caught value = %s, want boom", v)
	}

	// Equivalent to pure interpreter from-scratch execution.
	base := runInterp(t, prog, "mainCaught", nil)
	if !ResultsEquivalent(base, v) {
		t.Fatal("caught result differs from interpreter baseline")
	}
}

func TestDeoptThenThrowUncaught(t *testing.T) {
	prog := buildThrowProgram()
	runner := NewOptimizedRunner(prog)
	_, err := runner.Run("mainUncaught", nil)
	if err == nil {
		t.Fatal("expected an uncaught throw error")
	}
	var ute *UncaughtThrowError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UncaughtThrowError, got %T: %v", err, err)
	}
	if ute.Value.AsString() != "boom" {
		t.Fatalf("uncaught value = %s, want boom", ute.Value)
	}
}
