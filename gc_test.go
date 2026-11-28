package linker

import (
	"reflect"
	"testing"
)

func TestGCSectionsReachabilityAndKeepRoots(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: ".text.main", Kind: KindCode, Data: []byte{0x11}},
			{Name: ".text.helper", Kind: KindCode, Data: []byte{0x22}},
			{Name: ".text.dead", Kind: KindCode, Data: []byte{0x33}},
			{Name: ".rodata.live", Kind: KindReadOnlyData, Data: []byte{0x44}},
			{Name: ".rodata.dead", Kind: KindReadOnlyData, Data: []byte{0x55}},
			{Name: ".init_array", Kind: KindData, Flags: SHFKeep, Data: make([]byte, 16)},
			{Name: ".text.init", Kind: KindCode, Data: []byte{0x66}},
			{Name: ".data.dead", Kind: KindData, Flags: SHFWritable, Data: make([]byte, 8)},
		},
		Symbols: []Symbol{
			{Name: "main", Section: 0},
			{Name: "helper", Section: 1},
			{Name: "dead", Section: 2},
			{Name: "live_ro", Section: 3},
			{Name: "dead_ro", Section: 4},
			{Name: "init_fn", Section: 6},
		},
		Relocations: []Relocation{
			{From: 0, Offset: 0, Type: RelocCall, Symbol: "helper", Size: 4},
			{From: 0, Offset: 1, Type: RelocAddrTaken, Symbol: "live_ro", Size: 8},
			{From: 2, Offset: 0, Type: RelocCall, Symbol: "dead_ro", Size: 4},
			{From: 5, Offset: 0, Type: RelocAddrTaken, Symbol: "init_fn", Size: 8},
		},
		EntrySymbols: []string{"main"},
	}

	out, res, err := GCSections(img)
	if err != nil {
		t.Fatal(err)
	}
	wantLive := []SectionID{0, 1, 3, 5, 6}
	wantDead := []SectionID{2, 4, 7}
	if !reflect.DeepEqual(res.Live, wantLive) {
		t.Fatalf("live = %v, want %v", res.Live, wantLive)
	}
	if !reflect.DeepEqual(res.Dead, wantDead) {
		t.Fatalf("dead = %v, want %v", res.Dead, wantDead)
	}
	if len(out.Sections) != len(wantLive) {
		t.Fatalf("output has %d sections, want %d", len(out.Sections), len(wantLive))
	}
	if out.Sections[res.OldToNew[5]].Flags&SHFKeep == 0 {
		t.Fatalf("SHF_KEEP flag was not retained")
	}
	if res.OldToNew[0] != 0 || res.OldToNew[1] != 1 || res.OldToNew[3] != 2 || res.OldToNew[5] != 3 || res.OldToNew[6] != 4 {
		t.Fatalf("unexpected section remap: %v", res.OldToNew)
	}
	if res.RetainedRelocations != 3 {
		t.Fatalf("retained relocations = %d, want 3", res.RetainedRelocations)
	}
	if len(out.Relocations) != 3 {
		t.Fatalf("output relocations = %d, want 3", len(out.Relocations))
	}
	// The .init_array -> .text.init edge must still resolve after rebasing.
	var found bool
	for _, rel := range out.Relocations {
		if rel.From == res.OldToNew[5] && rel.Symbol == "init_fn" {
			target, err := out.resolveRelocation(rel, out.SymbolMap())
			if err != nil {
				t.Fatal(err)
			}
			if target.Section != res.OldToNew[6] {
				t.Fatalf("init relocation points to %d, want %d", target.Section, res.OldToNew[6])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("keep-section relocation was not retained")
	}
	if _, ok := out.LookupSymbol("dead"); ok {
		t.Fatal("symbol in unreachable section survived GC")
	}
}

func TestGCSectionsRootsOnlyFromEntryAndKeep(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "root", Kind: KindCode, Data: []byte{1}},
			{Name: "child", Kind: KindCode, Data: []byte{2}},
			{Name: "grandchild", Kind: KindCode, Data: []byte{3}},
			{Name: "unreachable", Kind: KindCode, Data: []byte{4}},
		},
		Symbols: []Symbol{
			{Name: "root", Section: 0},
			{Name: "child", Section: 1},
			{Name: "grandchild", Section: 2},
			{Name: "unreachable", Section: 3},
		},
		Relocations: []Relocation{
			{From: 0, Offset: 0, Type: RelocCall, Symbol: "child"},
			{From: 1, Offset: 0, Type: RelocAddrTaken, Symbol: "grandchild"},
			{From: 3, Offset: 0, Type: RelocCall, Symbol: "child"},
		},
		EntrySymbols: []string{"root"},
	}
	out, res, err := GCSections(img)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Live, []SectionID{0, 1, 2}) {
		t.Fatalf("live = %v", res.Live)
	}
	if len(out.Relocations) != 2 {
		t.Fatalf("relocations = %d, want 2", len(out.Relocations))
	}
}

func TestGCSectionsKeepsExternalAndDirectEdges(t *testing.T) {
	img := &Image{
		Sections: []Section{
			{Name: "main", Kind: KindCode, Data: make([]byte, 16)},
			{Name: "direct", Kind: KindCode, Data: []byte{1}},
			{Name: "dead", Kind: KindCode, Data: []byte{2}},
		},
		Symbols: []Symbol{
			{Name: "main", Section: 0},
			{Name: "direct", Section: 1},
			{Name: "dead", Section: 2},
		},
		Relocations: []Relocation{
			{From: 0, Offset: 0, Type: RelocCall, Symbol: "printf", Size: 8},
			{From: 0, Offset: 8, Type: RelocCall, Target: 1, Size: 8},
		},
		EntrySymbols: []string{"main"},
	}
	out, res, err := GCSections(img)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Live, []SectionID{0, 1}) {
		t.Fatalf("live = %v, want main and direct", res.Live)
	}
	if len(out.Relocations) != 2 {
		t.Fatalf("external/direct relocations were not retained: %d", len(out.Relocations))
	}
	if out.Relocations[1].Target != res.OldToNew[1] {
		t.Fatalf("direct target = %d, want %d", out.Relocations[1].Target, res.OldToNew[1])
	}
}
