package commit_test

import (
	"errors"
	"math/rand"
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

// genInstance 随机构造一个约束内的合法实例：稀疏地址、随机柱面、随机源切片/常量。
func genInstance(rng *rand.Rand, k int) (*commit.Instance, map[commit.SectorID][]byte) {
	const size = 8
	nTargets := 1 + rng.Intn(5)
	nSectors := nTargets + rng.Intn(5)

	ids := make([]commit.SectorID, nSectors)
	cyls := map[commit.SectorID]commit.Cylinder{}
	disk := map[commit.SectorID][]byte{}
	for i := range ids {
		ids[i] = commit.SectorID(100 + i*7 + rng.Intn(3)) // 稀疏且不保证有序
		cyls[ids[i]] = commit.Cylinder(rng.Intn(12) - 3)
		buf := make([]byte, size)
		rng.Read(buf)
		disk[ids[i]] = buf
	}

	targets := make([]commit.Target, nTargets)
	for t := range targets {
		addr := ids[t]
		var frags []commit.Fragment
		off := 0
		for off < size {
			length := 1 + rng.Intn(size-off)
			if rng.Intn(3) == 0 {
				buf := make([]byte, length)
				rng.Read(buf)
				frags = append(frags, commit.Fragment{Length: length, Constant: buf})
			} else {
				src := ids[rng.Intn(nSectors)]
				start := rng.Intn(size - length + 1)
				frags = append(frags, commit.Fragment{
					Source: ptr(src), SourceOffset: start, Length: length,
				})
			}
			off += length
		}
		targets[t] = commit.Target{Address: addr, Sector: cyls[addr], Fragments: frags}
	}

	// 费用函数随机选一种：柱面距离或固定权重，保证非负。
	mode := rng.Intn(2)
	cost := func(head, cyl commit.Cylinder) int64 {
		if mode == 0 {
			return absCyl(int64(head) - int64(cyl))
		}
		return absCyl(int64(head)-int64(cyl)) * 2
	}
	inst := &commit.Instance{
		SectorSize: size,
		K:          k,
		Initial:    commit.Cylinder(rng.Intn(12) - 3),
		Cylinders:  cyls,
		ReadCost:   cost,
		Targets:    targets,
	}
	return inst, disk
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	const iterations = 400
	feasibleCount := 0
	infeasibleCount := 0
	for iter := 0; iter < iterations; iter++ {
		k := 1 + rng.Intn(4)
		inst, disk := genInstance(rng, k)

		ok, enumCost, enumReads, err := commit.Enumerate(inst)
		if err != nil {
			t.Fatalf("iter %d enumerate: %v", iter, err)
		}

		plan, planErr := commit.BuildPlan(inst)
		if !ok {
			infeasibleCount++
			var inf *commit.InfeasibleError
			if !errors.As(planErr, &inf) {
				t.Fatalf("iter %d want InfeasibleError, got %v", iter, planErr)
			}
			continue
		}
		feasibleCount++
		if planErr != nil {
			t.Fatalf("iter %d plan: %v", iter, planErr)
		}
		if plan.ReadCost != enumCost || int64(plan.Reads) != enumReads {
			t.Fatalf("iter %d metrics plan=(%d,%d) enum=(%d,%d)",
				iter, plan.ReadCost, plan.Reads, enumCost, enumReads)
		}

		spy := &commit.SpyDevice{Inner: &commit.MapDevice{Sectors: cloneDisk(disk)}}
		execPlan, rec, err := commit.Commit(inst, spy)
		if err != nil {
			t.Fatalf("iter %d commit: %v", iter, err)
		}
		if execPlan.ReadCost != enumCost {
			t.Fatalf("iter %d executed cost %d != enum %d", iter, execPlan.ReadCost, enumCost)
		}
		assertSpyMatchesPlan(t, spy, execPlan)
		assertFinalDisk(t, inst, disk, spy.Inner.(*commit.MapDevice).Sectors)
		verifyHeadLog(t, rec, inst)
		if len(rec.Log) == 0 {
			t.Fatalf("iter %d empty recorder log", iter)
		}
	}
	if feasibleCount == 0 || infeasibleCount == 0 {
		t.Fatalf("coverage weak: feasible=%d infeasible=%d", feasibleCount, infeasibleCount)
	}
	t.Logf("cross-check done: feasible=%d infeasible=%d", feasibleCount, infeasibleCount)
}
