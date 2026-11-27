package midend

import "testing"

// buildMixedProgram combines several functions with heap/stack/global
// storage: some allocations are scalarizable, one escapes through a global,
// one through a return, and a non-escaping one uses dynamic offset and must
// be promoted rather than split.
func buildMixedProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	pair := StructT("pair", F("a", I64), F("b", I64))
	node := StructT("node", F("v", I64), F("ref", PtrT(I64)))
	g := prog.AddGlobal(&Global{Name: "registry", Ty: node})
	b := NewBuilder(prog)

	// addPair reads two ints into a fresh non-escaping pair and returns their sum.
	b.Func("addPair", I64, &Param{Name: "x", Ty: I64}, &Param{Name: "y", Ty: I64})
	b.Alloc("p", true, pair)
	b.Field("pa", R("p"), 0, PtrT(I64))
	b.Store(R("pa"), R("x"))
	b.Field("pb", R("p"), 1, PtrT(I64))
	b.Store(R("pb"), R("y"))
	b.Load("la", R("pa"), I64)
	b.Load("lb", R("pb"), I64)
	b.Bin("sum", "add", I64, R("la"), R("lb"))
	b.Ret(R("sum"))

	// registerEscape allocates a node and stores it into the global registry,
	// forcing global escape.
	b.Func("registerEscape", I64, &Param{Name: "v", Ty: I64})
	b.Alloc("n", true, node)
	b.Field("nv", R("n"), 0, PtrT(I64))
	b.Store(R("nv"), R("v"))
	b.GlobalAddr("gp", g)
	b.Field("gr", R("gp"), 1, PtrT(PtrT(node)))
	b.Store(R("gr"), R("n"))
	b.Ret(C(0, I64))

	// dynamicAlloc is non-escaping but uses a runtime offset, so SROA refuses
	// splitting and the allocation must be stack-promoted instead.
	b.Func("dynamicAlloc", I64, &Param{Name: "k", Ty: I64})
	b.Alloc("d", true, pair)
	b.AddrAdd("dp", R("d"), R("k"), PtrT(I64))
	b.Store(R("dp"), C(7, I64))
	b.Load("dr", R("dp"), I64)
	b.Ret(R("dr"))

	b.Func("main", I64)
	b.Call("s1", "addPair", I64, C(3, I64), C(4, I64))
	b.Call("s2", "addPair", I64, C(10, I64), C(20, I64))
	b.Bin("tot", "add", I64, R("s1"), R("s2"))
	b.Print(R("tot"))
	b.Call("reg", "registerEscape", I64, C(55, I64))
	b.Call("dyn", "dynamicAlloc", I64, C(8, I64))
	b.Print(R("dyn"))

	// One more local scalarizable allocation with branch-merged field phi.
	b.Alloc("local", true, pair)
	b.Branch(C(1, I64), "m1", "m2")
	b.Block("m1")
	b.Field("l1", R("local"), 0, PtrT(I64))
	b.Store(R("l1"), C(100, I64))
	b.Jump("mj")
	b.Block("m2")
	b.Field("l2", R("local"), 1, PtrT(I64))
	b.Store(R("l2"), C(200, I64))
	b.Jump("mj")
	b.Block("mj")
	b.Field("lf0", R("local"), 0, PtrT(I64))
	b.Load("lv", R("lf0"), I64)
	b.Print(R("lv"))
	b.Ret(R("tot"))

	return prog
}

func TestMixedEliminationCountAndEquivalence(t *testing.T) {
	prog := buildMixedProgram()
	esc := AnalyzeEscape(prog)

	// The globally-registered node must escape.
	if !esc.AllocEscaped(prog.Func("registerEscape"), "n") {
		t.Error("registerEscape.n must escape globally")
	}

	before := NewInterpreter(prog)
	beforeRes := before.Run()

	opt, res := RunSROA(prog, esc)

	// addPair.p and main.local must be fully eliminated.
	if res.EliminatedHeapAllocs != 2 {
		t.Fatalf("eliminated heap allocs = %d, want 2; rejected=%v",
			res.EliminatedHeapAllocs, res.Rejected)
	}
	// dynamicAlloc.d is non-escaping but unsplittable, so it is promoted.
	if res.PromotedStackAllocs < 1 {
		t.Fatalf("expected at least 1 stack promotion, got %d", res.PromotedStackAllocs)
	}

	after := NewInterpreter(opt)
	afterRes := after.Run()

	if beforeRes.Int != afterRes.Int {
		t.Fatalf("return before=%d after=%d", beforeRes.Int, afterRes.Int)
	}
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output mismatch before=%v after=%v", before.Output, after.Output)
	}
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap state mismatch\n before=%q\n after =%q",
			before.HeapSnapshot(), after.HeapSnapshot())
	}

	// The globally escaped node must remain live in the optimized heap.
	snap := after.HeapSnapshot()
	if snap == "" {
		t.Fatal("escaped global-linked heap object must survive SROA")
	}
}

func TestOptimalIRHasNoCandidateAllocs(t *testing.T) {
	prog := buildMixedProgram()
	opt, _ := RunSROA(prog, AnalyzeEscape(prog))
	// addPair should contain no allocation after SROA.
	for _, blk := range opt.Func("addPair").Blocks {
		for _, ins := range blk.Insts {
			if _, ok := ins.(*Alloc); ok {
				t.Fatalf("addPair still contains an alloc: %s", ins)
			}
		}
	}
}
