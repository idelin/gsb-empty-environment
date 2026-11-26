package gdict

import (
	"reflect"
	"testing"
)

func TestSparseUint64AndHoleBoundaries(t *testing.T) {
	a, b, c, d := Text("a"), Text("b"), Text("c"), Text("d")
	const high = uint64(1) << 40
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{OldCode: high, Key: a}, {OldCode: high + 10, Key: b}, {OldCode: high + 20, Key: c}},
			Cuts:    []Cut{{Name: "at-hole", OldCode: high + 15, Bound: LE}},
		}},
		Preorders: []Preorder{mkpre("v", a, b, c, d)},
		Space: CodeSpace{
			Limit: 1 << 50,
			Holes: []Interval{{Lo: 5, Hi: 9}, {Lo: 1 << 49, Hi: 1 << 49}},
			Fixed: []Fixed{{Code: 4, Key: a}},
		},
		MaxStates: 500000,
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	for _, kc := range plan.KeyCodes {
		if kc.Code >= cfg.Space.Limit {
			t.Fatalf("code outside space: %d", kc.Code)
		}
		for _, h := range cfg.Space.Holes {
			if h.Lo <= kc.Code && kc.Code <= h.Hi {
				t.Fatalf("code %d in hole %+v", kc.Code, h)
			}
		}
	}
	seen := map[uint64]bool{}
	for _, kc := range plan.KeyCodes {
		seen[kc.Code] = true
	}
	if len(seen) != plan.Stats.UsedCodes {
		t.Fatal("stats mismatch")
	}
}

func TestLTvsLEAtSameCode(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{OldCode: 0, Key: a}, {OldCode: 10, Key: b}},
			Cuts: []Cut{
				{Name: "lt", OldCode: 10, Bound: LT},
				{Name: "le", OldCode: 10, Bound: LE},
			},
		}},
		Preorders: []Preorder{mkpre("v", a, b)},
		Space:     CodeSpace{Limit: 100},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	part := plan.Partitions[0]
	if len(part.Pages) != 1 {
		t.Fatalf("want one page, got %+v", part.Pages)
	}
	// With delta 0, LT at 10 excludes b and LE includes it. If the optimizer
	// moved codes, Verify above still checked both concrete row sets.
	_ = part
}

func TestAliasAndLookup(t *testing.T) {
	usa, us := Text("USA"), Text("US")
	cfg := Config{
		Aliases: []Alias{{Name: "country", Members: []Key{usa, us}}},
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{OldCode: 3, Key: usa}},
		}, {
			Name: "q", Version: "v",
			Entries: []Entry{{OldCode: 8, Key: us}},
		}},
		Preorders: []Preorder{{Version: "v", Ranks: map[Key]int64{usa: 1, us: 1, Text("x"): 2}}},
		Space:     CodeSpace{Limit: 100},
		Lookups: []Lookup{
			{Name: "p-usa", Partition: "p", Key: us},
			{Name: "q-us", Partition: "q", Key: usa},
		},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	var c1, c2 uint64
	for _, kc := range plan.KeyCodes {
		if kc.Key == usa {
			c1 = kc.Code
		}
		if kc.Key == us {
			c2 = kc.Code
		}
	}
	if c1 != c2 {
		t.Fatalf("alias members not unified: %d != %d", c1, c2)
	}
}

func TestNullPositionAndEmptyString(t *testing.T) {
	nul, empty, x := Null(), Text(""), Text("x")
	// NULL sorts last in this version; empty string remains distinct.
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{OldCode: 0, Key: empty}, {OldCode: 1, Key: x}, {OldCode: 2, Key: nul}},
		}},
		Preorders: []Preorder{{Version: "v", Ranks: map[Key]int64{empty: 0, x: 1, nul: 2}}},
		Space:     CodeSpace{Limit: 100},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	codes := map[Key]uint64{}
	for _, kc := range plan.KeyCodes {
		codes[kc.Key] = kc.Code
	}
	if !(codes[empty] < codes[x] && codes[x] < codes[nul]) {
		t.Fatalf("bad NULL/empty order: %v", codes)
	}
	if codes[empty] == codes[nul] {
		t.Fatal("NULL and empty string collapsed")
	}
}

func TestDeterministic(t *testing.T) {
	a, b, c := Text("a"), Text("b"), Text("c")
	cfg := Config{
		Partitions: []Partition{{
			Name: "p", Version: "v",
			Entries: []Entry{{10, a}, {20, b}, {30, c}},
			Cuts:    []Cut{{Name: "c", OldCode: 25, Bound: LT}},
		}},
		Preorders: []Preorder{mkpre("v", a, b, c)},
		Space:     CodeSpace{Limit: 1 << 20, Holes: []Interval{{Lo: 4, Hi: 4}}},
	}
	p1, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("non-deterministic plan\n%s\n%s", p1.String(), p2.String())
	}
}

func TestSharedFixedCodeIsReviewable(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {10, b}}}},
		Preorders:  []Preorder{{Version: "v", Ranks: map[Key]int64{a: 1, b: 1}}},
		Space:      CodeSpace{Limit: 6, Fixed: []Fixed{{Code: 5, Key: b}}},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	for _, kc := range plan.KeyCodes {
		if kc.Key == a && kc.Code != 5 {
			t.Fatalf("equivalent non-fixed key did not share fixed code: %d", kc.Code)
		}
	}
}

func TestRepeatedLogicalKeyWithoutSplit(t *testing.T) {
	a := Text("a")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {10, a}}}},
		Preorders:  []Preorder{mkpre("v", a)},
		Space:      CodeSpace{Limit: 100},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(cfg, plan); err != nil {
		t.Fatal(err)
	}
	if plan.Entries[0].NewCode != plan.Entries[1].NewCode {
		t.Fatalf("one logical key split without a cut: %+v", plan.Entries)
	}
}
