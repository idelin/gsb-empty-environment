package bitmap

import (
	"math/rand"
	"testing"
)

func oracleAnd(a, b map[uint64]bool) map[uint64]bool {
	out := make(map[uint64]bool)
	for v := range a {
		if b[v] {
			out[v] = true
		}
	}
	return out
}

func oracleOr(a, b map[uint64]bool) map[uint64]bool {
	out := make(map[uint64]bool)
	for v := range a {
		out[v] = true
	}
	for v := range b {
		out[v] = true
	}
	return out
}

func oracleXor(a, b map[uint64]bool) map[uint64]bool {
	out := make(map[uint64]bool)
	for v := range a {
		if !b[v] {
			out[v] = true
		}
	}
	for v := range b {
		if !a[v] {
			out[v] = true
		}
	}
	return out
}

func assertBitmapEq(t *testing.T, name string, bm *Bitmap, want map[uint64]bool) {
	t.Helper()
	if got := bm.Cardinality(); got != uint64(len(want)) {
		t.Fatalf("%s: cardinality=%d want %d", name, got, len(want))
	}
	for v := range want {
		if !bm.Contains(v) {
			t.Fatalf("%s: missing %d", name, v)
		}
	}
	// No extras.
	bm.ForEach(func(id uint64) {
		if !want[id] {
			t.Fatalf("%s: unexpected id %d", name, id)
		}
	})
}

func TestBitmapSparseAndDense(t *testing.T) {
	bm := New()
	want := make(map[uint64]bool)
	// Sparse discrete IDs across a wide 64-bit space.
	sparse := []uint64{0, 1, 65535, 65536, 1 << 20, 1 << 32, 1<<40 + 12345, 1<<48 - 1}
	for _, id := range sparse {
		if !bm.Add(id) {
			t.Fatalf("Add(%d) reported not-new", id)
		}
		want[id] = true
	}
	// Dense contiguous ranges.
	bm.AddRange(1<<22, 1<<22+9999)
	for i := uint64(1 << 22); i < 1<<22+10000; i++ {
		want[i] = true
	}
	bm.AddRange(5<<32, 5<<32+65535*2+10) // spans 3 containers
	for i := uint64(5 << 32); i <= 5<<32+65535*2+10; i++ {
		want[i] = true
	}
	assertBitmapEq(t, "sparse+dense", bm, want)

	// Duplicate adds report false.
	for _, id := range sparse {
		if bm.Add(id) {
			t.Fatalf("duplicate Add(%d) reported new", id)
		}
	}
	// Dense ranges must use RLE containers.
	st := bm.Stats()
	if st.RunContainers == 0 {
		t.Fatalf("dense ranges not stored as RLE: %+v", st)
	}
	if st.BitmapContainers != 0 {
		t.Fatalf("unexpected bitmap containers: %+v", st)
	}
}

func TestBitmapContainerAutoConversion(t *testing.T) {
	bm := New()
	// 5000 non-contiguous values in one container -> bitmap container.
	for i := 0; i < 5000; i++ {
		bm.Add(uint64(i * 12)) // i*12 < 65536 for i < 5462
	}
	st := bm.Stats()
	if st.BitmapContainers != 1 || st.ArrayContainers != 0 || st.RunContainers != 0 {
		t.Fatalf("want exactly one bitmap container, got %+v", st)
	}
	// Remove down to sparse -> converts back to array.
	for i := 0; i < 5000; i++ {
		id := uint64(i * 12)
		if i%10 != 0 { // keep every 10th -> 500 left
			bm.Remove(id)
		}
	}
	st = bm.Stats()
	if st.ArrayContainers != 1 || st.BitmapContainers != 0 {
		t.Fatalf("want one array container after removals, got %+v", st)
	}
	if bm.Cardinality() != 500 {
		t.Fatalf("cardinality=%d want 500", bm.Cardinality())
	}
}

func TestBitmapSetOpsAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 30; trial++ {
		a, oa := randomBitmap(rng)
		b, ob := randomBitmap(rng)
		assertBitmapEq(t, "and", a.And(b), oracleAnd(oa, ob))
		assertBitmapEq(t, "or", a.Or(b), oracleOr(oa, ob))
		assertBitmapEq(t, "xor", a.Xor(b), oracleXor(oa, ob))
	}
}

func randomBitmap(rng *rand.Rand) (*Bitmap, map[uint64]bool) {
	bm := New()
	want := make(map[uint64]bool)
	// A few sparse IDs.
	for i := 0; i < 50; i++ {
		id := uint64(rng.Intn(1 << 28))
		bm.Add(id)
		want[id] = true
	}
	// A few dense ranges.
	for i := 0; i < 4; i++ {
		lo := uint64(rng.Intn(1 << 24))
		hi := lo + uint64(rng.Intn(3000))
		bm.AddRange(lo, hi)
		for v := lo; v <= hi; v++ {
			want[v] = true
		}
	}
	return bm, want
}

func TestBatchCommit(t *testing.T) {
	bm := New()
	b := bm.Batch()
	for i := uint64(0); i < 1000; i++ {
		b.Add(i * 3)
	}
	b.Add(1 << 40).Add(1<<40 + 1)
	bm.Commit(b)
	if bm.Cardinality() != 1002 {
		t.Fatalf("cardinality=%d want 1002", bm.Cardinality())
	}
	if !bm.ContainsAll(0, 3, 999*3, 1<<40, 1<<40+1) {
		t.Fatal("ContainsAll failed after commit")
	}
	// Batch removal.
	rb := bm.Batch()
	for i := uint64(0); i < 500; i++ {
		rb.Remove(i * 3)
	}
	bm.Commit(rb)
	if bm.Cardinality() != 502 {
		t.Fatalf("cardinality=%d want 502", bm.Cardinality())
	}
	if bm.Contains(0) || !bm.Contains(500*3) {
		t.Fatal("batch removal wrong")
	}
}

func TestCountPresent(t *testing.T) {
	bm := New()
	bm.AddRange(100, 199)
	bm.Add(1 << 33)
	if got := bm.CountPresent(100, 150, 199, 200, 1<<33, 1<<34); got != 4 {
		t.Fatalf("CountPresent=%d want 4", got)
	}
	if bm.ContainsAll(100, 200) {
		t.Fatal("ContainsAll should be false")
	}
}

func TestForEachOrder(t *testing.T) {
	bm := New()
	bm.AddRange(10, 20)
	bm.Add(5)
	bm.Add(1 << 20)
	prev := uint64(0)
	first := true
	n := 0
	bm.ForEach(func(id uint64) {
		if !first && id <= prev {
			t.Fatalf("ForEach not ascending: %d after %d", id, prev)
		}
		first = false
		prev = id
		n++
	})
	if n != 13 {
		t.Fatalf("ForEach visited %d, want 13", n)
	}
}

func TestEmptyAndNilSafety(t *testing.T) {
	a, b := New(), New()
	if a.And(b).Cardinality() != 0 || a.Or(b).Cardinality() != 0 || a.Xor(b).Cardinality() != 0 {
		t.Fatal("empty ops should be empty")
	}
	if a.Remove(42) || a.Contains(42) {
		t.Fatal("empty bitmap ops wrong")
	}
	a.Add(7)
	if a.And(b).Cardinality() != 0 {
		t.Fatal("and with empty should be empty")
	}
	if a.Or(b).Cardinality() != 1 {
		t.Fatal("or with empty should copy")
	}
	if a.Xor(b).Cardinality() != 1 {
		t.Fatal("xor with empty should copy")
	}
}
