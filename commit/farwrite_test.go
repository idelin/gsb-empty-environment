package commit_test

import (
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

// 场景二：一个零读费的远端写改变最优读序。
//
// 磁头初始柱面 100。A、B、C 都在远端柱面 0。
// 目标 A 引用自身入口值与源 B；目标 B 引用源 C 与常量。K=2。
//
// 物理约束锁定顺序：写 A 需要 A、B 同时驻留；写 B 需要 C 驻留，
// 而 C 只能在槽位腾出后读入，故读 C 必然发生在写 A 之后。
// 写 A 是一次远端写，把磁头免费带到柱面 0；其后读 C 同柱面，严格零读费。
//
// 最优序列：读 A(100)、读 B(0) -> 写 A（磁头驻留 0）-> 读 C(0) -> 写 B，
// 总读费用 100、读 3 次，其中两次远端读为零费。
// 反事实“写不搬磁头”：每次读都从初始 100 寻道，三次读各 100，严格升到 300。
func TestZeroCostFarWriteReordersReads(t *testing.T) {
	const a, b, c commit.SectorID = 1, 2, 3
	cyls := map[commit.SectorID]commit.Cylinder{a: 0, b: 0, c: 0}
	entry := map[commit.SectorID][]byte{
		a: mkSector(sectorSize, 7),
		b: mkSector(sectorSize, 9),
		c: mkSector(sectorSize, 4),
	}
	inst := &commit.Instance{
		SectorSize: sectorSize,
		K:          2,
		Initial:    100,
		Cylinders:  cyls,
		ReadCost:   distanceCost,
		Targets: []commit.Target{
			{
				Address: a, Sector: 0,
				Fragments: []commit.Fragment{
					{Source: ptr(a), SourceOffset: 0, Length: 4},
					{Source: ptr(b), SourceOffset: 0, Length: 4},
				},
			},
			{
				Address: b, Sector: 0,
				Fragments: []commit.Fragment{
					{Source: ptr(c), SourceOffset: 0, Length: 4},
					{Length: 4, Constant: mkSector(4, 3)},
				},
			},
		},
	}

	spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(entry)}}
	plan, rec, err := commit.Commit(inst, spy)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if plan.ReadCost != 100 || plan.Reads != 3 {
		t.Fatalf("moving-write optimum (%d,%d), want (100,3)", plan.ReadCost, plan.Reads)
	}

	writeA, readC := -1, -1
	var readCCost int64 = -1
	zeroFar := 0
	for i, tick := range rec.Log {
		if tick.Op.Kind == "write" && tick.Op.Sector == a {
			writeA = i
		}
		if tick.Op.Kind == "read" && tick.Op.Sector == c {
			readC = i
			readCCost = tick.ReadCost
		}
		if tick.Op.Kind == "read" && tick.ReadCost == 0 {
			zeroFar++
		}
	}
	if writeA < 0 || readC < 0 || writeA > readC {
		t.Fatalf("want far write A (idx %d) strictly before read C (idx %d)", writeA, readC)
	}
	if readCCost != 0 {
		t.Fatalf("read C cost = %d, want 0 enabled by far write A", readCCost)
	}
	if zeroFar != 2 {
		t.Fatalf("zero-cost far reads = %d, want 2", zeroFar)
	}

	noMove := *inst
	noMove.ReadCost = func(_, cyl commit.Cylinder) int64 {
		return absCyl(int64(100) - int64(cyl))
	}
	cost2, reads2, err := commit.Quote(&noMove)
	if err != nil {
		t.Fatalf("quote no-move: %v", err)
	}
	if cost2 != 300 || reads2 != 3 {
		t.Fatalf("no-move counterfactual (%d,%d), want (300,3)", cost2, reads2)
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
