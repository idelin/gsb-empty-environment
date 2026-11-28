package linker

import (
	"reflect"
	"testing"
)

func callSection(target string) ([]byte, Relocation) {
	return []byte{0xE8, 0, 0, 0, 0, 0xC3}, Relocation{Offset: 1, Type: RelocCall, Symbol: target, Size: 4}
}

func TestICFSelfRecursiveSectionsMerge(t *testing.T) {
	fCode, fRel := callSection("f")
	gCode, gRel := callSection("g")
	img := &Image{
		Sections: []Section{
			{Name: "f", Kind: KindCode, Data: fCode},
			{Name: "g", Kind: KindCode, Data: gCode},
			{Name: "main", Kind: KindCode, Data: []byte{0xC3}},
		},
		Symbols: []Symbol{
			{Name: "f", Section: 0},
			{Name: "g", Section: 1},
			{Name: "main", Section: 2},
		},
		Relocations: []Relocation{
			withFrom(fRel, 0),
			withFrom(gRel, 1),
		},
	}
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Merges[1] != 0 {
		t.Fatalf("g was not folded into f: merges=%v", res.Merges)
	}
	if len(out.Sections) != 2 {
		t.Fatalf("sections after ICF = %d, want 2", len(out.Sections))
	}
	sym, ok := out.LookupSymbol("g")
	if !ok || sym.Section != res.OldToNew[0] {
		t.Fatalf("g symbol was not rewritten to representative: %+v", sym)
	}
}

func TestICFMutualRecursionCyclesMerge(t *testing.T) {
	c1, r1 := callSection("f2")
	c2, r2 := callSection("f1")
	c3, r3 := callSection("g2")
	c4, r4 := callSection("g1")
	img := &Image{
		Sections: []Section{
			{Name: "f1", Kind: KindCode, Data: c1},
			{Name: "f2", Kind: KindCode, Data: c2},
			{Name: "g1", Kind: KindCode, Data: c3},
			{Name: "g2", Kind: KindCode, Data: c4},
		},
		Symbols: []Symbol{
			{Name: "f1", Section: 0},
			{Name: "f2", Section: 1},
			{Name: "g1", Section: 2},
			{Name: "g2", Section: 3},
		},
		Relocations: []Relocation{
			withFrom(r1, 0),
			withFrom(r2, 1),
			withFrom(r3, 2),
			withFrom(r4, 3),
		},
	}
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sections) != 1 {
		t.Fatalf("mutually-recursive cycle was not folded: %d sections remain", len(out.Sections))
	}
	for id := SectionID(1); id <= 3; id++ {
		if res.Canonical[id] != 0 {
			t.Fatalf("section %d canonicalized to %d, want 0", id, res.Canonical[id])
		}
	}
}

func TestICFMoorRefinementSplitsNonIsomorphicTargets(t *testing.T) {
	// Two isomorphic chains (a0 -> b0 -> c0 = 0xAA and a0b -> b0b -> c0b =
	// 0xAA) must fold at every level.  The parallel chains ending in 0xBB and
	// 0xCC have the same one-level shape but must not fold; refinement has to
	// carry the differing leaf classes backward through two rounds.
	b0, br0 := callSection("c0")
	b1, br1 := callSection("c1")
	b0b, br0b := callSection("c0b")
	b1b, br1b := callSection("c1b")
	a0, ar0 := callSection("b0")
	a1, ar1 := callSection("b1")
	a0b, ar0b := callSection("b0b")
	a1b, ar1b := callSection("b1b")
	img := &Image{
		Sections: []Section{
			{Name: "a0", Kind: KindCode, Data: a0},
			{Name: "a1", Kind: KindCode, Data: a1},
			{Name: "a0b", Kind: KindCode, Data: a0b},
			{Name: "a1b", Kind: KindCode, Data: a1b},
			{Name: "b0", Kind: KindCode, Data: b0},
			{Name: "b1", Kind: KindCode, Data: b1},
			{Name: "b0b", Kind: KindCode, Data: b0b},
			{Name: "b1b", Kind: KindCode, Data: b1b},
			{Name: "c0", Kind: KindCode, Data: []byte{0xAA}},
			{Name: "c1", Kind: KindCode, Data: []byte{0xBB}},
			{Name: "c0b", Kind: KindCode, Data: []byte{0xAA}},
			{Name: "c1b", Kind: KindCode, Data: []byte{0xCC}},
		},
		Symbols: []Symbol{
			{Name: "a0", Section: 0},
			{Name: "a1", Section: 1},
			{Name: "a0b", Section: 2},
			{Name: "a1b", Section: 3},
			{Name: "b0", Section: 4},
			{Name: "b1", Section: 5},
			{Name: "b0b", Section: 6},
			{Name: "b1b", Section: 7},
			{Name: "c0", Section: 8},
			{Name: "c1", Section: 9},
			{Name: "c0b", Section: 10},
			{Name: "c1b", Section: 11},
		},
		Relocations: []Relocation{
			withFrom(ar0, 0), withFrom(ar1, 1),
			withFrom(ar0b, 2), withFrom(ar1b, 3),
			withFrom(br0, 4), withFrom(br1, 5),
			withFrom(br0b, 6), withFrom(br1b, 7),
		},
	}
	_, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Canonical[0] != res.Canonical[2] {
		t.Fatalf("isomorphic a0/a0b were not folded: %d vs %d", res.Canonical[0], res.Canonical[2])
	}
	if res.Canonical[4] != res.Canonical[6] {
		t.Fatalf("isomorphic b0/b0b were not folded: %d vs %d", res.Canonical[4], res.Canonical[6])
	}
	if res.Canonical[8] != res.Canonical[10] {
		t.Fatalf("isomorphic c0/c0b were not folded: %d vs %d", res.Canonical[8], res.Canonical[10])
	}
	if res.Canonical[1] == res.Canonical[3] {
		t.Fatal("a1/a1b were incorrectly folded despite different transitive targets")
	}
	if res.Canonical[5] == res.Canonical[7] {
		t.Fatal("b1/b1b were incorrectly folded despite different targets")
	}
	if res.Canonical[9] == res.Canonical[11] {
		t.Fatal("c1/c1b were incorrectly folded")
	}
}

func TestICFSafeProtectsTwoAddressTakenSections(t *testing.T) {
	img := safeImage()
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Canonical[0] != 0 || res.Canonical[1] != 1 {
		t.Fatalf("two address-taken sections were merged: canonical=%v", res.Canonical)
	}
	if res.Merges[2] != 0 {
		t.Fatalf("unaddressed section was not folded into an addressed representative: %v", res.Merges)
	}
	if res.OldToNew[2] != res.OldToNew[0] {
		t.Fatalf("U did not map to A: oldToNew=%v", res.OldToNew)
	}
	// Symbolic relocation target and direct section target must both be
	// rewritten to the representative.
	uSym, _ := out.LookupSymbol("U")
	if uSym.Section != res.OldToNew[0] {
		t.Fatalf("U symbol section = %d, want %d", uSym.Section, res.OldToNew[0])
	}
	var directFound bool
	for _, rel := range out.Relocations {
		if rel.From == res.OldToNew[4] && rel.Type == RelocCall && rel.Symbol == "" {
			if rel.Target != res.OldToNew[0] {
				t.Fatalf("direct relocation target = %d, want %d", rel.Target, res.OldToNew[0])
			}
			directFound = true
		}
	}
	if !directFound {
		t.Fatal("direct relocation was lost")
	}
	if len(out.Sections) != 4 {
		t.Fatalf("sections = %d, want A,B,main,caller", len(out.Sections))
	}

	_, allRes, err := ICF(img, ICFAll)
	if err != nil {
		t.Fatal(err)
	}
	if allRes.Canonical[1] != 0 || allRes.Canonical[2] != 0 {
		t.Fatalf("icf=all did not fold addressed sections: %v", allRes.Canonical)
	}
}

func TestICFSafeMergesOnlyUnaddressedSections(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "a", Kind: KindCode, Data: []byte{1}},
			{Name: "b", Kind: KindCode, Data: []byte{1}},
			{Name: "c", Kind: KindCode, Data: []byte{1}},
		},
		Symbols: []Symbol{
			{Name: "a", Section: 0},
			{Name: "b", Section: 1},
			{Name: "c", Section: 2},
		},
	}
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sections) != 1 || res.Merges[1] != 0 || res.Merges[2] != 0 {
		t.Fatalf("unaddressed identical sections did not merge: %v", res.Merges)
	}
}

func TestICFSafeMergesManyUnaddressedIntoAddressed(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "A", Kind: KindCode, Data: []byte{1}},
			{Name: "U1", Kind: KindCode, Data: []byte{1}},
			{Name: "U2", Kind: KindCode, Data: []byte{1}},
			{Name: "U3", Kind: KindCode, Data: []byte{1}},
			{Name: "main", Kind: KindCode, Data: make([]byte, 8)},
		},
		Symbols: []Symbol{
			{Name: "A", Section: 0},
			{Name: "U1", Section: 1},
			{Name: "U2", Section: 2},
			{Name: "U3", Section: 3},
			{Name: "main", Section: 4},
		},
		Relocations: []Relocation{
			{From: 4, Offset: 0, Type: RelocAddrTaken, Symbol: "A", Size: 8},
		},
	}
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []SectionID{1, 2, 3} {
		if res.Merges[id] != 0 {
			t.Fatalf("section %d merged to %d, want 0", id, res.Merges[id])
		}
	}
	if len(out.Sections) != 2 {
		t.Fatalf("sections = %d, want A and main", len(out.Sections))
	}
}

func TestICFSafeChoosesAddressedRepresentativeRegardlessOfID(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "U", Kind: KindCode, Data: []byte{1}},
			{Name: "A", Kind: KindCode, Data: []byte{1}},
			{Name: "main", Kind: KindCode, Data: make([]byte, 8)},
		},
		Symbols: []Symbol{
			{Name: "U", Section: 0},
			{Name: "A", Section: 1},
			{Name: "main", Section: 2},
		},
		Relocations: []Relocation{
			{From: 2, Offset: 0, Type: RelocAddrTaken, Symbol: "A", Size: 8},
		},
	}
	out, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Canonical[0] != 1 {
		t.Fatalf("U canonicalized to %d, want addressed A", res.Canonical[0])
	}
	if res.OldToNew[1] == res.OldToNew[2] {
		t.Fatal("representative mapping is wrong")
	}
	if len(out.Sections) != 2 {
		t.Fatalf("sections = %d, want A and main", len(out.Sections))
	}
}

func TestGCThenSafeICFIgnoresAddressTakenFromDeadSections(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "A", Kind: KindCode, Data: []byte{1}},
			{Name: "B", Kind: KindCode, Data: []byte{1}},
			{Name: "U", Kind: KindCode, Data: []byte{1}},
			{Name: "main", Kind: KindCode, Data: make([]byte, 12)},
			{Name: "dead", Kind: KindCode, Data: make([]byte, 16)},
		},
		Symbols: []Symbol{
			{Name: "A", Section: 0},
			{Name: "B", Section: 1},
			{Name: "U", Section: 2},
			{Name: "main", Section: 3},
			{Name: "dead", Section: 4},
		},
		Relocations: []Relocation{
			{From: 3, Offset: 0, Type: RelocCall, Symbol: "A", Size: 4},
			{From: 3, Offset: 4, Type: RelocCall, Symbol: "B", Size: 4},
			{From: 3, Offset: 8, Type: RelocCall, Symbol: "U", Size: 4},
			{From: 4, Offset: 0, Type: RelocAddrTaken, Symbol: "A", Size: 8},
			{From: 4, Offset: 8, Type: RelocAddrTaken, Symbol: "B", Size: 8},
		},
		EntrySymbols: []string{"main"},
	}
	result, err := Link(img, LinkOptions{GCSections: true, ICF: ICFSafe})
	if err != nil {
		t.Fatal(err)
	}
	if result.ICF.Canonical[0] != result.ICF.Canonical[1] ||
		result.ICF.Canonical[1] != result.ICF.Canonical[2] {
		t.Fatalf("dead-section ADDR_TAKEN edges incorrectly protected sections: %v", result.ICF.Canonical)
	}
	if len(result.Image.Sections) != 2 {
		t.Fatalf("sections = %d, want A representative and main", len(result.Image.Sections))
	}
}

func TestICFFoldsReadOnlyDataWithRelocations(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "ro1", Kind: KindReadOnlyData, Data: make([]byte, 8)},
			{Name: "ro2", Kind: KindReadOnlyData, Data: make([]byte, 8)},
			{Name: "data", Kind: KindData, Flags: SHFWritable, Data: make([]byte, 8)},
		},
		Symbols: []Symbol{
			{Name: "ro1", Section: 0},
			{Name: "ro2", Section: 1},
			{Name: "data", Section: 2},
		},
		Relocations: []Relocation{
			{From: 0, Offset: 0, Type: RelocAddrTaken, Symbol: "data", Size: 8},
			{From: 1, Offset: 0, Type: RelocAddrTaken, Symbol: "data", Size: 8},
		},
	}
	_, res, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Canonical[1] != 0 {
		t.Fatalf("identical rodata was not folded: %v", res.Canonical)
	}
}

func TestParseICFMode(t *testing.T) {
	for in, want := range map[string]ICFMode{
		"": ICFDisabled, "none": ICFDisabled, "off": ICFDisabled,
		"safe": ICFSafe, "SAFE": ICFSafe, "--icf=safe": ICFSafe,
		"all": ICFAll, "icf=all": ICFAll,
	} {
		got, err := ParseICFMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseICFMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseICFMode("invalid"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
}

func TestICFExcludesNoICFWritableAndDataSections(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "a", Kind: KindCode, Data: []byte{1}},
			{Name: "b", Kind: KindCode, Data: []byte{1}},
			{Name: "noicf", Kind: KindCode, Flags: SHFNoICF, Data: []byte{1}},
			{Name: "wcode", Kind: KindCode, Flags: SHFWritable, Data: []byte{1}},
			{Name: "wdata", Kind: KindData, Flags: SHFWritable, Data: []byte{1}},
			{Name: "rodata", Kind: KindReadOnlyData, Data: []byte{1}},
		},
		Symbols: []Symbol{
			{Name: "a", Section: 0},
			{Name: "b", Section: 1},
			{Name: "noicf", Section: 2},
			{Name: "wcode", Section: 3},
			{Name: "wdata", Section: 4},
			{Name: "rodata", Section: 5},
		},
	}
	out, res, err := ICF(img, ICFAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Merges) != 1 || res.Merges[1] != 0 {
		t.Fatalf("unexpected merges: %v", res.Merges)
	}
	if len(out.Sections) != 5 {
		t.Fatalf("sections = %d, want 5", len(out.Sections))
	}
}

func safeImage() *Image {
	mainData := make([]byte, 32)
	return &Image{
		Sections: []Section{
			{Name: "A", Kind: KindCode, Data: []byte{0x01}},
			{Name: "B", Kind: KindCode, Data: []byte{0x01}},
			{Name: "U", Kind: KindCode, Data: []byte{0x01}},
			{Name: "main", Kind: KindCode, Data: mainData},
			{Name: "caller", Kind: KindCode, Data: []byte{0x99, 0, 0, 0}},
		},
		Symbols: []Symbol{
			{Name: "A", Section: 0},
			{Name: "B", Section: 1},
			{Name: "U", Section: 2},
			{Name: "main", Section: 3},
		},
		Relocations: []Relocation{
			{From: 3, Offset: 0, Type: RelocAddrTaken, Symbol: "A", Size: 8},
			{From: 3, Offset: 8, Type: RelocAddrTaken, Symbol: "B", Size: 8},
			{From: 3, Offset: 16, Type: RelocCall, Symbol: "U", Size: 4},
			{From: 4, Offset: 0, Type: RelocCall, Target: 2, Size: 4},
		},
	}
}

func withFrom(rel Relocation, from SectionID) Relocation {
	rel.From = from
	return rel
}

func TestICFGroupsAreDeterministic(t *testing.T) {
	code, rel := callSection("f")
	img := &Image{
		Sections: []Section{
			{Name: "f", Kind: KindCode, Data: code},
			{Name: "g", Kind: KindCode, Data: append([]byte(nil), code...)},
		},
		Symbols: []Symbol{
			{Name: "f", Section: 0},
			{Name: "g", Section: 1},
		},
		Relocations: []Relocation{
			withFrom(rel, 0),
			withFrom(rel, 1),
		},
	}
	_, a, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := ICF(img, ICFSafe)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Canonical, b.Canonical) || !reflect.DeepEqual(a.Merges, b.Merges) {
		t.Fatal("ICF output is not deterministic")
	}
}
