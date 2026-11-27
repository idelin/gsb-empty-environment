package midend

import "testing"

// buildDynamicOffsetProgram allocates a struct and accesses it through an
// AddrAdd whose offset is a runtime value. SROA must refuse because the exact
// field is not statically known.
func buildDynamicOffsetProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("dyn", F("a", I64), F("b", I64), F("c", I64))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("o", true, ty)
	b.Bin("idx", "add", I64, C(0, I64), C(8, I64))
	b.AddrAdd("dp", R("o"), R("idx"), PtrT(I64))
	b.Store(R("dp"), C(5, I64))
	b.Load("r", R("dp"), I64)
	b.Print(R("r"))
	b.Ret(R("r"))
	return prog
}

func TestDynamicOffsetRejected(t *testing.T) {
	prog := buildDynamicOffsetProgram()
	esc := AnalyzeEscape(prog)
	if esc.AllocEscaped(prog.Func("main"), "o") {
		t.Fatal("o should not escape")
	}
	_, res := RunSROA(prog, esc)
	if _, ok := res.Rejected["o"]; !ok {
		t.Fatalf("dynamic-offset alloc must be rejected, got %+v", res)
	}
	if res.EliminatedHeapAllocs != 0 {
		t.Fatalf("no heap alloc may be eliminated, got %d", res.EliminatedHeapAllocs)
	}

	opt, _ := RunSROA(prog, AnalyzeEscape(prog))
	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
}

// buildOverlapProgram writes through a pointer whose static offset and access
// size do not line up with a single whole struct field, modelling a
// different-sized partial/overlapping write.
func buildOverlapProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	// Four i64 fields give 8-byte granularity; we emulate an overlapping
	// 4-byte write that cannot map to a whole field.
	ty := StructT("ov", F("a", I64), F("b", I64))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("o", true, ty)
	// Field address of field 0, then access only 4 bytes via an i32 load/store,
	// which overlaps half of the i64 field.
	b.Field("f0", R("o"), 0, PtrT(I64))
	b.Store(R("f0"), C(0x1122334455667788, I64))
	b.AddrAdd("half", R("f0"), C(4, I64), PtrT(I32))
	b.Store(R("half"), C(0x77, I32))
	b.Load("full", R("f0"), I64)
	b.Print(R("full"))
	b.Ret(R("full"))
	return prog
}

func TestOverlappingWriteRejected(t *testing.T) {
	prog := buildOverlapProgram()
	esc := AnalyzeEscape(prog)
	_, res := RunSROA(prog, esc)
	if _, ok := res.Rejected["o"]; !ok {
		t.Fatalf("overlapping-write alloc must be rejected, got %+v", res)
	}

	opt, _ := RunSROA(prog, AnalyzeEscape(prog))
	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
}

// buildUnknownAliasProgram keeps a local allocation non-escaping but merges
// its field address with an opaque pointer loaded from an escaped object into
// a pointer phi. SROA must refuse because one phi source is an unknown alias.
func buildUnknownAliasProgram() *Program {
	prog := NewProgram()
	prog.Entry = "main"
	ty := StructT("ua", F("x", I64), F("p", PtrT(I64)))
	b := NewBuilder(prog)

	b.Func("main", I64)
	b.Alloc("o", true, ty)
	// Sentinel escapes through the return and supplies an opaque pointer.
	b.Alloc("sentinel", true, ty)
	b.Field("of", R("o"), 0, PtrT(I64))
	b.Field("sp", R("sentinel"), 1, PtrT(PtrT(ty)))
	b.Load("alien", R("sp"), PtrT(I64))

	b.Branch(C(1, I64), "q1", "q2")
	b.Block("q1")
	b.Jump("qm")
	b.Block("q2")
	b.Jump("qm")
	b.Block("qm")
	// phi merges a known local field with an opaque (unknown) pointer.
	b.Phi("mix", PtrT(I64), E("q1", R("of")), E("q2", R("alien")))
	b.Load("r", R("mix"), I64)
	b.Print(R("r"))
	b.Ret(R("sentinel"))
	return prog
}

func TestUnknownAliasRejected(t *testing.T) {
	prog := buildUnknownAliasProgram()
	esc := AnalyzeEscape(prog)
	if !esc.AllocEscaped(prog.Func("main"), "sentinel") {
		t.Error("sentinel must escape")
	}
	if esc.AllocEscaped(prog.Func("main"), "o") {
		t.Error("o must stay non-escaping")
	}
	_, res := RunSROA(prog, esc)
	if _, ok := res.Rejected["o"]; !ok {
		t.Fatalf("o must be rejected due to unknown alias, got %+v", res)
	}

	opt, _ := RunSROA(prog, AnalyzeEscape(prog))
	before := NewInterpreter(prog)
	before.Run()
	after := NewInterpreter(opt)
	after.Run()
	if stringsJoin(before.Output) != stringsJoin(after.Output) {
		t.Fatalf("output before=%v after=%v", before.Output, after.Output)
	}
}
