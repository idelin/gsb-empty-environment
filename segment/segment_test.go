package segment

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 构建验收场景：'database' 短链 20 条，'storage' 长链 10000 条，
// 交集为 docID 0, 500, 1000, ..., 9500。
func buildSkewedSegment(t *testing.T) (*Segment, []uint32) {
	t.Helper()
	b := NewBuilder()
	for i := uint32(0); i < 10000; i++ {
		b.Add(i, "storage engine benchmark")
	}
	var want []uint32
	for i := uint32(0); i < 10000; i += 500 {
		b.Add(i, "database storage engine")
		want = append(want, i)
	}
	seg, err := Open(b.Build())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return seg, want
}

// 验收：构建倒排索引并检索 'database AND storage'。
func TestIntersectDatabaseAndStorage(t *testing.T) {
	seg, want := buildSkewedSegment(t)

	db := seg.Postings("database")
	st := seg.Postings("storage")
	if db == nil || st == nil {
		t.Fatal("postings not found")
	}
	if db.Len() != 20 || st.Len() != 10000 {
		t.Fatalf("list lengths: database=%d storage=%d, want 20 and 10000", db.Len(), st.Len())
	}

	got := Intersect(db, st)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intersection mismatch:\n got=%v\nwant=%v", got, want)
	}
	t.Logf("intersection (%d docs): %v...", len(got), got[:5])
}

// 验收：两链长度悬殊时，短链驱动 + 跳跃指针使长链只解码极少量 posting。
func TestSkipPointersAccelerateIntersection(t *testing.T) {
	seg, want := buildSkewedSegment(t)

	db := seg.Postings("database")
	st := seg.Postings("storage")
	got := Intersect(db, st)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intersection mismatch: got %d docs, want %d", len(got), len(want))
	}

	// 朴素归并需解码全部 10000 条长链 posting；
	// 跳跃指针下每次 Advance 最多解码 skipInterval 条，20 次跳跃上界约 20*8+1。
	if st.SkipsUsed == 0 {
		t.Fatal("expected skip pointers to be used on the long list")
	}
	if limit := 20*skipInterval + skipInterval; st.Decoded > limit {
		t.Fatalf("long list decoded %d postings, want <= %d (naive merge: %d)",
			st.Decoded, limit, st.Len())
	}
	if st.Decoded > st.Len()/10 {
		t.Fatalf("skip pointers ineffective: decoded %d of %d", st.Decoded, st.Len())
	}
	t.Logf("long list: decoded %d/%d postings, skips used %d",
		st.Decoded, st.Len(), st.SkipsUsed)
	t.Logf("short list: decoded %d/%d postings", db.Decoded, db.Len())
}

// 词典：前缀压缩不影响查找正确性，缺失词返回 nil。
func TestDictionaryLookup(t *testing.T) {
	b := NewBuilder()
	b.Add(1, "apple apply application apt banana band")
	b.Add(2, "apple banana")
	seg, err := Open(b.Build())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if seg.TermCount() != 6 {
		t.Fatalf("TermCount=%d, want 6", seg.TermCount())
	}
	for _, term := range []string{"apple", "application", "apply", "apt", "banana", "band"} {
		if !seg.Has(term) {
			t.Errorf("term %q should exist", term)
		}
	}
	for _, term := range []string{"", "app", "apples", "bandana", "zebra"} {
		if seg.Has(term) {
			t.Errorf("term %q should not exist", term)
		}
		if it := seg.Postings(term); it != nil {
			t.Errorf("Postings(%q) should be nil", term)
		}
	}
}

// 倒排列表内容：docID 有序去重，词频正确。
func TestPostingsContentAndTF(t *testing.T) {
	b := NewBuilder()
	b.Add(7, "storage storage storage engine")
	b.Add(3, "storage engine")
	b.Add(9, "storage")
	seg, err := Open(b.Build())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	it := seg.Postings("storage")
	if it.Len() != 3 {
		t.Fatalf("Len=%d, want 3", it.Len())
	}
	type posting struct{ doc, tf uint32 }
	var got []posting
	for it.Next() {
		got = append(got, posting{it.Doc(), it.TF()})
	}
	want := []posting{{3, 1}, {7, 3}, {9, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Advance 语义：随机倒排链上，Advance(target) 必须停在第一个 >= target 的文档。
func TestAdvanceMatchesLinearScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(500)
		ids := make([]uint32, 0, n)
		cur := uint32(rng.Intn(3))
		for i := 0; i < n; i++ {
			cur += uint32(1 + rng.Intn(10))
			ids = append(ids, cur)
		}
		b := NewBuilder()
		for _, id := range ids {
			b.Add(id, "x")
		}
		seg, err := Open(b.Build())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		it := seg.Postings("x")
		// 目标递增的 Advance 序列与线性扫描对照。
		scan := 0
		for step := 0; step < 30; step++ {
			target := ids[0] + uint32(rng.Intn(int(ids[len(ids)-1]-ids[0])+20))
			for scan < len(ids) && ids[scan] < target {
				scan++
			}
			ok := it.Advance(target)
			if scan == len(ids) {
				if ok {
					t.Fatalf("trial %d: Advance(%d)=%v, want exhausted", trial, target, ok)
				}
				break
			}
			if !ok || it.Doc() != ids[scan] {
				t.Fatalf("trial %d: Advance(%d) got (%v,%d), want doc %d",
					trial, target, ok, it.Doc(), ids[scan])
			}
		}
	}
}

// 交集边界：缺失词、空交集、自身交集。
func TestIntersectEdgeCases(t *testing.T) {
	b := NewBuilder()
	for i := uint32(0); i < 100; i += 2 {
		b.Add(i, "even")
	}
	for i := uint32(1); i < 100; i += 2 {
		b.Add(i, "odd")
	}
	b.Add(2, "even both")
	b.Add(3, "odd both")
	seg, err := Open(b.Build())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got := Intersect(seg.Postings("even"), seg.Postings("missing")); got != nil {
		t.Fatalf("missing term: got %v, want nil", got)
	}
	if got := Intersect(seg.Postings("even"), seg.Postings("odd")); len(got) != 0 {
		t.Fatalf("disjoint lists: got %v, want empty", got)
	}
	if got := Intersect(seg.Postings("even"), seg.Postings("even")); len(got) != 50 {
		t.Fatalf("self intersect: got %d docs, want 50", len(got))
	}
	if got := Intersect(seg.Postings("even"), seg.Postings("both")); !reflect.DeepEqual(got, []uint32{2}) {
		t.Fatalf("even AND both: got %v, want [2]", got)
	}
}

// 段应明显小于未压缩的 (docID,tf) 定长编码。
func TestSegmentIsCompact(t *testing.T) {
	seg, _ := buildSkewedSegment(t)
	// 总 posting 数：storage/engine/benchmark 各 10000，叠加 20 篇文档各 +3。
	raw := 30060 * 8 // 每条 posting 未压缩为 docID+tf 两个 uint32
	if got := len(seg.postings); got > raw/2 {
		t.Fatalf("postings region %d bytes, want < %d (delta+varint compression)", got, raw/2)
	}
	t.Logf("postings region: %d bytes vs %d raw", len(seg.postings), raw)
}

func ExampleIntersect() {
	b := NewBuilder()
	for i := uint32(0); i < 10000; i++ {
		b.Add(i, "storage engine")
	}
	for i := uint32(0); i < 10000; i += 500 {
		b.Add(i, "database storage")
	}
	seg, _ := Open(b.Build())
	hits := Intersect(seg.Postings("database"), seg.Postings("storage"))
	fmt.Println(len(hits), hits[:3])
	// Output: 20 [0 500 1000]
}
