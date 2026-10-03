package bitmap

import (
	"math/rand"
	"testing"
	"time"
)

type bruteSet map[uint64]struct{}

func setOf(b *Bitmap) bruteSet {
	s := bruteSet{}
	it := b.Iterator()
	for {
		v, ok := it.Next()
		if !ok {
			return s
		}
		s[v] = struct{}{}
	}
}

func mustEqualSets(t *testing.T, b *Bitmap, want bruteSet, op string) {
	t.Helper()
	got := setOf(b)
	if len(got) != len(want) {
		t.Fatalf("%s: cardinality got %d want %d", op, len(got), len(want))
	}
	for v := range want {
		if _, ok := got[v]; !ok {
			t.Fatalf("%s: missing value %d", op, v)
		}
	}
	if b.Cardinality() != uint64(len(want)) {
		t.Fatalf("%s: Cardinality() got %d want %d", op, b.Cardinality(), len(want))
	}
}

func TestSparseAddsStayArray(t *testing.T) {
	b := New()
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		b.Add(uint64(r.Intn(65536)))
	}
	st := b.Stats()
	if st.Containers != 1 || st.ArrayContainers != 1 {
		t.Fatalf("expected single array container, got %+v", st)
	}
	if st.Bytes > 2*4096 {
		t.Fatalf("array container too large: %d bytes", st.Bytes)
	}
}

func TestManySparseAddsConvertToBitmap(t *testing.T) {
	b := New()
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 20000; i++ {
		b.Add(uint64(r.Intn(65536)))
	}
	st := b.Stats()
	if st.BitmapContainers != 1 {
		t.Fatalf("expected bitmap container, got %+v", st)
	}
	if st.Bytes != bitmapContainerBytes {
		t.Fatalf("expected %d bytes, got %d", bitmapContainerBytes, st.Bytes)
	}
}

func TestDenseRangeUsesRunContainer(t *testing.T) {
	b := New()
	b.AddRange(0, 65535)
	st := b.Stats()
	if st.RunContainers != 1 || st.Bytes != runLengthOverhead {
		t.Fatalf("expected one 4-byte run container, got %+v", st)
	}
	if b.Cardinality() != 65536 {
		t.Fatalf("cardinality got %d want 65536", b.Cardinality())
	}
	for _, v := range []uint64{0, 1, 32768, 65535} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	if b.Contains(65536) {
		t.Fatal("unexpected value beyond range")
	}
}

func TestContiguousSingleAddsConvertToRun(t *testing.T) {
	b := New()
	for i := uint64(0); i < 5000; i++ {
		b.Add(i)
	}
	st := b.Stats()
	if st.RunContainers != 1 || st.Bytes != runLengthOverhead {
		t.Fatalf("expected run container after contiguous adds, got %+v", st)
	}
}

func TestAddRangeAcrossContainerBoundaries(t *testing.T) {
	b := New()
	const lo, hi = uint64(100), uint64(1) << 20
	b.AddRange(lo, hi)
	if got := b.Cardinality(); got != hi-lo+1 {
		t.Fatalf("cardinality got %d want %d", got, hi-lo+1)
	}
	for _, v := range []uint64{lo, lo + 1, 65535, 65536, hi - 1, hi} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	for _, v := range []uint64{lo - 1, hi + 1} {
		if b.Contains(v) {
			t.Fatalf("unexpected %d", v)
		}
	}
}

func TestHighKeySpace(t *testing.T) {
	b := New()
	ids := []uint64{0, 1, 10_000_000_000, 10_000_000_001, 1 << 40, (1 << 48) - 1}
	for _, id := range ids {
		b.Add(id)
	}
	for _, id := range ids {
		if !b.Contains(id) {
			t.Fatalf("missing %d", id)
		}
	}
	if b.Cardinality() != uint64(len(ids)) {
		t.Fatalf("cardinality got %d", b.Cardinality())
	}
}

// mixedBitmap builds a bitmap exercising all three container types.
func mixedBitmap(r *rand.Rand, keyBase uint64) (*Bitmap, bruteSet) {
	b := New()
	s := bruteSet{}
	add := func(v uint64) {
		b.Add(v)
		s[v] = struct{}{}
	}
	// sparse values -> array containers
	for i := 0; i < 300; i++ {
		add(keyBase<<16 + uint64(r.Intn(65536)))
	}
	// dense range -> run containers
	lo := (keyBase + 100) << 16
	hi := lo + 40000
	b.AddRange(lo, hi)
	for v := lo; v <= hi; v++ {
		s[v] = struct{}{}
	}
	// dense random -> bitmap containers
	for i := 0; i < 20000; i++ {
		add((keyBase+200)<<16 + uint64(r.Intn(65536)))
	}
	return b, s
}

func TestAndOrXorAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for trial := 0; trial < 5; trial++ {
		b1, s1 := mixedBitmap(r, uint64(trial))
		b2, s2 := mixedBitmap(r, uint64(trial))

		andWant := bruteSet{}
		for v := range s1 {
			if _, ok := s2[v]; ok {
				andWant[v] = struct{}{}
			}
		}
		mustEqualSets(t, b1.And(b2), andWant, "AND")
		if got := b1.AndCardinality(b2); got != uint64(len(andWant)) {
			t.Fatalf("AndCardinality got %d want %d", got, len(andWant))
		}

		orWant := bruteSet{}
		for v := range s1 {
			orWant[v] = struct{}{}
		}
		for v := range s2 {
			orWant[v] = struct{}{}
		}
		mustEqualSets(t, b1.Or(b2), orWant, "OR")

		xorWant := bruteSet{}
		for v := range s1 {
			if _, ok := s2[v]; !ok {
				xorWant[v] = struct{}{}
			}
		}
		for v := range s2 {
			if _, ok := s1[v]; !ok {
				xorWant[v] = struct{}{}
			}
		}
		mustEqualSets(t, b1.Xor(b2), xorWant, "XOR")
	}
}

func TestBatchOps(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	b1, s1 := mixedBitmap(r, 10)
	b2, s2 := mixedBitmap(r, 10)
	b3, s3 := mixedBitmap(r, 10)

	andWant := bruteSet{}
	for v := range s1 {
		if _, ok := s2[v]; ok {
			if _, ok := s3[v]; ok {
				andWant[v] = struct{}{}
			}
		}
	}
	mustEqualSets(t, b1.And(b2, b3), andWant, "batch AND")

	orWant := bruteSet{}
	for _, s := range []bruteSet{s1, s2, s3} {
		for v := range s {
			orWant[v] = struct{}{}
		}
	}
	mustEqualSets(t, b1.Or(b2, b3), orWant, "batch OR")

	xorWant := setOf(b1.Xor(b2).Xor(b3))
	mustEqualSets(t, b1.Xor(b2, b3), xorWant, "batch XOR")
}

func TestEmptyBitmapOps(t *testing.T) {
	a, b := New(), New()
	if !a.And(b).IsEmpty() || !a.Or(b).IsEmpty() || !a.Xor(b).IsEmpty() {
		t.Fatal("ops on empty bitmaps must be empty")
	}
	if a.AndCardinality(b) != 0 {
		t.Fatal("AndCardinality of empty bitmaps must be 0")
	}
	b.Add(42)
	if !a.And(b).IsEmpty() {
		t.Fatal("empty AND non-empty must be empty")
	}
	if got := a.Or(b).Cardinality(); got != 1 {
		t.Fatalf("empty OR singleton got %d", got)
	}
	if got := a.Xor(b).Cardinality(); got != 1 {
		t.Fatalf("empty XOR singleton got %d", got)
	}
}

func TestIteratorAscending(t *testing.T) {
	b := New()
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 5000; i++ {
		b.Add(uint64(r.Intn(1 << 22)))
	}
	b.AddRange(3_000_000, 3_100_000)
	it := b.Iterator()
	prev := uint64(0)
	first := true
	count := 0
	for {
		v, ok := it.Next()
		if !ok {
			break
		}
		if !first && v <= prev {
			t.Fatalf("iterator not strictly ascending at %d", v)
		}
		prev = v
		first = false
		count++
	}
	if uint64(count) != b.Cardinality() {
		t.Fatalf("iterated %d values, cardinality %d", count, b.Cardinality())
	}
}

func TestCloneIndependence(t *testing.T) {
	b := New()
	b.AddRange(0, 100000)
	c := b.Clone()
	if !b.Equals(c) {
		t.Fatal("clone must equal original")
	}
	c.Add(200000)
	if b.Contains(200000) {
		t.Fatal("mutating clone affected original")
	}
	if b.Equals(c) {
		t.Fatal("expected bitmaps to differ after mutation")
	}
}

func TestLargeScaleIntersection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	const containers = 4096 // 4096 * 65536 = 2.68e8 values per bitmap
	a, b := New(), New()
	for k := uint64(0); k < containers; k++ {
		a.AddRange(k<<16, k<<16+0xffff)
		b.AddRange(k<<16, k<<16+0xffff)
	}
	start := time.Now()
	got := a.AndCardinality(b)
	elapsed := time.Since(start)
	want := uint64(containers) * 65536
	if got != want {
		t.Fatalf("AndCardinality got %d want %d", got, want)
	}
	t.Logf("intersected 2 x %.2e values in %s", float64(want), elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("intersection too slow: %s", elapsed)
	}
}

func fullContainerBitmap(containers int) *Bitmap {
	b := New()
	for k := 0; k < containers; k++ {
		b.AddRange(uint64(k)<<16, uint64(k)<<16+0xffff)
	}
	return b
}

func BenchmarkAnd(b *testing.B) {
	x, y := fullContainerBitmap(256), fullContainerBitmap(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkOr(b *testing.B) {
	x, y := fullContainerBitmap(256), fullContainerBitmap(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}

func BenchmarkXor(b *testing.B) {
	x, y := fullContainerBitmap(256), fullContainerBitmap(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Xor(y)
	}
}

func BenchmarkAndCardinality(b *testing.B) {
	x, y := fullContainerBitmap(256), fullContainerBitmap(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.AndCardinality(y)
	}
}
