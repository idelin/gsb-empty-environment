package compaction

import "testing"

// 内部空洞：同序号两个碎片在区间内部留下一个未覆盖点键。更旧的外层
// 点值会从空洞中穿透，必须通过完整范围扫描捕获，而不是仅证明每个键
// “存在某个正确版本”。
func TestInternalHole(t *testing.T) {
	other := []Record{
		pv(1, []byte("a"), []byte("A")),
		pv(2, []byte("b"), []byte("B")),
		pv(3, []byte("c"), []byte("C")),
		pv(4, []byte("d"), []byte("D")),
	}
	tomb := rd(8, []byte("a"), []byte("e")) // 覆盖 a、b、c、d
	m := &Manifest{
		OtherLayers:   other,
		ReplacedPages: []ReplacedPage{{ID: 1, Records: []Record{tomb}}},
		Candidates: []CandidatePage{
			mkCand(t, 10, []Record{rd(8, []byte("a"), []byte("c"))}), // 覆盖 a、b
			mkCand(t, 11, []Record{rd(8, []byte("d"), []byte("e"))}), // 覆盖 d，漏掉 c
		},
		RetainIntervals: []SeqInterval{{1, 8}},
		LatestSnapshot:  8,
	}

	// 没有任何候选子集能在快照 8 上让 c 仍读出墓碑 null。
	_, err := Solve(m)
	if err == nil {
		t.Fatal("expected no-solution for internal hole")
	}

	// 两页都安装后，c 仍复活成旧外层值；安装器报告的首个反例就是 c@8。
	rep, ierr := Install(m, []uint32{10, 11})
	if ierr != nil {
		t.Fatal(ierr)
	}
	if rep.Verified() {
		t.Fatal("installer must detect the internal hole")
	}
	ce := rep.Counterexample
	if string(ce.Key) != "c" || ce.Snapshot != 8 {
		t.Fatalf("counterexample = key %s snap %d, want c@8", ce.Key, ce.Snapshot)
	}
	if ce.Expected.Kind != KindRangeDelete || ce.Got.Kind != KindPointValue || string(ce.Got.Value) != "C" {
		t.Fatalf("counterexample contents wrong: want null, got %+v", ce.Got)
	}

	// 完整覆盖原区间的碎片组合应通过安装校验。
	good := &Manifest{
		OtherLayers:   other,
		ReplacedPages: []ReplacedPage{{ID: 1, Records: []Record{tomb}}},
		Candidates: []CandidatePage{
			mkCand(t, 10, []Record{rd(8, []byte("a"), []byte("d"))}),
			mkCand(t, 11, []Record{rd(8, []byte("c"), []byte("e"))}), // 与上一片重叠
		},
		RetainIntervals: []SeqInterval{{1, 8}},
		LatestSnapshot:  8,
	}
	sol := mustSolve(t, good)
	if len(sol.SelectedIDs) != 2 {
		t.Fatalf("both covering fragments needed, got %v", sol.SelectedIDs)
	}
	okRep, err := Install(good, sol.SelectedIDs)
	if err != nil || !okRep.Verified() {
		t.Fatalf("overlapping covering fragments must verify: %v %+v", err, okRep)
	}
}
