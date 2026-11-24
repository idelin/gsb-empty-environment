package zplanner

import (
	"testing"
	"time"
)

func TestStressMaxSize(t *testing.T) {
	init := InitialState{Table: emptyTable()}
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"a": 1, "b": 2}},
		{ID: 1, Kind: OpSnapshot, Name: "s"},
		{ID: 2, Kind: OpWrite, Writes: map[string]CID{"a": 3, "c": 4}},
		{ID: 3, Kind: OpSnapshot, Name: "t"},
		{ID: 4, Kind: OpWrite, Writes: map[string]CID{"b": 5, "c": 6}},
		{ID: 5, Kind: OpDeleteSnapshot, Name: "s"},
	}
	start := time.Now()
	p, err := Plan(init, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(init, ops, p); err != nil {
		t.Fatal(err)
	}
	t.Logf("cost=%+v steps=%d dur=%v", p.Cost, len(p.Steps), time.Since(start))
}

func TestStressForcedRelocations(t *testing.T) {
	// Every non-empty zone is pinned by live content and only zone 1 has a
	// single free page. The first write must copy a live CID into that page,
	// then reset its source zone to make room.
	init := InitialState{Table: emptyTable()}
	for z := ZoneID(0); z < MaxZones; z++ {
		init.Fill[z] = PagesPerZone
	}
	init.Fill[1] = PagesPerZone - 1
	for z := ZoneID(0); z < MaxZones; z++ {
		cid := CID(z + 1)
		init.Table.Loc[cid] = Phys{Zone: z, Off: 0}
		init.Table.Roots[pageName(cid)] = cid
	}
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"x1": 6, "x2": 7}},
	}
	start := time.Now()
	p, err := Plan(init, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(init, ops, p); err != nil {
		t.Fatal(err)
	}
	if p.Cost.Copies == 0 {
		t.Fatalf("expected relocation copies, got %+v", p.Cost)
	}
	t.Logf("cost=%+v steps=%d dur=%v", p.Cost, len(p.Steps), time.Since(start))
}

func TestStressTightSpace(t *testing.T) {
	// Pre-fill most zones with garbage and a few pinned live pages, forcing
	// repeated relocation/erase cycles across a six-operation sequence.
	init := InitialState{Table: emptyTable()}
	init.Fill[0] = PagesPerZone
	init.Fill[1] = PagesPerZone
	init.Fill[2] = PagesPerZone - 2
	init.Fill[3] = PagesPerZone
	init.Fill[4] = PagesPerZone - 1
	init.Table.Loc[1] = Phys{Zone: 0, Off: 0}
	init.Table.Roots["a"] = 1
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"b": 2}},
		{ID: 1, Kind: OpSnapshot, Name: "s"},
		{ID: 2, Kind: OpWrite, Writes: map[string]CID{"c": 3}},
		{ID: 3, Kind: OpWrite, Writes: map[string]CID{"d": 4}},
		{ID: 4, Kind: OpDeleteSnapshot, Name: "s"},
		{ID: 5, Kind: OpWrite, Writes: map[string]CID{"e": 5}},
	}
	start := time.Now()
	p, err := Plan(init, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(init, ops, p); err != nil {
		t.Fatal(err)
	}
	t.Logf("cost=%+v steps=%d dur=%v", p.Cost, len(p.Steps), time.Since(start))
}
