package commit_test

import (
	"errors"
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

const sectorSize = 8

// 场景一：原地互引。A、B 互为源且各自引用自身旧值。
// 写入瞬间所有源（含自己的入口旧值）必须驻留；覆盖后缓存副本仍可用。
func TestInPlaceMutualReferences(t *testing.T) {
	const a, b commit.SectorID = 10, 20
	cyls := map[commit.SectorID]commit.Cylinder{a: 1, b: 2}
	entry := map[commit.SectorID][]byte{
		a: {1, 1, 1, 1, 1, 1, 1, 1},
		b: {2, 2, 2, 2, 2, 2, 2, 2},
	}
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          2,
		Initial:    1,
		Cylinders:  cyls,
		ReadCost:   distanceCost,
		Targets: []commit.Target{
			{
				Address: a, Sector: 1,
				Fragments: []commit.Fragment{
					{Source: ptr(a), SourceOffset: 0, Length: 4},
					{Source: ptr(b), SourceOffset: 4, Length: 4},
				},
			},
			{
				Address: b, Sector: 2,
				Fragments: []commit.Fragment{
					{Source: ptr(a), SourceOffset: 4, Length: 4},
					{Source: ptr(b), SourceOffset: 0, Length: 4},
				},
			},
		},
	}

	plan, rec, err := commit.Commit(inst, &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(entry)}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if plan.Reads != 2 || rec.Reads != 2 {
		t.Fatalf("reads = %d/%d, want 2", plan.Reads, rec.Reads)
	}

	// K=1 下两个目标各需两个同时驻留的源，必然无解；但所有目标仍可“计算”出来。
	instK1 := *inst
	instK1.K = 1
	if _, _, err := commit.Commit(&instK1, &commit.MapDevice{Sectors: cloneDisk(entry)}); err == nil {
		t.Fatal("K=1 mutual references unexpectedly feasible")
	} else {
		var inf *commit.InfeasibleError
		if !errors.As(err, &inf) {
			t.Fatalf("want InfeasibleError, got %T: %v", err, err)
		}
	}

	// 独立枚举器必须给出同样的可行性与双指标。
	ok, cost, reads, err := commit.Enumerate(inst)
	if err != nil || !ok {
		t.Fatalf("enumerate: ok=%v err=%v", ok, err)
	}
	if cost != plan.ReadCost || reads != int64(plan.Reads) {
		t.Fatalf("enumerate (%d,%d) != plan (%d,%d)", cost, reads, plan.ReadCost, plan.Reads)
	}

	// 记录器重放 + 最终完整盘核对。
	spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(entry)}}
	plan2, rec2, err := commit.Commit(inst, spy)
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	assertSpyMatchesPlan(t, spy, plan2)
	md := spy.Inner.(*commit.MapDevice)
	assertFinalDisk(t, inst, entry, md.Sectors)
	if rec2.ReadCost != plan2.ReadCost {
		t.Fatalf("recorder cost %d != plan %d", rec2.ReadCost, plan2.ReadCost)
	}
	verifyHeadLog(t, rec2, inst)
}

// verifyHeadLog 逐拍核对：读/写后磁头位于其柱面，释放不移动磁头。
func verifyHeadLog(t *testing.T, rec *commit.Recorder, inst *commit.Instance) {
	t.Helper()
	head := inst.Initial
	var cost int64
	for _, tick := range rec.Log {
		if tick.HeadFrom != head {
			t.Fatalf("tick %d head-from %d != %d", tick.Index, tick.HeadFrom, head)
		}
		switch tick.Op.Kind {
		case "read", "write":
			if tick.HeadTo != tick.Op.Cyl {
				t.Fatalf("tick %d head-to %d != op cyl %d", tick.Index, tick.HeadTo, tick.Op.Cyl)
			}
			head = tick.HeadTo
		case "release":
			if tick.HeadTo != head {
				t.Fatalf("release tick %d moved head to %d", tick.Index, tick.HeadTo)
			}
		}
		cost += tick.ReadCost
	}
	if cost != rec.ReadCost {
		t.Fatalf("summed tick cost %d != recorder %d", cost, rec.ReadCost)
	}
}
