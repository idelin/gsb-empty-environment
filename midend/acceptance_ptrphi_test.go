package midend

import "testing"

// buildPtrPhiProgram builds a non-escaping allocation whose field pointer is
// selected through a pointer phi on two branches, then read and written via
// that phi. All phi sources are static, known non-escaping offsets, so SROA
// must split the allocation and insert value phis at the join block.
func buildPtrPhiProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("pair", F("a", I64), F("b", I64))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("o", true, ty)
	b.Branch(C(1, I64), "ta", "tb")

	b.Block("ta")
	b.Field("fa", R("o"), 0, PtrT(I64))
	b.Store(R("fa"), C(11, I64))
	b.Jump("merge")

	b.Block("tb")
	b.Field("fb", R("o"), 1, PtrT(I64))
	b.Store(R("fb"), C(22, I64))
	b.Jump("merge")

	b.Block("merge")
	// Pointer phi merging two static field addresses of the same allocation.
	b.Phi("pp", PtrT(I64), E("ta", R("fa")), E("tb", R("fb")))
	b.Store(R("pp"), C(33, I64))
	b.Load("vv", R("pp"), I64)
	b.Print(R("vv"))
	b.Ret(R("vv"))
	return prog
}

func TestPtrPhiSplittable(t *testing.T) {
	prog := buildPtrPhiProgram()
	esc := AnalyzeEscape(prog)
	if esc.AllocEscaped(prog.Func("main"), "o") {
		t.Fatal("o must not escape")
	}

	before := NewInterpreter(prog)
	before.Run()

	opt, res := RunSROA(prog, esc)
	if _, rejected := res.Rejected["o"]; rejected {
		t.Fatalf("o should be splittable, rejected: %v", res.Rejected)
	}
	if res.EliminatedHeapAllocs != 1 {
		t.Fatalf("eliminated=%d want 1", res.EliminatedHeapAllocs)
	}

	// A value phi for each split field must exist at the merge block.
	join := opt.Func("main").blockByName("merge")
	phiCount := 0
	for _, ins := range join.Insts {
		if ph, ok := ins.(*Phi); ok {
			phiCount++
			if len(ph.Incoming) != 2 {
				t.Errorf("phi %s has %d incoming, want 2", ph.Name, len(ph.Incoming))
			}
		}
	}
	if phiCount != 2 {
		t.Fatalf("merge phi count = %d, want 2 (one per field)", phiCount)
	}

	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap before=%q after=%q", before.HeapSnapshot(), after.HeapSnapshot())
	}
}

// buildPtrPhiDistinctAllocProgram makes a pointer phi that merges addresses of
// two distinct non-escaping allocations. That cannot be scalarized safely for
// both objects, so SROA must conservatively refuse.
func buildPtrPhiDistinctAllocProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	mty := StructT("one", F("x", I64))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("a1", true, mty)
	b.Alloc("a2", true, mty)
	b.Branch(C(0, I64), "p1", "p2")

	b.Block("p1")
	b.Field("a1f", R("a1"), 0, PtrT(I64))
	b.Jump("m")

	b.Block("p2")
	b.Field("a2f", R("a2"), 0, PtrT(I64))
	b.Jump("m")

	b.Block("m")
	b.Phi("mix", PtrT(I64), E("p1", R("a1f")), E("p2", R("a2f")))
	b.Load("r", R("mix"), I64)
	b.Print(R("r"))
	b.Ret(R("r"))
	return prog
}

func TestPtrPhiDistinctAllocRejected(t *testing.T) {
	prog := buildPtrPhiDistinctAllocProgram()
	esc := AnalyzeEscape(prog)
	_, res := RunSROA(prog, esc)
	for _, name := range []string{"a1", "a2"} {
		if _, ok := res.Rejected[name]; !ok {
			t.Errorf("allocation %s should be rejected for SROA", name)
		}
	}

	// Even when refused, the program must remain executable and equivalent.
	opt, _ := RunSROA(prog, AnalyzeEscape(prog))
	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
}
