package midend

import "testing"

// buildArgToArgProgram models nested container wiring:
//
//	storeInto(dst, src): stores src into dst.ref  -> ArgToArg(0 -> 1)
//	chain(outer, inner, leaf): stores inner into outer then leaf into inner,
//	                          exercising nested ArgToArg propagation.
//
// Two call sites distinguish an escaping destination (returned container)
// from a non-escaping one (pure local container).
func buildArgToArgProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	nodeTy := StructT("node", F("v", I64), F("ref", PtrT(I64)))
	b := NewBuilder(prog)

	// void-ish helper returning 0; signature uses pointer params.
	b.Func("storeInto", I64,
		&Param{Name: "dst", Ty: PtrT(nodeTy)},
		&Param{Name: "src", Ty: PtrT(nodeTy)})
	b.Field("df", R("dst"), 1, PtrT(PtrT(nodeTy)))
	b.Store(R("df"), R("src"))
	b.Ret(C(0, I64))

	b.Func("chain", I64,
		&Param{Name: "outer", Ty: PtrT(nodeTy)},
		&Param{Name: "inner", Ty: PtrT(nodeTy)},
		&Param{Name: "leaf", Ty: PtrT(nodeTy)})
	b.Call("s1", "storeInto", I64, R("outer"), R("inner"))
	b.Call("s2", "storeInto", I64, R("inner"), R("leaf"))
	b.Ret(C(0, I64))

	// Escaping path: holder is returned, and stored objects must inherit
	// escape through the field graph.
	b.Func("escaping", PtrT(nodeTy))
	b.Alloc("holder", true, nodeTy)
	b.Alloc("child", true, nodeTy)
	b.Alloc("grand", true, nodeTy)
	b.Call("c1", "chain", I64, R("holder"), R("child"), R("grand"))
	b.Ret(R("holder"))

	// Non-escaping path: all three stay local and are only read at the end.
	b.Func("local", I64)
	b.Alloc("h2", true, nodeTy)
	b.Alloc("c2", true, nodeTy)
	b.Alloc("g2", true, nodeTy)
	b.Call("c2call", "chain", I64, R("h2"), R("c2"), R("g2"))
	b.Field("h2f", R("h2"), 0, PtrT(I64))
	b.Load("hv", R("h2f"), I64)
	b.Print(R("hv"))
	b.Ret(R("hv"))

	b.Func("main", I64)
	b.Call("e", "escaping", PtrT(nodeTy))
	b.Call("l", "local", I64)
	b.Ret(C(0, I64))

	return prog
}

func TestArgToArgSummary(t *testing.T) {
	prog := buildArgToArgProgram()
	res := AnalyzeEscape(prog)
	sum := res.Summaries()

	si := sum["storeInto"]
	if got := si.Params[0].Class; got != NoEscape {
		t.Errorf("storeInto dst class = %s, want NoEscape", got)
	}
	if got := si.Params[1].Class; got != ArgToArg {
		t.Errorf("storeInto src class = %s, want ArgToArg", got)
	}
	if len(si.Params[1].ToArg) != 1 || si.Params[1].ToArg[0] != 0 {
		t.Errorf("storeInto src ToArg = %v, want [0]", si.Params[1].ToArg)
	}

	ch := sum["chain"]
	// inner is both a destination (leaf -> inner) and a source (inner -> outer).
	if got := ch.Params[1].Class; got != ArgToArg {
		t.Errorf("chain inner class = %s, want ArgToArg", got)
	}
	if got := ch.Params[2].Class; got != ArgToArg {
		t.Errorf("chain leaf class = %s, want ArgToArg", got)
	}
}

func TestArgToArgNestedEscape(t *testing.T) {
	prog := buildArgToArgProgram()
	res := AnalyzeEscape(prog)

	esc := prog.Func("escaping")
	for _, name := range []string{"holder", "child", "grand"} {
		if !res.AllocEscaped(esc, name) {
			t.Errorf("escaping.%s should escape via nested field graph", name)
		}
	}

	loc := prog.Func("local")
	for _, name := range []string{"h2", "c2", "g2"} {
		if res.AllocEscaped(loc, name) {
			t.Errorf("local.%s should not escape", name)
		}
	}
}

func TestArgToArgInterpEquivalence(t *testing.T) {
	prog := buildArgToArgProgram()
	esc := AnalyzeEscape(prog)
	opt, _ := RunSROA(prog, esc)

	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()

	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
	if before.HeapSnapshot() != after.HeapSnapshot() {
		t.Fatalf("heap before=%q after=%q", before.HeapSnapshot(), after.HeapSnapshot())
	}
}
