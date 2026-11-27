package midend

import "testing"

func twoFieldTy() *StructType {
	return StructT("pair", F("a", I64), F("next", PtrT(I64)))
}

func TestInterpBasic(t *testing.T) {
	prog := NewProgram()
	prog.Entry = "main"
	g := prog.AddGlobal(&Global{Name: "g", Ty: twoFieldTy()})
	b := NewBuilder(prog)
	b.Func("main", I64)
	b.Alloc("o", true, twoFieldTy())
	b.Field("p", R("o"), 0, PtrT(I64))
	b.Store(R("p"), C(42, I64))
	b.Load("v", R("p"), I64)
	b.Print(R("v"))
	b.GlobalAddr("gp", g)
	b.Field("gf", R("gp"), 1, PtrT(I64))
	b.Store(R("gf"), R("o"))
	b.Ret(R("v"))

	in := NewInterpreter(prog)
	got := in.Run()
	if got.Int != 42 {
		t.Fatalf("ret = %d, want 42", got.Int)
	}
	if len(in.Output) != 1 || in.Output[0] != "42" {
		t.Fatalf("output = %v, want [42]", in.Output)
	}
	snap := in.HeapSnapshot()
	if snap == "" {
		t.Fatal("expected live heap object")
	}
}

func TestEscapeFieldGraph(t *testing.T) {
	// A holds B in a field; A is returned, so B must escape via the graph.
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("node", F("v", I64), F("ref", PtrT(I64)))
	b := NewBuilder(prog)
	b.Func("main", PtrT(ty))
	b.Alloc("a", true, ty)
	b.Alloc("bb", true, ty)
	b.Field("af", R("a"), 1, PtrT(PtrT(ty)))
	b.Store(R("af"), R("bb"))
	b.Ret(R("a"))

	res := AnalyzeEscape(prog)
	main := prog.Func("main")
	if !res.AllocEscaped(main, "a") {
		t.Error("a should escape (returned)")
	}
	if !res.AllocEscaped(main, "bb") {
		t.Error("bb should escape transitively through a's field")
	}
}

func TestEscapeFieldGraphDeep(t *testing.T) {
	// Three-level chain: c stored in b.ref, b stored in a.ref, a returned.
	// Closure over the connection graph must flag all three as escaped.
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("node3", F("v", I64), F("ref", PtrT(I64)))
	b := NewBuilder(prog)
	b.Func("main", PtrT(ty))
	b.Alloc("a", true, ty)
	b.Alloc("bb", true, ty)
	b.Alloc("c", true, ty)

	b.Field("bf", R("bb"), 1, PtrT(PtrT(ty)))
	b.Store(R("bf"), R("c"))
	b.Field("af", R("a"), 1, PtrT(PtrT(ty)))
	b.Store(R("af"), R("bb"))
	b.Ret(R("a"))

	res := AnalyzeEscape(prog)
	main := prog.Func("main")
	for _, name := range []string{"a", "bb", "c"} {
		if !res.AllocEscaped(main, name) {
			t.Errorf("%s must escape through the deep field graph", name)
		}
	}
}
