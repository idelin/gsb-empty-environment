package midend

import "testing"

// TestStackPromotionReclaimsMemory ensures a non-escaping but unsplittable
// allocation is moved to the stack and reclaimed on function return, leaving
// no trace in the live heap snapshot.
func TestStackPromotionReclaimsMemory(t *testing.T) {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("sp", F("a", I64), F("b", I64))
	b := NewBuilder(prog)
	b.Func("main", I64)
	b.Alloc("d", true, ty)
	b.AddrAdd("dp", R("d"), C(8, I64), PtrT(I64))
	b.Store(R("dp"), C(13, I64))
	b.Load("r", R("dp"), I64)
	b.Print(R("r"))
	b.Ret(R("r"))

	esc := AnalyzeEscape(prog)
	opt, res := RunSROA(prog, esc)
	if res.PromotedStackAllocs != 1 {
		t.Fatalf("promoted=%d want 1, rejected=%v", res.PromotedStackAllocs, res.Rejected)
	}

	// Optimized program must mark the allocation as stack.
	var found *Alloc
	for _, blk := range opt.Func("main").Blocks {
		for _, ins := range blk.Insts {
			if a, ok := ins.(*Alloc); ok && a.Name == "d" {
				found = a
			}
		}
	}
	if found == nil {
		t.Fatal("allocation should be retained (unsplittable)")
	}
	if found.Heap {
		t.Fatal("allocation should be promoted to stack")
	}

	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()

	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
	// Original heap object dies (no root); promoted stack object is reclaimed.
	if after.HeapSnapshot() != "" {
		t.Fatalf("stack allocation must not appear in heap snapshot: %q",
			after.HeapSnapshot())
	}
}
