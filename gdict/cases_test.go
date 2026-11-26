package gdict

import (
	"errors"
	"fmt"
	"testing"
)

func mkpre(v string, ks ...Key) Preorder {
	m := map[Key]int64{}
	for i, k := range ks {
		m[k] = int64(i)
	}
	return Preorder{Version: v, Ranks: m}
}

func TestSplitCollations(t *testing.T) {
	nul, empty, A, a, b := Null(), Text(""), Text("A"), Text("a"), Text("b")
	v1 := Preorder{Version: "v1", Ranks: map[Key]int64{nul: 0, empty: 1, A: 2, a: 2, b: 3}}
	v2 := Preorder{Version: "v2", Ranks: map[Key]int64{nul: 0, empty: 1, A: 2, a: 3, b: 4}}
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v1",
			Entries: []Entry{{10, nul}, {20, empty}, {30, A}, {40, a}, {50, b}},
			Cuts:    []Cut{{Name: "between-equal", OldCode: 35, Bound: LT}},
		}},
		Preorders: []Preorder{v1, v2},
		Space:     CodeSpace{Limit: 1 << 20},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(plan.String())
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	// A and a must not share a code: v2 strictly separates them and a live cut
	// splits their rows.
	codes := map[Key]uint64{}
	for _, kc := range plan.KeyCodes {
		codes[kc.Key] = kc.Code
	}
	if codes[A] == codes[a] {
		t.Fatalf("A and a share code %d", codes[A])
	}
	if codes[nul] == codes[empty] {
		t.Fatalf("NULL and empty share code")
	}
	if !(codes[nul] < codes[empty] && codes[empty] < codes[A] && codes[A] < codes[a] && codes[a] < codes[b]) {
		t.Fatalf("bad order: %v", codes)
	}
}

func TestFixedHole(t *testing.T) {
	a, b, c := Text("a"), Text("b"), Text("c")
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{0, a}, {1, b}, {2, c}},
		}},
		Preorders: []Preorder{mkpre("v", a, b, c)},
		Space:     CodeSpace{Limit: 100, Holes: []Interval{{1, 1}}},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(plan.String())
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	for _, kc := range plan.KeyCodes {
		if kc.Code == 1 {
			t.Fatalf("used hole code")
		}
	}
}

func TestFixedAnchor(t *testing.T) {
	a, b, c := Text("a"), Text("b"), Text("c")
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{0, a}, {1, b}, {2, c}},
		}},
		Preorders: []Preorder{mkpre("v", a, b, c)},
		Space:     CodeSpace{Limit: 100, Fixed: []Fixed{{Code: 50, Key: b}}},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(plan.String())
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	for _, kc := range plan.KeyCodes {
		if kc.Key == b && kc.Code != 50 {
			t.Fatalf("b not fixed")
		}
	}
}

func TestEnumerateSplitNullEmptyHole(t *testing.T) {
	nul, empty, A, a := Null(), Text(""), Text("A"), Text("a")
	v1 := Preorder{Version: "v1", Ranks: map[Key]int64{nul: 0, empty: 1, A: 2, a: 2}}
	v2 := Preorder{Version: "v2", Ranks: map[Key]int64{nul: 0, empty: 1, A: 2, a: 3}}
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v1",
			Entries: []Entry{{0, nul}, {1, empty}, {2, A}, {3, a}},
			Cuts:    []Cut{{Name: "split-group", OldCode: 2, Bound: LE}},
		}},
		Preorders: []Preorder{v1, v2},
		Space:     CodeSpace{Limit: 8, Holes: []Interval{{Lo: 4, Hi: 4}}},
	}
	want, err := exhaustiveOpt(t, cfg)
	if err != nil || want == nil {
		t.Fatalf("exhaustive oracle failed: %v", err)
	}
	got, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, got); err != nil {
		t.Fatal(err)
	}
	if got.Stats.ExceptionPages != want.Stats.ExceptionPages || got.Stats.UsedCodes != want.Stats.UsedCodes {
		t.Fatalf("enumeration mismatch got %+v want %+v", got.Stats, want.Stats)
	}
	for i := range got.Entries {
		if got.Entries[i].NewCode != want.Entries[i].NewCode {
			t.Fatalf("entry %d code %d, enumeration says %d", i, got.Entries[i].NewCode, want.Entries[i].NewCode)
		}
	}
	codes := map[Key]uint64{}
	for _, kc := range got.KeyCodes {
		codes[kc.Key] = kc.Code
	}
	if codes[A] == codes[a] || codes[nul] == codes[empty] {
		t.Fatalf("required distinctions lost: %v", codes)
	}
	for _, c := range codes {
		if c == 4 {
			t.Fatal("used hole boundary code 4")
		}
	}
}

func TestSearchBudgetIsExplicit(t *testing.T) {
	var keys []Key
	for i := 0; i < 9; i++ {
		keys = append(keys, Text(fmt.Sprintf("k%d", i)))
	}
	var entries []Entry
	for i, k := range keys {
		entries = append(entries, Entry{OldCode: uint64(i), Key: k})
	}
	ranks := map[Key]int64{}
	for _, k := range keys {
		ranks[k] = 1 // all universally equivalent, no cuts
	}
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: entries}},
		Preorders:  []Preorder{{Version: "v", Ranks: ranks}},
		Space:      CodeSpace{Limit: 100},
		MaxStates:  10,
	}
	if _, err := Solve(cfg); !errors.Is(err, ErrSearchLimit) {
		t.Fatalf("want ErrSearchLimit, got %v", err)
	}
}
