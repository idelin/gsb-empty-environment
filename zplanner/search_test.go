package zplanner

import (
	"errors"
	"testing"
)

func emptyTable() Table {
	return Table{
		LastOp: -1,
		Loc:    map[CID]Phys{},
		Roots:  map[string]CID{},
		Snaps:  map[string]map[string]CID{},
	}
}

func TestPlanSimpleWritesNeedNoCopiesOrErases(t *testing.T) {
	init := InitialState{Table: emptyTable()}
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"a": 1}},
		{ID: 1, Kind: OpWrite, Writes: map[string]CID{"b": 2}},
	}
	plan, err := Plan(init, ops)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Cost != (Cost{}) {
		t.Fatalf("expected zero cost, got %+v", plan.Cost)
	}
	if err := ValidatePlan(init, ops, plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
}

func TestPlanReturnsInfeasibleWhenAllZonesFullAndPinned(t *testing.T) {
	// Every zone is full and each holds a distinct live CID, so no zone can
	// be reset and there is nowhere to append a new page.
	init := InitialState{Table: emptyTable()}
	var fill [MaxZones]int
	for z := ZoneID(0); z < MaxZones; z++ {
		fill[z] = PagesPerZone
		cid := CID(z + 1)
		init.Table.Loc[cid] = Phys{Zone: z, Off: 0}
		init.Table.Roots[pageName(cid)] = cid
	}
	init.Fill = fill
	ops := []Op{{ID: 0, Kind: OpWrite, Writes: map[string]CID{"b": 6}}}
	_, err := Plan(init, ops)
	var infeasible *ErrInfeasible
	if !errors.As(err, &infeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
}

func TestPlanRequiresMigrationWhenGarbageFillsDevice(t *testing.T) {
	// All zones are full, but only zone 0 holds live data. A reset frees
	// another zone with zero copies because garbage zones are not pinned.
	init := InitialState{Table: emptyTable()}
	var fill [MaxZones]int
	for z := range fill {
		fill[z] = PagesPerZone
	}
	init.Fill = fill
	init.Table.Loc[1] = Phys{Zone: 0, Off: 0}
	init.Table.Roots["a"] = 1
	ops := []Op{{ID: 0, Kind: OpWrite, Writes: map[string]CID{"b": 2}}}
	plan, err := Plan(init, ops)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Cost.Copies != 0 || plan.Cost.Erases != 1 {
		t.Fatalf("expected {0,1}, got %+v", plan.Cost)
	}
	if err := ValidatePlan(init, ops, plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
}

func TestPlanCopiesBeforeErasingPinnedZone(t *testing.T) {
	// Every non-empty zone is full and pinned by live content, with exactly
	// one free page sitting in one of the pinned zones. Two new pages are
	// required, so a live CID must be copied into that free page before its
	// source zone can be reset: exactly one copy, then one erase.
	init := InitialState{Table: emptyTable()}
	init.Fill[0] = PagesPerZone
	init.Fill[1] = PagesPerZone - 1
	for z := 2; z < MaxZones; z++ {
		init.Fill[z] = PagesPerZone
	}
	init.Table.Loc[1] = Phys{Zone: 0, Off: 0}
	init.Table.Roots["a"] = 1
	for z := ZoneID(1); z < MaxZones; z++ {
		cid := CID(z + 1)
		init.Table.Loc[cid] = Phys{Zone: z, Off: 0}
		init.Table.Roots[pageName(cid)] = cid
	}
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"b": 6, "c": 7}},
	}
	plan, err := Plan(init, ops)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := ValidatePlan(init, ops, plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if plan.Cost.Copies != 1 {
		t.Fatalf("expected exactly one relocation copy, got %+v", plan.Cost)
	}
	if plan.Cost.Erases < 1 {
		t.Fatalf("expected at least one erase, got %+v", plan.Cost)
	}
}

func TestPlanSnapshotSharingKeepsOnePhysicalCopy(t *testing.T) {
	init := InitialState{Table: emptyTable()}
	ops := []Op{
		{ID: 0, Kind: OpWrite, Writes: map[string]CID{"a": 1}},
		{ID: 1, Kind: OpSnapshot, Name: "s1"},
		{ID: 2, Kind: OpWrite, Writes: map[string]CID{"a": 2}},
		{ID: 3, Kind: OpDeleteSnapshot, Name: "s1"},
	}
	plan, err := Plan(init, ops)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := ValidatePlan(init, ops, plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	final := plan.Steps[len(plan.Steps)-1].Table
	// CID 1 was only referenced by the deleted snapshot; it must be gone.
	if _, ok := final.Loc[1]; ok {
		t.Fatalf("CID 1 should be reclaimed after snapshot deletion")
	}
	if final.Roots["a"] != 2 {
		t.Fatalf("root a should point at CID 2, got %d", final.Roots["a"])
	}
}

func TestPlanExceedsCIDLimit(t *testing.T) {
	init := InitialState{Table: emptyTable()}
	ops := []Op{{ID: 0, Kind: OpWrite, Writes: func() map[string]CID {
		m := map[string]CID{}
		for i := CID(1); i <= MaxCIDs+1; i++ {
			m[pageName(i)] = i
		}
		return m
	}()}}
	_, err := Plan(init, ops)
	var infeasible *ErrInfeasible
	if !errors.As(err, &infeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
}

func pageName(id CID) string {
	return "p" + string(rune('0'+id))
}
