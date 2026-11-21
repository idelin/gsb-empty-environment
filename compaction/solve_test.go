package compaction

import (
	"bytes"
	"errors"
	"sort"
	"testing"
)

// 内部空洞导致无解：两个碎片都漏掉 hole，更旧外层值会穿透。
func TestNoSolutionMinimalObservations(t *testing.T) {
	other := []Record{
		pv(1, []byte("n"), []byte("outer")),
		pv(2, []byte("kept"), []byte("ok")),
	}
	tomb := rd(3, []byte("a"), []byte("z"))
	m := &Manifest{
		OtherLayers: other,
		ReplacedPages: []ReplacedPage{{
			ID:      1,
			Records: []Record{tomb, pv(4, []byte("kept"), []byte("new"))},
		}},
		Candidates: []CandidatePage{
			mkCand(t, 1, []Record{rd(3, []byte("a"), []byte("m"))}),
			mkCand(t, 2, []Record{rd(3, []byte("o"), []byte("z"))}),
			mkCand(t, 3, []Record{pv(4, []byte("kept"), []byte("new"))}),
		},
		// 单点闭区间：墓碑只在快照 4 上产生一条不可满足观测，保证极小核唯一。
		RetainIntervals: []SeqInterval{{4, 4}},
		LatestSnapshot:  4,
	}
	_, err := Solve(m)
	var nse *NoSolutionError
	if !errors.As(err, &nse) {
		t.Fatalf("want NoSolutionError, got %v", err)
	}
	if len(nse.Observations) != 1 {
		t.Fatalf("minimal core should be singleton, got %d", len(nse.Observations))
	}
	ob := nse.Observations[0]
	if !bytes.Equal(ob.Key, []byte("n")) || ob.Correct.Kind != KindRangeDelete {
		t.Fatalf("unexpected core observation: %+v", ob)
	}

	// 极小性：删掉该观测后，存在合法页集满足其余全部观测。
	cands := m.sortedCandidates()
	store := baselineStore(m)
	allObs := observations(m, store, buildSnapshots(m))
	rest := make([]Observation, 0, len(allObs)-1)
	for _, o := range allObs {
		if bytes.Equal(o.Key, ob.Key) && o.Snapshot == ob.Snapshot {
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
		t.Fatal("core is not inclusion-minimal: no subset satisfies the remaining observations")
	}
}

// 空值（空字节串点值）、墓碑 null、缺失三种状态互相不同。
func TestEmptyValueVersusMissing(t *testing.T) {
	m := &Manifest{
		OtherLayers:   []Record{pv(4, []byte("e"), []byte{})},
		ReplacedPages: []ReplacedPage{{ID: 1, Records: []Record{pd(7, []byte("d"))}}},
		Candidates:    []CandidatePage{mkCand(t, 2, []Record{pd(7, []byte("d"))})},
		RetainIntervals: []SeqInterval{
			{1, 4},
			{7, 7},
		},
		LatestSnapshot: 7,
	}
	store := baselineStore(m)
	if got := readAt(store, []byte("e"), 7); got.Kind != KindPointValue || len(got.Value) != 0 || got.Value == nil {
		t.Fatalf("empty value must be non-nil empty slice, got %+v", got)
	}
	if got := readAt(store, []byte("d"), 7); got.Kind != KindPointDelete {
		t.Fatalf("point delete expected, got %+v", got)
	}
	if got := readAt(store, []byte("missing"), 7); got.Kind != KindMissing {
		t.Fatalf("missing expected, got %+v", got)
	}
	snaps := buildSnapshots(m)
	if !sort.SliceIsSorted(snaps, func(i, j int) bool { return snaps[i] < snaps[j] }) {
		t.Fatal("snapshots not sorted")
	}
	if snaps[0] != 1 || snaps[len(snaps)-1] != 7 {
		t.Fatalf("snapshot set %v", snaps)
	}
	sol := mustSolve(t, m)
	rep, err := Install(m, sol.SelectedIDs)
	if err != nil || !rep.Verified() {
		t.Fatalf("install: %v %+v", err, rep)
	}
}

func TestTieBreakRules(t *testing.T) {
	rep := []Record{pv(5, []byte("a"), []byte("v")), pv(6, []byte("b"), []byte("w"))}
	base := func(cands []CandidatePage, dicts []DictSegment) *Manifest {
		return &Manifest{
			ReplacedPages:   []ReplacedPage{{ID: 1, Records: rep}},
			Candidates:      cands,
			Dictionaries:    dicts,
			RetainIntervals: []SeqInterval{{1, 6}},
			LatestSnapshot:  6,
		}
	}

	// 字节相同的等价单页：页编号向量小者胜。
	m := base([]CandidatePage{
		mkCand(t, 8, rep),
		mkCand(t, 3, rep),
	}, nil)
	sol := mustSolve(t, m)
	if len(sol.SelectedIDs) != 1 || sol.SelectedIDs[0] != 3 {
		t.Fatalf("lex tie break failed: %v", sol.SelectedIDs)
	}

	// 单页方案与两页方案都可行且总字节相同：页数少者胜。单页携带
	// 两个点记录并引用一个大字典；两页各带一个点记录、共享小字典。
	d50 := mkDict(50, 5)
	pageA := mkCand(t, 20, rep, 51)
	pageB1 := mkCand(t, 10, []Record{rep[0]}, 50)
	pageB2 := mkCand(t, 11, []Record{rep[1]}, 50)
	// d51 的物理长度 = B1+B2 的页字节 - A 的页字节 + d50 的物理长度，
	// 这样 cost(A)=cost(B1∪B2)。
	raw := len(pageB1.Encoding) + len(pageB2.Encoding) - len(pageA.Encoding) + len(d50.Data) - 12
	if raw <= 0 {
		t.Fatalf("test setup: non-positive payload %d", raw)
	}
	d51 := mkDict(51, raw)
	m = base([]CandidatePage{pageA, pageB1, pageB2}, []DictSegment{d50, d51})
	sol = mustSolve(t, m)
	if len(sol.SelectedIDs) != 1 {
		t.Fatalf("fewer pages must win equal bytes: %v", sol.SelectedIDs)
	}
	if sol.SelectedIDs[0] != 20 {
		t.Fatalf("equal-cost single page selected wrong id: %v", sol.SelectedIDs)
	}
	costTwo := subsetCost(m, m.sortedCandidates(), 0b011) // 候选按 ID 排序：10,11,20
	if sol.TotalBytes != costTwo {
		t.Fatalf("costs not actually tied: %d vs %d", sol.TotalBytes, costTwo)
	}
}

func TestValidateRejectsIllegalCandidates(t *testing.T) {
	rep := []ReplacedPage{{ID: 1, Records: []Record{
		pv(5, []byte("a"), []byte("v")),
		rd(6, []byte("a"), []byte("z")),
	}}}
	cases := []struct {
		name string
		cand CandidatePage
	}{
		{"new seq", mkCand(t, 1, []Record{pv(99, []byte("a"), []byte("v"))})},
		{"changed value", mkCand(t, 1, []Record{pv(5, []byte("a"), []byte("other"))})},
		{"changed key", mkCand(t, 1, []Record{pv(5, []byte("b"), []byte("v"))})},
		{"kind flip", mkCand(t, 1, []Record{pd(5, []byte("a"))})},
		{"fragment grows lower", mkCand(t, 1, []Record{rd(6, []byte(" "), []byte("m"))})},
		{"fragment grows upper", mkCand(t, 1, []Record{rd(6, []byte("m"), []byte("zz"))})},
		{"fragment inverted", mkCand(t, 1, []Record{rd(6, []byte("z"), []byte("a"))})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				ReplacedPages:   rep,
				Candidates:      []CandidatePage{tc.cand},
				RetainIntervals: []SeqInterval{{1, 6}},
				LatestSnapshot:  6,
			}
			if err := m.Validate(); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("want ErrInvalidManifest, got %v", err)
			}
		})
	}

	// 合法截短（下界右移/上界左移/重叠碎片）应通过。
	m := &Manifest{
		ReplacedPages: rep,
		Candidates: []CandidatePage{
			mkCand(t, 1, []Record{
				pv(5, []byte("a"), []byte("v")),
				rd(6, []byte("a"), []byte("m")),
				rd(6, []byte("l"), []byte("z")),
			}),
		},
		RetainIntervals: []SeqInterval{{1, 6}},
		LatestSnapshot:  6,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("legal truncation rejected: %v", err)
	}

	// 超过 22 个候选页直接报错，不做序号/键空间枚举。
	many := make([]CandidatePage, maxCandidates+1)
	for i := range many {
		many[i] = mkCand(t, uint32(i+1), []Record{pv(5, []byte("a"), []byte("v"))})
	}
	over := &Manifest{
		ReplacedPages:   rep,
		Candidates:      many,
		RetainIntervals: []SeqInterval{{1, 6}},
		LatestSnapshot:  6,
	}
	if err := over.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("want limit error, got %v", err)
	}
}

// 安装器对坏编码、悬空字典、重复入选与反例顺序的处理。
func TestInstallerGuards(t *testing.T) {
	rep := []Record{pv(5, []byte("a"), []byte("v"))}
	c := mkCand(t, 1, rep)
	m := &Manifest{
		ReplacedPages:   []ReplacedPage{{ID: 1, Records: rep}},
		Candidates:      []CandidatePage{c},
		RetainIntervals: []SeqInterval{{1, 5}},
		LatestSnapshot:  5,
	}
	if _, err := Install(m, []uint32{99}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unknown selected id: %v", err)
	}
	if _, err := Install(m, []uint32{1, 1}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("duplicate selection: %v", err)
	}

	bad := c
	bad.Encoding = append(append([]byte(nil), c.Encoding...), 0xff)
	badM := &Manifest{
		ReplacedPages:   []ReplacedPage{{ID: 1, Records: rep}},
		Candidates:      []CandidatePage{bad},
		RetainIntervals: []SeqInterval{{1, 5}},
		LatestSnapshot:  5,
	}
	if _, err := Install(badM, []uint32{1}); !errors.Is(err, ErrCorruptEncoding) {
		t.Fatalf("corrupt encoding: %v", err)
	}

	// 反例顺序：键字节序优先于快照序号。
	other := []Record{pv(1, []byte("a"), []byte("oa")), pv(2, []byte("b"), []byte("ob"))}
	tomb := rd(5, []byte("a"), []byte("z"))
	hole := &Manifest{
		OtherLayers:     other,
		ReplacedPages:   []ReplacedPage{{ID: 1, Records: []Record{tomb}}},
		Candidates:      []CandidatePage{mkCand(t, 2, []Record{rd(5, []byte("a"), []byte("aa"))})},
		RetainIntervals: []SeqInterval{{1, 5}},
		LatestSnapshot:  5,
	}
	r2, err := Install(hole, []uint32{2})
	if err != nil || r2.Verified() {
		t.Fatalf("want counterexample report, err=%v", err)
	}
	if !bytes.Equal(r2.Counterexample.Key, []byte("b")) {
		t.Fatalf("first counterexample must be smallest failing key, got %s", r2.Counterexample.Key)
	}
}

// 编码往返：nil 与空切片值必须区分。
func TestEncodeRoundTrip(t *testing.T) {
	c := CandidatePage{
		ID: 7,
		Records: []Record{
			pv(1, []byte("k"), []byte{}),
			pd(2, []byte{0x00, 0xff}),
			rd(3, []byte("a"), []byte("z")),
		},
		DictIDs: []uint32{11, 12},
	}
	c.Encoding = EncodePage(c)
	back, err := DecodePage(c.Encoding)
	if err != nil {
		t.Fatal(err)
	}
	if !recordsEqual(back.Records, c.Records) || !u32Equal(back.DictIDs, c.DictIDs) || back.ID != c.ID {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	if back.Records[0].Value == nil {
		t.Fatal("empty point value must not decode to nil")
	}

	d := mkDict(3, 7)
	seg, err := DecodeSegment(EncodeSegment(d))
	if err != nil || seg.ID != 3 || !bytes.Equal(seg.Data, d.Data) {
		t.Fatalf("segment round trip: %+v err=%v", seg, err)
	}
}
