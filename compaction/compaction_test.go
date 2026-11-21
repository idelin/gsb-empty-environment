package compaction

import (
	"bytes"
	"testing"
)

// ---------- 构造辅助 ----------

func pv(seq uint64, key, val []byte) Record {
	v := make([]byte, len(val))
	copy(v, val)
	return Record{Seq: seq, Kind: KindPointValue, Key: append([]byte(nil), key...), Value: v}
}

func pd(seq uint64, key []byte) Record {
	return Record{Seq: seq, Kind: KindPointDelete, Key: append([]byte(nil), key...)}
}

func rd(seq uint64, start, end []byte) Record {
	return Record{Seq: seq, Kind: KindRangeDelete, Key: append([]byte(nil), start...), End: append([]byte(nil), end...)}
}

func mkCand(t *testing.T, id uint32, recs []Record, dictIDs ...uint32) CandidatePage {
	if t != nil {
		t.Helper()
	}
	c := CandidatePage{ID: id, Records: recs, DictIDs: dictIDs}
	c.Encoding = EncodePage(c)
	return c
}

func mkDict(id uint32, n int) DictSegment {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(id)
	}
	d := DictSegment{ID: id, Data: data}
	d.Data = EncodeSegment(d)
	return d
}

func mustSolve(t *testing.T, m *Manifest) *Solution {
	t.Helper()
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return sol
}

// ---------- 题目指定场景 ----------

// 非 UTF-8 键：含 0x00、0xff 等字节，按字节序排序与比较必须正确。
func TestNonUTF8Keys(t *testing.T) {
	keys := [][]byte{{0x00, 0xff}, {0xff}, {0x80}, {0x00}, {0x41}}
	other := []Record{
		pv(1, keys[0], []byte("old")),
		pv(2, keys[1], []byte("x")),
	}
	rep := []Record{
		pv(3, keys[0], []byte{0xde, 0xad}),
		pv(4, keys[2], []byte("mid")),
		pv(5, keys[3], []byte("zero")),
		pv(6, keys[4], []byte("A")),
	}
	m := &Manifest{
		OtherLayers:     other,
		ReplacedPages:   []ReplacedPage{{ID: 1, Records: rep}},
		Candidates:      []CandidatePage{mkCand(t, 7, rep)},
		RetainIntervals: []SeqInterval{{1, 6}},
		LatestSnapshot:  6,
	}
	sol := mustSolve(t, m)
	if len(sol.SelectedIDs) != 1 || sol.SelectedIDs[0] != 7 {
		t.Fatalf("unexpected selection: %+v", sol)
	}
	rep2, err := Install(m, sol.SelectedIDs)
	if err != nil || !rep2.Verified() {
		t.Fatalf("install verified=%v err=%v ce=%+v", rep2 != nil && rep2.Verified(), err, rep2)
	}

	gotOrder := keyUniverse(m)
	want := [][]byte{{0x00}, {0x00, 0xff}, {0x41}, {0x80}, {0xff}}
	if len(gotOrder) != len(want) {
		t.Fatalf("key count")
	}
	for i := range want {
		if !bytes.Equal(gotOrder[i], want[i]) {
			t.Fatalf("key order mismatch at %d: got %v want %v", i, gotOrder[i], want[i])
		}
	}
}

// 同序号相交碎片：两个碎片区间重叠，联合覆盖原墓碑；缺一个就留下内部空洞。
func TestOverlappingFragmentsSameSeq(t *testing.T) {
	other := []Record{
		pv(1, []byte("k1"), []byte("outer1")),
		pv(2, []byte("k2"), []byte("outer2")),
		pv(3, []byte("k3"), []byte("outer3")),
	}
	tomb := rd(9, []byte("k1"), []byte("k4"))
	m := &Manifest{
		OtherLayers:   other,
		ReplacedPages: []ReplacedPage{{ID: 1, Records: []Record{tomb}}},
		Candidates: []CandidatePage{
			mkCand(t, 10, []Record{rd(9, []byte("k1"), []byte("k3"))}),
			mkCand(t, 11, []Record{rd(9, []byte("k2"), []byte("k4"))}),
		},
		RetainIntervals: []SeqInterval{{1, 9}},
		LatestSnapshot:  9,
	}
	sol := mustSolve(t, m)
	if len(sol.SelectedIDs) != 2 {
		t.Fatalf("both fragments required, got %v", sol.SelectedIDs)
	}

	rep, err := Install(m, []uint32{10})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verified() {
		t.Fatal("expected counterexample for internal hole")
	}
	ce := rep.Counterexample
	if !bytes.Equal(ce.Key, []byte("k3")) || ce.Snapshot != 9 || ce.Got.Kind != KindPointValue {
		t.Fatalf("wrong counterexample: %+v", ce)
	}
}

// 多个共享字典：同一字典被多页引用只计费一次。
func TestMultipleSharedDictionaries(t *testing.T) {
	rep := []Record{pv(5, []byte("a"), []byte("v1")), pv(6, []byte("b"), []byte("v2"))}
	d1 := mkDict(100, 100)
	d2 := mkDict(101, 30)
	c1 := mkCand(t, 1, []Record{rep[0]}, 100, 101)
	c2 := mkCand(t, 2, []Record{rep[1]}, 100)
	m := &Manifest{
		ReplacedPages:   []ReplacedPage{{ID: 9, Records: rep}},
		Candidates:      []CandidatePage{c1, c2},
		Dictionaries:    []DictSegment{d1, d2},
		RetainIntervals: []SeqInterval{{1, 6}},
		LatestSnapshot:  6,
	}
	sol := mustSolve(t, m)
	want := len(c1.Encoding) + len(c2.Encoding) + len(d1.Data) + len(d2.Data)
	if sol.TotalBytes != want {
		t.Fatalf("billing: got %d want %d", sol.TotalBytes, want)
	}
	rep1, err := Install(m, []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	if rep1.TotalBytes != len(c1.Encoding)+len(d1.Data)+len(d2.Data) {
		t.Fatalf("single page billing got %d", rep1.TotalBytes)
	}
}

// 墓碑恰在下界生效：snapshot == seq 可见，再小一号不可见；上界为开区间。
func TestTombstoneLowerBound(t *testing.T) {
	other := []Record{pv(1, []byte("a"), []byte("v")), pv(2, []byte("c"), []byte("w"))}
	tomb := rd(5, []byte("a"), []byte("c")) // 覆盖 a、b，不覆盖 c
	m := &Manifest{
		OtherLayers:     other,
		ReplacedPages:   []ReplacedPage{{ID: 1, Records: []Record{tomb}}},
		Candidates:      []CandidatePage{mkCand(t, 3, []Record{tomb})},
		RetainIntervals: []SeqInterval{{1, 5}},
		LatestSnapshot:  5,
	}
	store := baselineStore(m)
	if got := readAt(store, []byte("a"), 4); got.Kind != KindPointValue {
		t.Fatalf("snapshot below lower bound must still see old value, got %+v", got)
	}
	if got := readAt(store, []byte("a"), 5); got.Kind != KindRangeDelete {
		t.Fatalf("tombstone must take effect exactly at its sequence, got %+v", got)
	}
	if got := readAt(store, []byte("b"), 5); got.Kind != KindRangeDelete {
		t.Fatalf("interior key must be deleted, got %+v", got)
	}
	if got := readAt(store, []byte("c"), 5); got.Kind != KindPointValue {
		t.Fatalf("open upper bound key must keep value, got %+v", got)
	}
	sol := mustSolve(t, m)
	rep, err := Install(m, sol.SelectedIDs)
	if err != nil || !rep.Verified() {
		t.Fatalf("install: %v %+v", err, rep)
	}
}
