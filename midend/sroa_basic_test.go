package midend

import "testing"

// buildSimpleSROA builds: heap struct {a i64, b i64}; store constants into
// both fields on different branches joined by a block, then read and print.
func buildSimpleSROA() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("ab", F("a", I64), F("b", I64))
	b := NewBuilder(prog)
	b.Func("main", I64)
	b.Alloc("o", true, ty)
	b.Branch(C(1, I64), "left", "right")

	b.Block("left")
	b.Field("paL", R("o"), 0, PtrT(I64))
	b.Store(R("paL"), C(7, I64))
	b.Jump("join")

	b.Block("right")
	b.Field("paR", R("o"), 0, PtrT(I64))
	b.Store(R("paR"), C(9, I64))
	b.Jump("join")

	b.Block("join")
	b.Field("pa", R("o"), 0, PtrT(I64))
	b.Load("va", R("pa"), I64)
	b.Field("pb", R("o"), 1, PtrT(I64))
	b.Load("vb", R("pb"), I64)
	b.Print(R("va"))
	b.Print(R("vb"))
	b.Ret(R("va"))
	return prog
}

func TestSROABasicEquivalence(t *testing.T) {
	prog := buildSimpleSROA()
	esc := AnalyzeEscape(prog)
	main := prog.Func("main")
	if esc.AllocEscaped(main, "o") {
		t.Fatal("o must not escape")
	}

	before := NewInterpreter(prog)
	beforeRes := before.Run()

	opt, sroaRes := RunSROA(prog, esc)
	if sroaRes.EliminatedHeapAllocs != 1 {
		t.Fatalf("eliminated=%d want 1, rejected=%v", sroaRes.EliminatedHeapAllocs, sroaRes.Rejected)
	}

	after := NewInterpreter(opt)
	afterRes := after.Run()

	if beforeRes.Int != afterRes.Int {
		t.Fatalf("ret before=%d after=%d", beforeRes.Int, afterRes.Int)
	}
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap before=%q after=%q", before.HeapSnapshot(), after.HeapSnapshot())
	}
}

func stringsJoin(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
