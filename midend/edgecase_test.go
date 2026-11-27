package midend

import "testing"

func TestNoAllocPipeline(t *testing.T) {
	prog := NewProgram()
	prog.Entry = "main"
	b := NewBuilder(prog)
	b.Func("main", I64)
	b.Bin("x", "add", I64, C(2, I64), C(3, I64))
	b.Print(R("x"))
	b.Ret(R("x"))

	esc := AnalyzeEscape(prog)
	opt, res := RunSROA(prog, esc)
	if res.EliminatedHeapAllocs != 0 || res.PromotedStackAllocs != 0 {
		t.Fatalf("unexpected stats: %+v", res)
	}

	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != "5" || stringsJoin(after.Output) != "5" {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
}

func TestDeadAllocationRemoved(t *testing.T) {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("dead", F("a", I64))
	b := NewBuilder(prog)
	b.Func("main", I64)
	b.Alloc("unused", true, ty)
	b.Ret(C(1, I64))

	esc := AnalyzeEscape(prog)
	opt, res := RunSROA(prog, esc)
	if res.EliminatedHeapAllocs != 1 {
		t.Fatalf("dead alloc should be removed, stats=%+v", res)
	}
	for _, blk := range opt.Func("main").Blocks {
		for _, ins := range blk.Insts {
			if _, ok := ins.(*Alloc); ok {
				t.Fatal("dead alloc must not remain")
			}
		}
	}

	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap mismatch %q vs %q", before.HeapSnapshot(), after.HeapSnapshot())
	}
}

func TestIdempotentOptimization(t *testing.T) {
	prog := buildSimpleSROA()
	esc := AnalyzeEscape(prog)
	once, r1 := RunSROA(prog, esc)
	twice, r2 := RunSROA(once, AnalyzeEscape(once))

	if PrintProgram(once) != PrintProgram(twice) {
		t.Fatal("second SROA pass changed an already-optimized program")
	}
	if r2.EliminatedHeapAllocs != 0 {
		t.Fatalf("second pass should eliminate nothing, got %d", r2.EliminatedHeapAllocs)
	}
	_ = r1
}
