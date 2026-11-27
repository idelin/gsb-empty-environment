package midend

import "testing"

// buildGlobalExtProgram covers GlobalEscape through both a global store and
// an opaque external call.
func buildGlobalExtProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	node := StructT("gnode", F("v", I64), F("ref", PtrT(I64)))
	g := prog.AddGlobal(&Global{Name: "g", Ty: node})
	b := NewBuilder(prog)

	// toGlobal stores its parameter into a global field.
	b.Func("toGlobal", I64, &Param{Name: "p", Ty: PtrT(node)})
	b.GlobalAddr("gp", g)
	b.Field("gr", R("gp"), 1, PtrT(PtrT(node)))
	b.Store(R("gr"), R("p"))
	b.Ret(C(0, I64))

	// toExternal passes its parameter to an opaque external function.
	b.Func("toExternal", I64, &Param{Name: "q", Ty: PtrT(node)})
	b.ExtCall("e0", "sink", I64, R("q"))
	b.Ret(C(0, I64))

	b.Func("main", I64)
	b.Alloc("a", true, node)
	b.Call("tg", "toGlobal", I64, R("a"))
	b.Alloc("b", true, node)
	b.Call("te", "toExternal", I64, R("b"))
	b.Ret(C(0, I64))

	return prog
}

func TestGlobalAndExternalEscape(t *testing.T) {
	prog := buildGlobalExtProgram()
	res := AnalyzeEscape(prog)
	sum := res.Summaries()

	if got := sum["toGlobal"].Params[0].Class; got != GlobalEscape {
		t.Errorf("toGlobal param = %s, want GlobalEscape", got)
	}
	if got := sum["toExternal"].Params[0].Class; got != GlobalEscape {
		t.Errorf("toExternal param = %s, want GlobalEscape", got)
	}

	main := prog.Func("main")
	if !res.AllocEscaped(main, "a") {
		t.Error("a must escape via global")
	}
	if !res.AllocEscaped(main, "b") {
		t.Error("b must escape via external call")
	}

	// Neither allocation can be eliminated.
	opt, sroaRes := RunSROA(prog, res)
	if sroaRes.EliminatedHeapAllocs != 0 {
		t.Errorf("no elimination allowed, got %d", sroaRes.EliminatedHeapAllocs)
	}

	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap mismatch before=%q after=%q",
			before.HeapSnapshot(), after.HeapSnapshot())
	}
}

// buildLoopSROAProgram repeatedly updates a non-escaping allocation inside a
// loop, requiring a loop-carried phi for each scalarized slot.
func buildLoopSROAProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("acc", F("sum", I64), F("i", I64))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("acc", true, ty)
	b.Field("sp", R("acc"), 0, PtrT(I64))
	b.Store(R("sp"), C(0, I64))
	b.Field("ip", R("acc"), 1, PtrT(I64))
	b.Store(R("ip"), C(0, I64))
	b.Jump("cond")

	b.Block("cond")
	b.Field("ipc", R("acc"), 1, PtrT(I64))
	b.Load("ic", R("ipc"), I64)
	b.Bin("more", "lt", I64, R("ic"), C(5, I64))
	b.Branch(R("more"), "body", "exit")

	b.Block("body")
	b.Field("spb", R("acc"), 0, PtrT(I64))
	b.Load("sc", R("spb"), I64)
	b.Bin("ns", "add", I64, R("sc"), R("ic"))
	b.Store(R("spb"), R("ns"))
	b.Bin("ni", "add", I64, R("ic"), C(1, I64))
	b.Field("ipb", R("acc"), 1, PtrT(I64))
	b.Store(R("ipb"), R("ni"))
	b.Jump("cond")

	b.Block("exit")
	b.Field("spe", R("acc"), 0, PtrT(I64))
	b.Load("result", R("spe"), I64)
	b.Print(R("result"))
	b.Ret(R("result"))

	return prog
}

func TestLoopSROA(t *testing.T) {
	prog := buildLoopSROAProgram()
	esc := AnalyzeEscape(prog)
	if esc.AllocEscaped(prog.Func("main"), "acc") {
		t.Fatal("acc must not escape")
	}

	before := NewInterpreter(prog)
	beforeRes := before.Run()

	opt, res := RunSROA(prog, esc)
	if res.EliminatedHeapAllocs != 1 {
		t.Fatalf("eliminated=%d want 1 rejected=%v", res.EliminatedHeapAllocs, res.Rejected)
	}

	// The loop header must carry a value phi for each scalarized slot.
	var headerPhis int
	for _, ins := range opt.Func("main").blockByName("cond").Insts {
		if _, ok := ins.(*Phi); ok {
			headerPhis++
		}
	}
	if headerPhis < 2 {
		t.Fatalf("loop header phis = %d, want at least 2", headerPhis)
	}

	after := NewInterpreter(opt)
	afterRes := after.Run()
	if beforeRes.Int != afterRes.Int || beforeRes.Int != 10 {
		t.Fatalf("result before=%d after=%d (want 10)", beforeRes.Int, afterRes.Int)
	}
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap before=%q after=%q", before.HeapSnapshot(), after.HeapSnapshot())
	}
}
