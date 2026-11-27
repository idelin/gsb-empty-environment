package minijit

import "testing"

// buildInlineProgram builds main -> f1 -> f2 -> f3 (three nested calls).
// f3 creates two objects u and v, wires u.self = u, v.other = u (a cycle and
// a shared reference across frames) and performs an int64-overflowing add that
// promotes to float only in the dynamic semantics.
func buildInlineProgram() *Program {
	prog := NewProgram()

	// f3(x): local0=x, local1=u, local2=v
	f3 := newBuilder("f3", 1, 3)
	f3.loadConst(strConst("U")).newObject().storeLocal(1) // u
	f3.loadConst(strConst("V")).newObject().storeLocal(2) // v
	// u.self = u
	f3.loadLocal(1).loadLocal(1).loadConst(strConst("self")).setField()
	// v.other = u  (u referenced from two virtual objects)
	f3.loadLocal(2).loadLocal(1).loadConst(strConst("other")).setField()
	// v.sum = x + (maxInt) : overflows for x = 1 -> float
	f3.loadLocal(2)
	f3.loadLocal(0).loadConst(intConst(maxInt64())).add()
	f3.loadConst(strConst("sum")).setField()
	f3.loadLocal(2).ret()
	prog.Add(f3.function())

	// f2(a): local0=a, local1=r
	f2 := newBuilder("f2", 1, 2)
	f2.loadConst(FunctionValue(f3.function())).loadLocal(0).call(1).storeLocal(1)
	// r.kind = "shared" then return r
	f2.loadLocal(1).loadConst(strConst("shared")).loadConst(strConst("kind")).setField()
	f2.loadLocal(1).ret()
	prog.Add(f2.function())

	// f1(n): local0=n, local1=a, local2=r
	f1 := newBuilder("f1", 1, 3)
	f1.loadConst(strConst("A")).newObject().storeLocal(1)                         // a
	f1.loadConst(FunctionValue(f2.function())).loadLocal(0).call(1).storeLocal(2) // r
	// a.result = r ; a.alias = r (same identity in two slots)
	f1.loadLocal(1).loadLocal(2).loadConst(strConst("result")).setField()
	f1.loadLocal(1).loadLocal(2).loadConst(strConst("alias")).setField()
	// r.anchor = a closes the cross-frame cycle r <-> a
	f1.loadLocal(2).loadLocal(1).loadConst(strConst("anchor")).setField()
	f1.loadLocal(1).ret()
	prog.Add(f1.function())

	// main(p): local0=p, local1=out
	main := newBuilder("main", 1, 2)
	main.loadConst(FunctionValue(f1.function())).loadLocal(0).call(1).storeLocal(1)
	main.loadLocal(1).ret()
	prog.Add(main.function())
	return prog
}

func TestThreeLayerInlineDeoptAndGraphEquivalence(t *testing.T) {
	prog := buildInlineProgram()

	base := runInterp(t, prog, "main", []Value{IntValue(1)})
	opt, runner := runOptimized(t, prog, "main", []Value{IntValue(1)})

	if runner.DeoptCount() != 1 {
		t.Fatalf("expected exactly 1 deoptimization, got %d", runner.DeoptCount())
	}

	// Terminal value and reachable heap graph must be isomorphic.
	if !ResultsEquivalent(base, opt) {
		t.Fatalf("heap graphs differ\nbase:\n%s\nopt:\n%s",
			CanonicalGraph(base).DebugString(), CanonicalGraph(opt).DebugString())
	}

	a := opt.AsObject()
	result, ok := a.Field("result")
	if !ok {
		t.Fatal("missing a.result")
	}
	alias, ok := a.Field("alias")
	if !ok {
		t.Fatal("missing a.alias")
	}
	// Cross-frame shared virtual object keeps === identity.
	if !StrictEqual(result, alias) {
		t.Fatal("a.result and a.alias must be identical references")
	}

	v := result.AsObject()
	other, _ := v.Field("other")
	u := other.AsObject()
	self, _ := u.Field("self")
	// Cycle A.next-style: u.self === u must terminate, not loop forever.
	if !StrictEqual(ObjectValue(u), self) {
		t.Fatal("u.self must be identical to u (cycle materialization)")
	}
	anchor, _ := v.Field("anchor")
	if !StrictEqual(anchor, ObjectValue(a)) {
		t.Fatal("v.anchor must point back to a (cross-frame cycle)")
	}
	sum, _ := v.Field("sum")
	if !sum.IsFloat() || sum.AsFloat() != float64(maxInt64())+1 {
		t.Fatalf("overflow must promote to float %v, got %s", float64(maxInt64())+1, sum)
	}
	if got, want := u.Class(), "U"; got != want {
		t.Fatalf("u class = %s, want %s", got, want)
	}
}

func TestDeoptReconstructsIndependentFrames(t *testing.T) {
	prog := buildInlineProgram()
	var captured []FrameView
	runner := NewOptimizedRunner(prog)
	runner.MaxInlineDepth = 4
	runner.OnDeopt = func(reason string, depth int) {
		if depth < 4 {
			t.Fatalf("expected >=4 reconstructed frames (main,f1,f2,f3), got %d", depth)
		}
	}
	if _, err := runner.Run("main", []Value{IntValue(1)}); err != nil {
		t.Fatal(err)
	}
	meta := runner.LastMetadata()
	if meta == nil {
		t.Fatal("expected deoptimization metadata to be recorded")
	}
	res, err := Deopt(prog, meta)
	if err != nil {
		t.Fatal(err)
	}
	captured = res.Interpreter.Frames()
	if len(captured) < 4 {
		t.Fatalf("expected reconstructed frame views, got %d", len(captured))
	}
	names := map[string]bool{}
	for _, fv := range captured {
		names[fv.FunctionName] = true
		if fv.FunctionName == "f3" {
			if len(fv.Stack) < 2 {
				t.Fatalf("f3 operand stack at guard pc must hold the 2 add operands, got %d", len(fv.Stack))
			}
			top := fv.Stack[len(fv.Stack)-1]
			next := fv.Stack[len(fv.Stack)-2]
			if !top.IsInt() || !next.IsInt() {
				t.Fatalf("f3 top two stack values at Add pc must be ints, got %s, %s", next, top)
			}
		}
	}
	for _, want := range []string{"main", "f1", "f2", "f3"} {
		if !names[want] {
			t.Fatalf("reconstructed stack missing frame %s", want)
		}
	}
}
