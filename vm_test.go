package linker

import "testing"

func sumProgram(symbol string) (Code, error) {
	return Assemble([]Instruction{
		{Op: OpDup},
		{Op: OpPush, Imm: 0},
		{Op: OpEq},
		{Op: OpJz, Jump: "recurse"},
		{Op: OpDrop},
		{Op: OpPush, Imm: 0},
		{Op: OpRet},
		{Op: OpDup, Label: "recurse"},
		{Op: OpPush, Imm: 1},
		{Op: OpSub},
		{Op: OpCall, Symbol: symbol, Args: 1},
		{Op: OpAdd},
		{Op: OpRet},
	})
}

func copyCode(code Code) Code {
	out := Code{Data: append([]byte(nil), code.Data...)}
	out.Relocations = append([]Relocation(nil), code.Relocations...)
	return out
}

func withRelocFrom(code Code, from SectionID, symbol string) Code {
	out := copyCode(code)
	for i := range out.Relocations {
		out.Relocations[i].From = from
		out.Relocations[i].Symbol = symbol
	}
	return out
}

func vmImage(t *testing.T) *Image {
	t.Helper()
	fCode, err := sumProgram("f")
	if err != nil {
		t.Fatal(err)
	}
	gCode := withRelocFrom(fCode, 1, "g")
	uCode := withRelocFrom(fCode, 2, "u")
	fCode = withRelocFrom(fCode, 0, "f")
	mainCode, err := Assemble([]Instruction{
		{Op: OpAddr, Symbol: "f"},
		{Op: OpAddr, Symbol: "g"},
		{Op: OpEq},
		{Op: OpRet},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range mainCode.Relocations {
		mainCode.Relocations[i].From = 3
	}
	return &Image{
		Sections: []Section{
			{Name: "f", Kind: KindCode, Data: fCode.Data, Arity: 1},
			{Name: "g", Kind: KindCode, Data: gCode.Data, Arity: 1},
			{Name: "u", Kind: KindCode, Data: uCode.Data, Arity: 1},
			{Name: "main", Kind: KindCode, Data: mainCode.Data},
		},
		Symbols: []Symbol{
			{Name: "f", Section: 0},
			{Name: "g", Section: 1},
			{Name: "u", Section: 2},
			{Name: "main", Section: 3},
		},
		Relocations: append(append(append(fCode.Relocations,
			gCode.Relocations...), uCode.Relocations...), mainCode.Relocations...),
	}
}

func TestVMExecutesRecursiveFunctions(t *testing.T) {
	img := vmImage(t)
	for _, tc := range []struct {
		sym  string
		n    int64
		want int64
	}{
		{"f", 0, 0},
		{"f", 5, 15},
		{"g", 3, 6},
		{"u", 7, 28},
	} {
		v, err := ExecuteSymbol(img, tc.sym, []int64{tc.n}, 10000)
		if err != nil {
			t.Fatalf("%s(%d): %v", tc.sym, tc.n, err)
		}
		if v.Kind != ValueInt || v.Int != tc.want {
			t.Fatalf("%s(%d) = %s, want %d", tc.sym, tc.n, v, tc.want)
		}
	}
	v, err := ExecuteSymbol(img, "main", nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 0 {
		t.Fatalf("&f == &g evaluated to %d before folding", v.Int)
	}
}

func TestVMValidatesSafeFolding(t *testing.T) {
	before := vmImage(t)
	after, res, err := ICF(before, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Validate(before, after, res.OldToNew, ValidateOptions{
		Probes: []CallProbe{
			{Symbol: "f", Args: []int64{5}},
			{Symbol: "g", Args: []int64{4}},
			{Symbol: "u", Args: []int64{3}},
			{Symbol: "main"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("safe ICF validation failed: %+v", report.Errors)
	}
	if !report.PointersOK || len(report.Pointers) == 0 {
		t.Fatal("pointer identity was not checked")
	}
	v, err := ExecuteSymbol(after, "main", nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 0 {
		t.Fatalf("safe ICF changed &f == &g to %d", v.Int)
	}
	v, err = ExecuteSymbol(after, "u", []int64{6}, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 21 {
		t.Fatalf("folded u(6) = %d, want 21", v.Int)
	}
}

func TestVMDetectsICFAllPointerChange(t *testing.T) {
	before := vmImage(t)
	after, res, err := ICF(before, ICFAll)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Validate(before, after, res.OldToNew, ValidateOptions{
		Probes: []CallProbe{{Symbol: "main"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("icf=all pointer identity change was not reported")
	}
	v, err := ExecuteSymbol(after, "main", nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 1 {
		t.Fatalf("icf=all did not make &f == &g true: got %d", v.Int)
	}
}

func TestLinkICFAllReportsButDoesNotFailOnPointerChange(t *testing.T) {
	before := vmImage(t)
	result, err := Link(before, LinkOptions{ICF: ICFAll, Validate: true})
	if err != nil {
		t.Fatalf("icf=all validation should report, not fail: %v", err)
	}
	if result.Validation == nil || result.Validation.OK {
		t.Fatal("icf=all pointer change was not reported")
	}
	if result.Validation.CallsOK {
		t.Fatal("pointer-comparison result should be recorded as a call difference")
	}
}

func TestLinkEndToEndWithGCICFAndValidation(t *testing.T) {
	base := vmImage(t)
	entryCode, err := Assemble([]Instruction{
		{Op: OpPush, Imm: 1},
		{Op: OpCall, Symbol: "main", Args: 0},
		{Op: OpDrop},
		{Op: OpPush, Imm: 7},
		{Op: OpCall, Symbol: "u", Args: 1},
		{Op: OpDrop},
		{Op: OpPush, Imm: 5},
		{Op: OpCall, Symbol: "f", Args: 1},
		{Op: OpRet},
	})
	if err != nil {
		t.Fatal(err)
	}
	entryID := SectionID(len(base.Sections))
	for i := range entryCode.Relocations {
		entryCode.Relocations[i].From = entryID
	}
	base.Sections = append(base.Sections,
		Section{Name: "entry", Kind: KindCode, Data: entryCode.Data},
		Section{Name: "dead", Kind: KindCode, Data: []byte{0xC3}},
	)
	base.Symbols = append(base.Symbols,
		Symbol{Name: "entry", Section: entryID},
		Symbol{Name: "dead", Section: entryID + 1},
	)
	base.Relocations = append(base.Relocations, entryCode.Relocations...)
	base.EntrySymbols = []string{"entry"}

	result, err := Link(base, LinkOptions{
		GCSections: true,
		ICF:        ICFSafe,
		Validate:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GC == nil || result.ICF == nil || result.Validation == nil {
		t.Fatal("missing pipeline result")
	}
	if !result.Validation.OK {
		t.Fatalf("end-to-end validation failed: %+v", result.Validation.Errors)
	}
	if _, ok := result.Image.LookupSymbol("dead"); ok {
		t.Fatal("dead section survived GC")
	}
	if result.ICF.Canonical[2] == 2 {
		t.Fatal("unaddressed u was not folded")
	}
	v, err := ExecuteSymbol(result.Image, "entry", nil, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 15 {
		t.Fatalf("entry returned %d, want 15", v.Int)
	}
}

func TestMergeObjectsRebasesIDs(t *testing.T) {
	o1 := &Object{
		Name: "a.o",
		Sections: []Section{
			{Name: ".text.main", Kind: KindCode, Data: []byte{0xE8, 0, 0, 0, 0}},
		},
		Symbols: []Symbol{{Name: "main", Section: 0}},
		Relocations: []Relocation{
			{From: 0, Offset: 1, Type: RelocCall, Symbol: "helper", Size: 4},
		},
	}
	o2 := &Object{
		Name:     "b.o",
		Sections: []Section{{Name: ".text.helper", Kind: KindCode, Data: []byte{0xC3}}},
		Symbols:  []Symbol{{Name: "helper", Section: 0}},
	}
	img, err := MergeObjects(o1, o2)
	if err != nil {
		t.Fatal(err)
	}
	if len(img.Sections) != 2 {
		t.Fatalf("sections = %d", len(img.Sections))
	}
	sym, _ := img.LookupSymbol("helper")
	if sym.Section != 1 {
		t.Fatalf("helper section = %d, want 1", sym.Section)
	}
	if img.Relocations[0].From != 0 {
		t.Fatalf("relocation source = %d", img.Relocations[0].From)
	}
	target, err := img.resolveRelocation(img.Relocations[0], img.SymbolMap())
	if err != nil {
		t.Fatal(err)
	}
	if target.Section != 1 {
		t.Fatalf("relocation target = %d, want 1", target.Section)
	}
}

func TestValidatePointerIdentityWithAddends(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "f", Kind: KindCode, Data: []byte{0xC3}},
			{Name: "main", Kind: KindCode, Data: make([]byte, 16)},
		},
		Symbols: []Symbol{
			{Name: "f", Section: 0},
			{Name: "main", Section: 1},
		},
		Relocations: []Relocation{
			{From: 1, Offset: 0, Type: RelocAddrTaken, Symbol: "f", Size: 8},
			{From: 1, Offset: 8, Type: RelocAddrTaken, Symbol: "f", Addend: 1, Size: 8},
		},
	}
	after, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Validate(img, after, res.OldToNew, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pointers) != 1 {
		t.Fatalf("pointer pairs = %d, want 1", len(report.Pointers))
	}
	if report.Pointers[0].BeforeEqual || report.Pointers[0].AfterEqual || !report.OK {
		t.Fatalf("unexpected pointer result: %+v", report.Pointers[0])
	}
}
