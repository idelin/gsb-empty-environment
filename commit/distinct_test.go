package commit_test

import (
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

// 场景四：两个片段字节内容完全相同，却来自不同入口扇区。
// 规划器不得以“内容相同”合并源：必须分别读取、分别占槽，写入时两个源同时驻留。
func TestIdenticalFragmentsFromDistinctSources(t *testing.T) {
	const x, y, tgt commit.SectorID = 71, 72, 73
	identical := mkSector(sectorSize, 5) // X、Y 入口内容逐字节相同
	cyls := map[commit.SectorID]commit.Cylinder{x: 4, y: 5, tgt: 6}
	entry := map[commit.SectorID][]byte{
		x:   append([]byte(nil), identical...),
		y:   append([]byte(nil), identical...),
		tgt: mkSector(sectorSize, 0),
	}
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          2,
		Initial:    4,
		Cylinders:  cyls,
		ReadCost:   distanceCost,
		Targets: []commit.Target{{
			Address: tgt, Sector: 6,
			Fragments: []commit.Fragment{
				{Source: ptr(x), SourceOffset: 0, Length: 4},
				{Source: ptr(y), SourceOffset: 0, Length: 4},
			},
		}},
	}

	spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(entry)}}
	plan, rec, err := commit.Commit(inst, spy)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if plan.Reads != 2 {
		t.Fatalf("reads = %d, want 2 distinct source reads", plan.Reads)
	}

	// 写拍之前必须有两个不同源同时驻留。
	for _, tick := range rec.Log {
		if tick.Op.Kind == "write" {
			if len(tick.Occupied) != 2 {
				t.Fatalf("occupied at write = %v, want both sources", tick.Occupied)
			}
		}
	}

	// K=1 下即使内容相同也无法提交（两个不同源需同时驻留）。
	inst1 := *inst
	inst1.K = 1
	if _, _, err := commit.Commit(&inst1, &commit.MapDevice{Sectors: cloneDisk(entry)}); err == nil {
		t.Fatal("K=1 with two distinct identical-content sources unexpectedly feasible")
	}

	assertSpyMatchesPlan(t, spy, plan)
	assertFinalDisk(t, inst, entry, spy.Inner.(*commit.MapDevice).Sectors)
	verifyHeadLog(t, rec, inst)

	ok, cost, reads, err := commit.Enumerate(inst)
	if err != nil || !ok {
		t.Fatalf("enumerate: ok=%v err=%v", ok, err)
	}
	if cost != plan.ReadCost || reads != int64(plan.Reads) {
		t.Fatalf("enumerate (%d,%d) != plan (%d,%d)", cost, reads, plan.ReadCost, plan.Reads)
	}
}
