package commit_test

import (
	"strings"
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

func TestPlanningDoesNotTouchDisk(t *testing.T) {
	const a commit.SectorID = 1
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          1,
		Initial:    0,
		Cylinders:  map[commit.SectorID]commit.Cylinder{a: 0},
		ReadCost:   distanceCost,
		Targets: []commit.Target{{
			Address: a, Sector: 0,
			Fragments: []commit.Fragment{
				{Source: ptr(a), SourceOffset: 0, Length: sectorSize},
			},
		}},
	}
	// Quote 与 BuildPlan 都不得产生任何底盘访问。
	spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: map[commit.SectorID][]byte{
		a: mkSector(sectorSize, 1),
	}}}
	if _, _, err := commit.Quote(inst); err != nil {
		t.Fatalf("quote: %v", err)
	}
	if _, err := commit.BuildPlan(inst); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(spy.Calls) != 0 {
		t.Fatalf("planning touched disk %d times: %v", len(spy.Calls), spy.Calls)
	}
}

func TestValidationRejectsBadInstances(t *testing.T) {
	base := commit.Instance{
		SectorSize: sectorSize,
		K:          1,
		Cylinders:  map[commit.SectorID]commit.Cylinder{1: 0},
		ReadCost:   distanceCost,
		Targets: []commit.Target{{
			Address: 1, Sector: 0,
			Fragments: []commit.Fragment{
				{Source: ptr(1), SourceOffset: 0, Length: sectorSize},
			},
		}},
	}
	cases := map[string]func(*commit.Instance){
		"zero sector size": func(i *commit.Instance) { i.SectorSize = 0 },
		"negative K":       func(i *commit.Instance) { i.K = -1 },
		"nil cost":         func(i *commit.Instance) { i.ReadCost = nil },
		"bad total length": func(i *commit.Instance) {
			i.Targets[0].Fragments[0].Length = sectorSize - 1
		},
		"slice out of range": func(i *commit.Instance) {
			i.Targets[0].Fragments[0].SourceOffset = 1
		},
		"negative cost": func(i *commit.Instance) {
			i.ReadCost = func(_, _ commit.Cylinder) int64 { return -1 }
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			i := base
			mutate(&i)
			if _, _, err := commit.Quote(&i); err == nil {
				t.Fatalf("%s: expected error", name)
			}
		})
	}
}

func TestRecorderRejectsIllegalWrite(t *testing.T) {
	const a, b commit.SectorID = 1, 2
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          1,
		Initial:    0,
		Cylinders:  map[commit.SectorID]commit.Cylinder{a: 0, b: 1},
		ReadCost:   distanceCost,
		Targets: []commit.Target{{
			Address: a, Sector: 0,
			Fragments: []commit.Fragment{
				{Source: ptr(b), SourceOffset: 0, Length: sectorSize},
			},
		}},
	}
	dev := &commit.MapDevice{Sectors: map[commit.SectorID][]byte{
		a: mkSector(sectorSize, 0),
		b: mkSector(sectorSize, 1),
	}}
	rec := commit.NewRecorder(inst, dev)
	// 未读源 B 就写 A：组装寄存器缺源，必须被记录器拒绝。
	err := rec.Step(commit.Op{Kind: "write", Sector: a, Cyl: 0})
	if err == nil || !strings.Contains(err.Error(), "not resident") {
		t.Fatalf("want not-resident error, got %v", err)
	}
}
