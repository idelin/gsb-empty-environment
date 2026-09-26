package pct

import (
	"math/rand/v2"
	"runtime"
	"sort"
	"testing"
	"unsafe"
)

// validate recursively checks AVL structure: BST ordering, exact subtree
// size/height, and the balance invariant at every node.
func validate(t *testing.T, n *node, lo, hi int64) (int32, int32) {
	t.Helper()
	if n == nil {
		return 0, 0
	}
	if n.key <= lo || n.key >= hi {
		t.Fatalf("BST violation: key %d not in (%d,%d)", n.key, lo, hi)
	}
	ls, lh := validate(t, n.left, lo, n.key)
	rs, rh := validate(t, n.right, n.key, hi)
	if n.size != 1+ls+rs {
		t.Fatalf("size mismatch at key %d: stored %d, actual %d", n.key, n.size, 1+ls+rs)
	}
	wantH := 1 + max(lh, rh)
	if n.height != wantH {
		t.Fatalf("height mismatch at key %d: stored %d, actual %d", n.key, n.height, wantH)
	}
	if diff := lh - rh; diff < -1 || diff > 1 {
		t.Fatalf("AVL violation at key %d: left h=%d right h=%d", n.key, lh, rh)
	}
	return n.size, n.height
}

func checkTree(t *testing.T, tr Tree) {
	t.Helper()
	validate(t, tr.root, -1<<63, 1<<63-1)
}

func collect(tr Tree) []KV {
	var out []KV
	for it := tr.Iterate(); it.Next(); {
		out = append(out, KV{it.Key(), it.Value()})
	}
	return out
}

func TestMemoryLayout(t *testing.T) {
	if NodeBytes != 40 {
		t.Fatalf("node size = %d, want 40", NodeBytes)
	}
	var n node
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"key", unsafe.Offsetof(n.key), 0},
		{"val", unsafe.Offsetof(n.val), 8},
		{"height", unsafe.Offsetof(n.height), 16},
		{"size", unsafe.Offsetof(n.size), 20},
		{"left", unsafe.Offsetof(n.left), 24},
		{"right", unsafe.Offsetof(n.right), 32},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Fatalf("offset(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestPutGetAndPersistence(t *testing.T) {
	var v0 Tree
	v1 := v0.Put(5, 50)
	v2 := v1.Put(3, 30).Put(8, 80)
	v3 := v2.Put(5, 55) // overwrite existing key

	if v0.Len() != 0 || v1.Len() != 1 || v2.Len() != 3 || v3.Len() != 3 {
		t.Fatalf("lens: %d %d %d %d", v0.Len(), v1.Len(), v2.Len(), v3.Len())
	}
	// Old roots are stable: v2 still sees the old value for key 5.
	if got, _ := v2.Get(5); got != 50 {
		t.Fatalf("v2.Get(5) = %d, want 50 (old version must not change)", got)
	}
	if got, _ := v3.Get(5); got != 55 {
		t.Fatalf("v3.Get(5) = %d, want 55", got)
	}
	if _, ok := v1.Get(8); ok {
		t.Fatal("v1 must not see key 8 added in v2")
	}
	checkTree(t, v3)
}

func TestDeleteAbsentKeepsDigest(t *testing.T) {
	var tr Tree
	tr = tr.BatchPut([]KV{{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50}})
	before := tr.Digest()
	after := tr.Delete(999).Delete(-7)
	if after.Digest() != before {
		t.Fatalf("digest changed after deleting absent keys: %x -> %x", before, after.Digest())
	}
	if SharedNodes(tr, after) != tr.Len() {
		t.Fatal("deleting absent keys must return the identical root (full sharing)")
	}
	// Deleting a present key must change the digest.
	if tr.Delete(3).Digest() == before {
		t.Fatal("digest unchanged after deleting a present key")
	}
}

func TestRotationSubtreeCounts(t *testing.T) {
	// Each sequence forces a different rotation pattern.
	cases := map[string][]int64{
		"LL": {30, 20, 10},
		"RR": {10, 20, 30},
		"LR": {30, 10, 20},
		"RL": {10, 30, 20},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			var tr Tree
			for _, k := range keys {
				tr = tr.Put(k, k*10)
				checkTree(t, tr) // size/height exact after every rotation
			}
			if tr.Len() != 3 {
				t.Fatalf("len = %d", tr.Len())
			}
			for i, k := range []int64{10, 20, 30} {
				got, ok := tr.Select(i)
				if !ok || got != k {
					t.Fatalf("Select(%d) = %d,%v want %d", i, got, ok, k)
				}
			}
		})
	}
}

func TestRankAndSelectAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var tr Tree
	ref := map[int64]int64{}
	for i := 0; i < 2000; i++ {
		k := rng.Int64N(5000)
		v := rng.Int64()
		tr = tr.Put(k, v)
		ref[k] = v
		if i%97 == 0 {
			delete(ref, k)
			tr = tr.Delete(k)
		}
	}
	checkTree(t, tr)

	keys := make([]int64, 0, len(ref))
	for k := range ref {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	if tr.Len() != len(keys) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(keys))
	}
	for i, k := range keys {
		if got := tr.Rank(k); got != i {
			t.Fatalf("Rank(%d) = %d, want %d", k, got, i)
		}
		got, ok := tr.Select(i)
		if !ok || got != k {
			t.Fatalf("Select(%d) = %d,%v want %d", i, got, ok, k)
		}
	}
	// Rank of keys between present keys.
	if got := tr.Rank(keys[0] - 1); got != 0 {
		t.Fatalf("Rank below min = %d", got)
	}
	if got := tr.Rank(keys[len(keys)-1] + 1); got != len(keys) {
		t.Fatalf("Rank above max = %d", got)
	}
	if _, ok := tr.Select(len(keys)); ok {
		t.Fatal("Select out of range must fail")
	}
}

func TestRangeIteration(t *testing.T) {
	var tr Tree
	var kvs []KV
	for i := int64(0); i < 100; i++ {
		kvs = append(kvs, KV{i * 10, i})
	}
	tr = tr.BatchPut(kvs)

	it := tr.Range(250, 640)
	var got []int64
	for it.Next() {
		got = append(got, it.Key())
	}
	if len(got) != 40 || got[0] != 250 || got[len(got)-1] != 640 {
		t.Fatalf("range [250,640] yielded %d keys, first/last %v/%v", len(got), got[0], got[len(got)-1])
	}

	// Full iteration is sorted and complete.
	all := collect(tr)
	if len(all) != 100 {
		t.Fatalf("full iteration yielded %d keys", len(all))
	}
	for i, kv := range all {
		if kv.Key != int64(i)*10 {
			t.Fatalf("position %d: key %d", i, kv.Key)
		}
	}
}

func TestIteratorIndependenceAcrossVersions(t *testing.T) {
	var v1 Tree
	for i := int64(0); i < 50; i++ {
		v1 = v1.Put(i, i*100)
	}
	v2 := v1.Delete(7).Delete(8).Put(1000, 1)

	it1, it2 := v1.Iterate(), v2.Iterate()
	// Interleave reads from both iterators; each must see its own version.
	for i := int64(0); i < 50; i++ {
		if !it1.Next() || it1.Key() != i {
			t.Fatalf("v1 iterator broke at %d", i)
		}
		if i < 7 && (!it2.Next() || it2.Key() != i) {
			t.Fatalf("v2 iterator broke at %d", i)
		}
	}
	// v2's remaining stream: 9..49 then 1000, no 7 or 8.
	for want := int64(9); want < 50; want++ {
		if !it2.Next() || it2.Key() != want {
			t.Fatalf("v2 iterator: want %d", want)
		}
	}
	if !it2.Next() || it2.Key() != 1000 || it2.Next() {
		t.Fatal("v2 iterator tail wrong")
	}
}

func TestBatchPutSharesPaths(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	var base Tree
	var kvs []KV
	for i := 0; i < 10000; i++ {
		kvs = append(kvs, KV{int64(i), rng.Int64()})
	}
	base = base.BatchPut(kvs)
	checkTree(t, base)

	// 200 scattered updates on distinct keys.
	perm := rng.Perm(10000)
	batch := make([]KV, 200)
	for i := range batch {
		batch[i] = KV{int64(perm[i]), rng.Int64()}
	}

	// BatchPut allocates each surviving node once; sequential Put allocates
	// a fresh root-to-leaf path per key and discards intermediate versions.
	var m0, m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	viaBatch := base.BatchPut(batch)
	runtime.ReadMemStats(&m1)
	viaSeq := base
	for _, kv := range batch {
		viaSeq = viaSeq.Put(kv.Key, kv.Val)
	}
	runtime.ReadMemStats(&m2)
	batchAllocs := m1.TotalAlloc - m0.TotalAlloc
	seqAllocs := m2.TotalAlloc - m1.TotalAlloc

	// Logical content must be identical (shape may differ: batch packs new
	// subtrees perfectly balanced, sequential inserts rebalance locally).
	gotB, gotS := collect(viaBatch), collect(viaSeq)
	if len(gotB) != len(gotS) {
		t.Fatalf("batch len %d != sequential len %d", len(gotB), len(gotS))
	}
	for i := range gotB {
		if gotB[i] != gotS[i] {
			t.Fatalf("position %d: batch %v != sequential %v", i, gotB[i], gotS[i])
		}
	}
	checkTree(t, viaBatch)

	// Batch must share strictly more nodes with the base version than the
	// sequential path-copying cascade does.
	sharedBatch := SharedNodes(base, viaBatch)
	t.Logf("base=%d nodes, shared with batch=%d; batch alloc=%d bytes, sequential alloc=%d bytes",
		base.Len(), sharedBatch, batchAllocs, seqAllocs)
	if batchAllocs >= seqAllocs {
		t.Fatalf("batch allocated %d bytes, not less than sequential %d", batchAllocs, seqAllocs)
	}
	if sharedBatch < base.Len()-200*20 { // sanity: most of the tree is reused
		t.Fatalf("batch copied too much: only %d/%d shared", sharedBatch, base.Len())
	}
}

func TestBatchPutDuplicateKeysLastWins(t *testing.T) {
	var tr Tree
	tr = tr.BatchPut([]KV{{1, 10}, {1, 11}, {1, 12}, {2, 20}})
	if got, _ := tr.Get(1); got != 12 {
		t.Fatalf("last write must win, got %d", got)
	}
	if tr.Len() != 2 {
		t.Fatalf("len = %d", tr.Len())
	}
}

func TestRandomOpsAgainstMap(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 43))
	var tr Tree
	ref := map[int64]int64{}
	for i := 0; i < 20000; i++ {
		k := rng.Int64N(3000)
		switch rng.Int64N(3) {
		case 0, 1:
			v := rng.Int64()
			tr = tr.Put(k, v)
			ref[k] = v
		default:
			tr = tr.Delete(k)
			delete(ref, k)
		}
		if i%997 == 0 {
			checkTree(t, tr)
		}
	}
	checkTree(t, tr)
	if tr.Len() != len(ref) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(ref))
	}
	for _, kv := range collect(tr) {
		if ref[kv.Key] != kv.Val {
			t.Fatalf("key %d: tree %d, ref %d", kv.Key, kv.Val, ref[kv.Key])
		}
	}
}
