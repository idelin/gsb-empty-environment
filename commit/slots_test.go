package commit_test

import (
	"errors"
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

// 场景三：每个目标的负载都能由配方计算出来，但存在一个需要 K+1 个源
// 同时驻留的写指令，在槽位限制下无法提交；独立枚举同样判定无解。
func TestComputableButNotSubmittable(t *testing.T) {
	const tgt commit.SectorID = 50
	cyls := map[commit.SectorID]commit.Cylinder{
		tgt: 0, 51: 1, 52: 2, 53: 3,
	}
	entry := map[commit.SectorID][]byte{
		tgt: mkSector(sectorSize, 0),
		51:  mkSector(sectorSize, 1),
		52:  mkSector(sectorSize, 2),
		53:  mkSector(sectorSize, 3),
	}
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          2,
		Initial:    0,
		Cylinders:  cyls,
		ReadCost:   distanceCost,
		Targets: []commit.Target{{
			Address: tgt, Sector: 0,
			Fragments: []commit.Fragment{
				{Source: ptr(51), SourceOffset: 0, Length: 3},
				{Source: ptr(52), SourceOffset: 0, Length: 3},
				{Source: ptr(53), SourceOffset: 0, Length: 2},
			},
		}},
	}

	// 期望负载完全可以独立计算出来。
	want, err := commit.ExpectedPayloads(inst, entry)
	if err != nil {
		t.Fatalf("expected payloads: %v", err)
	}
	if len(want[tgt]) != sectorSize {
		t.Fatalf("payload not computable: %v", want[tgt])
	}

	_, _, err = commit.Commit(inst, &commit.MapDevice{Sectors: cloneDisk(entry)})
	var inf *commit.InfeasibleError
	if !errors.As(err, &inf) {
		t.Fatalf("want InfeasibleError, got %v", err)
	}

	ok, _, _, enumErr := commit.Enumerate(inst)
	if enumErr != nil {
		t.Fatalf("enumerate error: %v", enumErr)
	}
	if ok {
		t.Fatal("independent enumeration unexpectedly feasible")
	}

	// 同一实例 K=3 即可提交，证明问题确实是槽位而非配方。
	inst3 := *inst
	inst3.K = 3
	spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(entry)}}
	plan, rec, err := commit.Commit(&inst3, spy)
	if err != nil {
		t.Fatalf("K=3 commit: %v", err)
	}
	if plan.Reads != 3 {
		t.Fatalf("K=3 reads = %d, want 3", plan.Reads)
	}
	assertSpyMatchesPlan(t, spy, plan)
	assertFinalDisk(t, &inst3, entry, spy.Inner.(*commit.MapDevice).Sectors)
	verifyHeadLog(t, rec, &inst3)
}
