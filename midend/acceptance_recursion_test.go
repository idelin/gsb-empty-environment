package midend

import "testing"

// buildRecursionProgram builds a direct recursion (countdown) and a mutual
// recursion (even/odd). In countdown the parameter is only read (NoEscape);
// in wrap/unwrap a parameter is returned through mutually recursive calls,
// requiring fixpoint iteration to reach ReturnEscape.
func buildRecursionProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("box", F("v", I64), F("ref", PtrT(I64)))
	b := NewBuilder(prog)

	// main: allocate a box, call countdown(box, 3) then wrap(box); the box is
	// never stored elsewhere or returned from main.
	b.Func("main", I64)
	b.Alloc("box", true, ty)
	b.Field("bp", R("box"), 0, PtrT(I64))
	b.Store(R("bp"), C(3, I64))
	b.Call("c0", "countdown", I64, R("box"), C(3, I64))
	b.Call("w0", "wrap", PtrT(ty), R("box"), C(2, I64))
	b.Print(R("c0"))
	b.Ret(R("c0"))

	// countdown(box, n): reads only; recurses while n > 0.
	b.Func("countdown", I64, &Param{Name: "p", Ty: PtrT(ty)}, &Param{Name: "n", Ty: I64})
	b.Branch(R("n"), "body", "done")
	b.Block("done")
	b.Ret(C(0, I64))
	b.Block("body")
	b.Bin("nm1", "sub", I64, R("n"), C(1, I64))
	b.Call("rec", "countdown", I64, R("p"), R("nm1"))
	b.Ret(R("rec"))

	// wrap(p, depth) and unwrap(p, depth) call each other while depth > 0 and
	// return p at the base. Both summaries must converge to ReturnEscape
	// despite the mutual call cycle.
	b.Func("wrap", PtrT(ty), &Param{Name: "p", Ty: PtrT(ty)}, &Param{Name: "depth", Ty: I64})
	b.Branch(R("depth"), "wbody", "wbase")
	b.Block("wbase")
	b.Ret(R("p"))
	b.Block("wbody")
	b.Bin("wd", "sub", I64, R("depth"), C(1, I64))
	b.Call("u", "unwrap", PtrT(ty), R("p"), R("wd"))
	b.Ret(R("u"))

	b.Func("unwrap", PtrT(ty), &Param{Name: "p", Ty: PtrT(ty)}, &Param{Name: "depth", Ty: I64})
	b.Branch(R("depth"), "ubody", "ubase")
	b.Block("ubase")
	b.Ret(R("p"))
	b.Block("ubody")
	b.Bin("ud", "sub", I64, R("depth"), C(1, I64))
	b.Call("w", "wrap", PtrT(ty), R("p"), R("ud"))
	b.Ret(R("w"))

	return prog
}

func TestRecursiveSummaryFixpoint(t *testing.T) {
	prog := buildRecursionProgram()
	res := AnalyzeEscape(prog)
	sum := res.Summaries()

	cd := sum["countdown"]
	if got := cd.Params[0].Class; got != NoEscape {
		t.Errorf("countdown param class = %s, want NoEscape", got)
	}

	wp := sum["wrap"]
	if got := wp.Params[0].Class; got != ReturnEscape {
		t.Errorf("wrap param class = %s, want ReturnEscape", got)
	}
	up := sum["unwrap"]
	if got := up.Params[0].Class; got != ReturnEscape {
		t.Errorf("unwrap param class = %s, want ReturnEscape", got)
	}

	// The box does not escape main even though it flows into recursive calls.
	main := prog.Func("main")
	if res.AllocEscaped(main, "box") {
		t.Error("box should not escape main")
	}

	// Interpreter must still run to completion with the recursion.
	in := NewInterpreter(prog)
	out := in.Run()
	if out.Int != 0 {
		t.Fatalf("main ret = %d want 0", out.Int)
	}
}
