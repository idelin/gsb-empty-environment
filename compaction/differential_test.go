package compaction

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// TestRandomDifferential 在随机小数据库上枚举全部候选页子集，用参考
// 语义逐观测求值，与 Solve 的结论、成本与并列选择逐项对照。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m := randomManifest(rng)
		cands := m.sortedCandidates()
		store := baselineStore(m)
		obs := observations(m, store, buildSnapshots(m))

		sol, err := Solve(m)

		// 参考枚举：找出全部可行子集及最优（成本、页数、向量字典序）。
		var feasibleMasks []uint32
		for mask := uint32(0); mask < 1<<len(cands); mask++ {
			if simulateSubset(m, cands, mask, store, obs) {
				feasibleMasks = append(feasibleMasks, mask)
			}
		}
		if len(feasibleMasks) == 0 {
			var nse *NoSolutionError
			if !errors.As(err, &nse) {
				t.Fatalf("seed %d: want NoSolutionError, got %v", seed, err)
			}
			checkMinimalCore(t, m, cands, store, obs, nse)
			continue
		}
		if err != nil {
			t.Fatalf("seed %d: unexpected solver error %v", seed, err)
		}

		var refMask uint32
		refCost, refCount := -1, 0
		for _, mask := range feasibleMasks {
			cost := subsetCost(m, cands, mask)
			count := bitsCount(mask)
			if refCost == -1 || betterRef(mask, cost, count, refMask, refCost, refCount) {
				refMask, refCost, refCount = mask, cost, count
			}
		}
		if sol.TotalBytes != refCost || sol.PageCount != refCount {
			t.Fatalf("seed %d: solver cost=%d count=%d, ref cost=%d count=%d",
				seed, sol.TotalBytes, sol.PageCount, refCost, refCount)
		}
		var gotMask uint32
		for i, c := range cands {
			for _, id := range sol.SelectedIDs {
				if c.ID == id {
					gotMask |= 1 << i
				}
			}
		}
		if gotMask != refMask {
			t.Fatalf("seed %d: selection mask %b differs from reference %b", seed, gotMask, refMask)
		}

		// 安装器独立复核：最优解必须通过，任意可行/不可行子集的结论
		// 也要与参考语义一致（反例报告本身即可证明不一致）。
		rep, ierr := Install(m, sol.SelectedIDs)
		if ierr != nil || !rep.Verified() {
			t.Fatalf("seed %d: optimal solution failed install: %v %+v", seed, ierr, rep)
		}
		for _, mask := range feasibleMasks {
			if !installMatches(m, cands, mask, true) {
				t.Fatalf("seed %d: feasible subset %b rejected by installer", seed, mask)
			}
		}
		for _, mask := range infeasibleSamples(feasibleMasks, len(cands), rng) {
			if !installMatches(m, cands, mask, false) {
				t.Fatalf("seed %d: infeasible subset %b wrongly accepted", seed, mask)
			}
		}
	}
}

func randomManifest(rng *rand.Rand) *Manifest {
	const keyCount = 7
	key := func(i int) []byte { return []byte{byte('a' + i)} }

	var other, replaced []Record
	seq := uint64(1)
	// 其余层：每个键放一条较旧点值，制造“外层值穿透”压力。
	for i := 0; i < keyCount; i++ {
		val := []byte{byte('A' + i), byte(rng.Intn(256))}
		other = append(other, pv(seq, key(i), val))
		seq++
	}

	var cands []CandidatePage
	candID := uint32(1)
	tombRange := map[int]bool{} // 已被区间墓碑覆盖的点键下标，避免互相遮蔽。
	addCand := func(recs []Record, dicts ...uint32) {
		if len(cands) >= maxCandidates {
			return
		}
		c := mkCand(nil, candID, recs, dicts...)
		cands = append(cands, c)
		candID++
	}

	dicts := []DictSegment{mkDict(900, 3+rng.Intn(6)), mkDict(901, 2+rng.Intn(5))}

	// 被替换页：混合点值、点删除与区间墓碑。
	nRec := 3 + rng.Intn(4)
	for i := 0; i < nRec; i++ {
		r := Record{Seq: seq}
		switch rng.Intn(3) {
		case 0:
			r = pv(seq, key(rng.Intn(keyCount)), []byte{byte(rng.Intn(256))})
			replaced = append(replaced, r)
			// 有的点记录不给候选，制造无解空间。
			if rng.Intn(5) != 0 {
				ids := pickDicts(rng)
				addCand([]Record{r.Clone()}, ids...)
			}
		case 1:
			r = pd(seq, key(rng.Intn(keyCount)))
			replaced = append(replaced, r)
			if rng.Intn(5) != 0 {
				addCand([]Record{r.Clone()}, pickDicts(rng)...)
			}
		default:
			lo := rng.Intn(keyCount - 1)
			hi := lo + 1 + rng.Intn(keyCount-lo-1)
			if tombRange[lo] {
				// 该区域已有墓碑，改用点记录，避免多层墓碑遮蔽空洞。
				r = pd(seq, key(rng.Intn(keyCount)))
				replaced = append(replaced, r)
				if rng.Intn(5) != 0 {
					addCand([]Record{r.Clone()}, pickDicts(rng)...)
				}
				seq++
				continue
			}
			r = rd(seq, key(lo), key(hi))
			replaced = append(replaced, r)
			for k := lo; k < hi; k++ {
				tombRange[k] = true
			}
			if rng.Intn(4) == 0 {
				// 故意只给一个漏掉内部点键的碎片。该点键在其余层有
				// 更旧点值，且没有同序号其它墓碑遮蔽，缺失一定可观测，
				// 从而稳定产生空子句（无解）场景。
				mid := lo + 1
				if mid < hi {
					addCand([]Record{rd(seq, key(mid), key(hi))}, pickDicts(rng)...)
				} else {
					// 区间内没有可漏的内部点键：给一个左移的小碎片。
					addCand([]Record{rd(seq, key(lo), key(lo+1))}, pickDicts(rng)...)
				}
			} else {
				// 同序号相交/相接碎片，覆盖原区间。
				mid := lo + 1
				right := mid + 1
				if right > hi {
					right = hi
				}
				addCand([]Record{rd(seq, key(lo), key(right))}, pickDicts(rng)...)
				if mid < hi && len(cands) < maxCandidates {
					addCand([]Record{rd(seq, key(mid), key(hi))}, pickDicts(rng)...)
				}
			}
		}
		seq++
	}

	latest := seq - 1
	// 随机闭区间之并：1..latest 切 1-2 段。
	cut := 1 + rng.Intn(int(latest))
	var intervals []SeqInterval
	if rng.Intn(4) == 0 {
		// 单点保留区间：若存在不可满足观测，极小核恰好是单元素。
		pick := uint64(1 + rng.Intn(int(latest)))
		intervals = []SeqInterval{{Lo: pick, Hi: pick}}
	} else {
		intervals = []SeqInterval{{Lo: 1, Hi: uint64(cut)}}
		if cut+2 < int(latest) {
			intervals = append(intervals, SeqInterval{Lo: uint64(cut + 2), Hi: latest})
		}
	}
	return &Manifest{
		OtherLayers:     other,
		ReplacedPages:   []ReplacedPage{{ID: 50, Records: replaced}},
		Candidates:      cands,
		Dictionaries:    dicts,
		RetainIntervals: intervals,
		LatestSnapshot:  latest,
	}
}

func pickDicts(rng *rand.Rand) []uint32 {
	switch rng.Intn(3) {
	case 0:
		return nil
	case 1:
		return []uint32{900}
	default:
		return []uint32{900, 901}
	}
}

func bitsCount(mask uint32) int {
	n := 0
	for mask != 0 {
		n += int(mask & 1)
		mask >>= 1
	}
	return n
}

func itoa64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func subsetCost(m *Manifest, cands []CandidatePage, mask uint32) int {
	total := 0
	used := map[uint32]struct{}{}
	for i := range cands {
		if mask&(1<<i) == 0 {
			continue
		}
		total += len(cands[i].Encoding)
		for _, d := range cands[i].DictIDs {
			used[d] = struct{}{}
		}
	}
	for id := range used {
		for _, d := range m.Dictionaries {
			if d.ID == id {
				total += len(d.Data)
			}
		}
	}
	return total
}

func betterRef(mask uint32, cost, count int, bestMask uint32, bestCost, bestCount int) bool {
	if cost != bestCost {
		return cost < bestCost
	}
	if count != bestCount {
		return count < bestCount
	}
	diff := mask ^ bestMask
	low := diff & (^diff + 1)
	return mask&low == 0
}

func infeasibleSamples(feasible []uint32, n int, rng *rand.Rand) []uint32 {
	ok := map[uint32]bool{}
	for _, m := range feasible {
		ok[m] = true
	}
	var out []uint32
	for tries := 0; tries < 8 && tries < (1<<n); tries++ {
		mask := uint32(rng.Intn(1 << n))
		if !ok[mask] {
			out = append(out, mask)
		}
	}
	return out
}

func installMatches(m *Manifest, cands []CandidatePage, mask uint32, wantOK bool) bool {
	var ids []uint32
	for i := range cands {
		if mask&(1<<i) != 0 {
			ids = append(ids, cands[i].ID)
		}
	}
	rep, err := Install(m, ids)
	if err != nil {
		return false
	}
	return rep.Verified() == wantOK
}

func checkMinimalCore(t *testing.T, m *Manifest, cands []CandidatePage, store []taggedRecord, obs []Observation, nse *NoSolutionError) {
	t.Helper()
	// 正单调子句系统的不可满足核由全部空子句观测组成；逐一删除后，
	// 其余观测（均为非空子句）必须可被全集候选页同时满足。
	var zeroObs []Observation
	for _, o := range obs {
		win := winner(store, o.Key, o.Snapshot)
		if win == nil || !win.fromReplaced {
			continue
		}
		base := make([]taggedRecord, 0, len(m.OtherLayers))
		for _, r := range m.OtherLayers {
			base = append(base, taggedRecord{Record: r.Clone()})
		}
		canFix := false
		for ci := range cands {
			recs := append(append([]taggedRecord(nil), base...), taggedRecords(cands[ci].Records)...)
			if readAt(recs, o.Key, o.Snapshot).equal(o.Correct) {
				canFix = true
				break
			}
		}
		if !canFix {
			zeroObs = append(zeroObs, o)
		}
	}
	// Solve 返回按（键，快照）序的首个空子句观测；它本身就是一个
	// 包含关系极小的不可满足核。
	if len(nse.Observations) == 0 {
		t.Fatal("NoSolutionError carries no observations")
	}
	zeroSet := map[string]bool{}
	for _, o := range zeroObs {
		zeroSet[string(o.Key)+"/"+itoa64(o.Snapshot)] = true
	}
	for _, got := range nse.Observations {
		if !zeroSet[string(got.Key)+"/"+itoa64(got.Snapshot)] {
			t.Fatalf("returned observation %+v is not an empty-clause observation", got)
		}
	}
	first := zeroObs[0]
	if !bytes.Equal(nse.Observations[0].Key, first.Key) || nse.Observations[0].Snapshot != first.Snapshot {
		t.Fatalf("first core %+v differs from first empty-clause %+v", nse.Observations[0], first)
	}
	for _, core := range nse.Observations {
		// 极小性只对单元素核可直接验证；多空约束场景下，删除其中一个
		// 仍有其它空约束，属于另一个独立的不可满足核。
		if len(zeroObs) > 1 {
			continue
		}
		rest := make([]Observation, 0, len(obs))
		for _, o := range obs {
			if bytes.Equal(o.Key, core.Key) && o.Snapshot == core.Snapshot {
				continue
			}
			rest = append(rest, o)
		}
		found := false
		for mask := uint32(0); mask < 1<<len(cands); mask++ {
			if simulateSubset(m, cands, mask, store, rest) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("core observation %+v is not inclusion-minimal", core)
		}
	}
}
