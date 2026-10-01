package crset

import (
	"math/rand"
	"testing"
)

func newNoRunSet(t *testing.T) *Set {
	t.Helper()
	s, err := NewSet(Config{SparseToBitmapCard: 4096, BitmapToSparseCard: 3584, RunSavingsFactor: 0})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func repOf(t *testing.T, s *Set, high uint16) repKind {
	t.Helper()
	i, ok := s.findBucket(high)
	if !ok {
		t.Fatalf("bucket %d missing", high)
	}
	return s.buckets[i].c.rep
}

func equalUint32s(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBucketBoundary checks that values straddling the 16-bit split land in
// the right buckets, including the extremes of the uint32 domain.
func TestBucketBoundary(t *testing.T) {
	s := New()
	vals := []uint32{0, 65535, 65536, 131071, 0xFFFFFFFF}
	for _, v := range vals {
		if !s.Add(v) {
			t.Fatalf("Add(%d) reported duplicate", v)
		}
	}
	if got := len(s.buckets); got != 3 {
		t.Fatalf("buckets = %d, want 3 (highs 0, 1, 65535)", got)
	}
	for i, wantHigh := range []uint16{0, 1, 0xFFFF} {
		if s.buckets[i].high != wantHigh {
			t.Fatalf("bucket %d high = %d, want %d", i, s.buckets[i].high, wantHigh)
		}
	}
	for _, v := range vals {
		if !s.Contains(v) {
			t.Fatalf("Contains(%d) = false", v)
		}
	}
	for _, v := range []uint32{1, 65534, 65537, 131072, 0xFFFFFFFE} {
		if s.Contains(v) {
			t.Fatalf("Contains(%d) = true", v)
		}
	}
	if got := s.Elements(); !equalUint32s(got, vals) {
		t.Fatalf("Elements = %v, want %v", got, vals)
	}
	// Removing the last member of a bucket drops the bucket itself.
	s.Remove(0)
	s.Remove(65535)
	if got := len(s.buckets); got != 2 {
		t.Fatalf("buckets after drain = %d, want 2", got)
	}
	if s.Contains(0) || s.Contains(65535) {
		t.Fatal("removed values still present")
	}
}

// TestSparseBitmapHysteresis fills a container just past the bitmap
// threshold, then hovers one element above/below it. The representation
// must not move until cardinality crosses the far side of the band.
func TestSparseBitmapHysteresis(t *testing.T) {
	s := newNoRunSet(t)
	stride := func(i int) uint32 { return uint32(3 * i) }
	for i := 0; i <= 4096; i++ { // 4097 elements -> bitmap
		s.Add(stride(i))
	}
	if got := repOf(t, s, 0); got != repBitmap {
		t.Fatalf("rep = %s, want bitmap", got)
	}
	if got := s.Conversions(); got != 1 {
		t.Fatalf("conversions = %d, want 1 (sparse->bitmap)", got)
	}
	before := s.Elements()

	// Hover: card oscillates 4096 <-> 4097, inside/at the band edge.
	edge := stride(4096)
	for k := 0; k < 1000; k++ {
		s.Remove(edge)
		s.Add(edge)
	}
	if got := repOf(t, s, 0); got != repBitmap {
		t.Fatalf("after hover rep = %s, want bitmap (no flip-flop)", got)
	}
	if got := s.Conversions(); got != 1 {
		t.Fatalf("after hover conversions = %d, want 1 (no flip-flop)", got)
	}
	if got := s.Elements(); !equalUint32s(got, before) {
		t.Fatal("elements changed while hovering")
	}

	// Drain: bitmap must hold until card < BitmapToSparseCard (3584).
	i := 4096
	for s.Cardinality() > 3584 {
		s.Remove(stride(i))
		i--
	}
	if got := repOf(t, s, 0); got != repBitmap {
		t.Fatalf("at card 3584 rep = %s, want bitmap (band)", got)
	}
	s.Remove(stride(i)) // card 3583 -> sparse
	if got := repOf(t, s, 0); got != repSparse {
		t.Fatalf("at card 3583 rep = %s, want sparse", got)
	}
	if got := s.Conversions(); got != 2 {
		t.Fatalf("conversions = %d, want 2", got)
	}
}

// TestRunLifecycle walks contiguous data through sparse -> run, then
// shatters it into run -> sparse, checking elements at every stage and
// verifying no oscillation when hovering at the exit threshold.
func TestRunLifecycle(t *testing.T) {
	s := New() // default config: RLE enabled
	for v := uint32(0); v < 5000; v++ {
		s.Add(v)
	}
	if got := repOf(t, s, 0); got != repRun {
		t.Fatalf("rep = %s, want run", got)
	}
	if runs := s.buckets[0].c.runs; len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	ref := map[uint32]bool{}
	for v := uint32(0); v < 5000; v++ {
		ref[v] = true
	}

	// Shatter: remove every 3rd value. Runs multiply until RLE is no
	// longer worth it; exactly one conversion (run->sparse) must happen.
	convBefore := s.Conversions()
	for v := uint32(1); v < 5000; v += 3 {
		s.Remove(v)
		delete(ref, v)
	}
	if got := repOf(t, s, 0); got != repSparse {
		t.Fatalf("after shatter rep = %s, want sparse", got)
	}
	if got := s.Conversions(); got != convBefore+1 {
		t.Fatalf("shatter caused %d conversions, want exactly 1", got-convBefore)
	}
	if got, want := s.Cardinality(), len(ref); got != want {
		t.Fatalf("card = %d, want %d", got, want)
	}

	// Hover at the exit threshold: alternating add/remove of an isolated
	// value must not re-enter RLE (entry needs 2x savings, we are at ~1x).
	convBefore = s.Conversions()
	for k := 0; k < 1000; k++ {
		s.Add(1)
		s.Remove(1)
	}
	if got := s.Conversions(); got != convBefore {
		t.Fatalf("hover caused %d conversions, want 0", got-convBefore)
	}
	if got := repOf(t, s, 0); got != repSparse {
		t.Fatalf("after hover rep = %s, want sparse", got)
	}

	// Elements still exactly the reference set.
	got := s.Elements()
	if len(got) != len(ref) {
		t.Fatalf("elements = %d, want %d", len(got), len(ref))
	}
	for _, v := range got {
		if !ref[v] {
			t.Fatalf("unexpected element %d", v)
		}
	}
}

// TestRunToBitmapLifecycle drives a large contiguous container through its
// full representation lifecycle as elements are removed:
// run -> bitmap (runs exceed 64 KiB of RLE) -> run (card <= 8192 makes RLE
// 2x cheaper than bitmap again) -> sparse (card 3584, sparse beats RLE).
func TestRunToBitmapLifecycle(t *testing.T) {
	s := New()
	for v := uint32(0); v < 40000; v++ {
		s.Add(v)
	}
	if got := repOf(t, s, 0); got != repRun {
		t.Fatalf("rep = %s, want run", got)
	}
	// Remove all odd values: 20000 runs, 80000 RLE bytes > 65536 -> bitmap.
	for v := uint32(1); v < 40000; v += 2 {
		s.Remove(v)
	}
	if got := repOf(t, s, 0); got != repBitmap {
		t.Fatalf("rep = %s, want bitmap", got)
	}
	if got := s.Cardinality(); got != 20000 {
		t.Fatalf("card = %d, want 20000", got)
	}
	// Drain evens. Remaining data is stride-2, so runs == card.
	drain := func(target int) {
		t.Helper()
		for v := uint32(0); v < 40000 && s.Cardinality() > target; v += 2 {
			s.Remove(v)
		}
		if got := s.Cardinality(); got != target {
			t.Fatalf("card = %d, want %d", got, target)
		}
	}
	drain(9000)
	if got := repOf(t, s, 0); got != repBitmap {
		t.Fatalf("at card 9000 rep = %s, want bitmap", got)
	}
	// At card 8192 RLE (32768 B) is exactly 2x smaller than bitmap: -> run.
	drain(8100)
	if got := repOf(t, s, 0); got != repRun {
		t.Fatalf("at card 8100 rep = %s, want run", got)
	}
	// At card 3584 sparse (7168 B) beats RLE (14336 B): -> sparse.
	drain(3583)
	if got := repOf(t, s, 0); got != repSparse {
		t.Fatalf("at card 3583 rep = %s, want sparse", got)
	}
	drain(0)
	if got := s.Cardinality(); got != 0 {
		t.Fatalf("card = %d, want 0", got)
	}
	if got := len(s.buckets); got != 0 {
		t.Fatalf("buckets = %d, want 0", got)
	}
	// Conversions: sparse->run (fill), run->bitmap, bitmap->run, run->sparse.
	if got := s.Conversions(); got != 4 {
		t.Fatalf("conversions = %d, want 4", got)
	}
}

// TestByteStats verifies the exact encoded layout sizes.
func TestByteStats(t *testing.T) {
	// One RLE bucket: 2 (key) + 3 (header) + 4 (one run) = 9 bytes.
	s := New()
	for v := uint32(0); v < 1000; v++ {
		s.Add(v)
	}
	st := s.Stats()
	if st.TotalBytes != 9 {
		t.Fatalf("run TotalBytes = %d, want 9", st.TotalBytes)
	}
	if st.Run.Buckets != 1 || st.Run.Bytes != 7 || st.Run.Elements != 1000 {
		t.Fatalf("run stats = %+v", st.Run)
	}
	if st.DirectoryBytes != 2 || st.Elements != 1000 {
		t.Fatalf("stats = %+v", st)
	}

	// One sparse bucket (RLE disabled): 2 + 3 + 2*100 = 205 bytes.
	sp := newNoRunSet(t)
	for i := 0; i < 100; i++ {
		sp.Add(uint32(3 * i))
	}
	if st := sp.Stats(); st.TotalBytes != 205 {
		t.Fatalf("sparse TotalBytes = %d, want 205", st.TotalBytes)
	}

	// One bitmap bucket (RLE disabled): 2 + 3 + 65536 = 65541 bytes.
	bm := newNoRunSet(t)
	for i := 0; i < 5001; i++ {
		bm.Add(uint32(3 * i))
	}
	if st := bm.Stats(); st.TotalBytes != 65541 {
		t.Fatalf("bitmap TotalBytes = %d, want 65541", st.TotalBytes)
	}

	// Two RLE buckets: 2*2 (keys) + 2*(3+4) = 18 bytes.
	two := New()
	for v := uint32(0); v < 1000; v++ {
		two.Add(v)
		two.Add(100000 + v)
	}
	if st := two.Stats(); st.TotalBytes != 18 || st.Buckets != 2 {
		t.Fatalf("two-bucket stats = %+v", st)
	}
}

// TestFuzzAgainstMap applies random mutations and periodically compares the
// full element sequence and byte accounting against a map reference.
func TestFuzzAgainstMap(t *testing.T) {
	s := New()
	ref := map[uint32]bool{}
	rng := rand.New(rand.NewSource(42))
	check := func(op int) {
		t.Helper()
		got := s.Elements()
		if len(got) != len(ref) {
			t.Fatalf("op %d: %d elements, want %d", op, len(got), len(ref))
		}
		for _, v := range got {
			if !ref[v] {
				t.Fatalf("op %d: unexpected element %d", op, v)
			}
		}
		for i := 1; i < len(got); i++ {
			if got[i] <= got[i-1] {
				t.Fatalf("op %d: elements not strictly ascending at %d", op, i)
			}
		}
		st := s.Stats()
		if st.Elements != len(ref) {
			t.Fatalf("op %d: stats elements = %d, want %d", op, st.Elements, len(ref))
		}
		wantBytes := 0
		for i := range s.buckets {
			wantBytes += bucketKeyBytes + containerHeaderBytes + s.buckets[i].c.payloadBytes()
		}
		if st.TotalBytes != wantBytes {
			t.Fatalf("op %d: TotalBytes = %d, want %d", op, st.TotalBytes, wantBytes)
		}
	}
	for op := 0; op < 150000; op++ {
		v := uint32(rng.Intn(300000)) // spans ~5 buckets
		if rng.Intn(100) < 55 {
			if got, want := s.Add(v), !ref[v]; got != want {
				t.Fatalf("op %d: Add(%d) = %v, want %v", op, v, got, want)
			}
			ref[v] = true
		} else {
			if got, want := s.Remove(v), ref[v]; got != want {
				t.Fatalf("op %d: Remove(%d) = %v, want %v", op, v, got, want)
			}
			delete(ref, v)
		}
		if op%3000 == 0 {
			check(op)
		}
	}
	check(150000)
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewSet(Config{}); err != nil {
		t.Fatalf("zero config should select defaults: %v", err)
	}
	bad := []Config{
		{SparseToBitmapCard: 0, BitmapToSparseCard: 0, RunSavingsFactor: 2},
		{SparseToBitmapCard: 65537, BitmapToSparseCard: 0, RunSavingsFactor: 2},
		{SparseToBitmapCard: 4096, BitmapToSparseCard: 4096, RunSavingsFactor: 2},
		{SparseToBitmapCard: 4096, BitmapToSparseCard: -1, RunSavingsFactor: 2},
		{SparseToBitmapCard: 4096, BitmapToSparseCard: 3584, RunSavingsFactor: 1},
	}
	for i, cfg := range bad {
		if _, err := NewSet(cfg); err == nil {
			t.Fatalf("bad config %d accepted: %+v", i, cfg)
		}
	}
	if _, err := NewSet(Config{SparseToBitmapCard: 1024, BitmapToSparseCard: 512, RunSavingsFactor: 0}); err != nil {
		t.Fatalf("valid custom config rejected: %v", err)
	}
}

func TestDuplicateAndMissing(t *testing.T) {
	s := New()
	if !s.Add(7) || s.Add(7) {
		t.Fatal("Add duplicate reporting wrong")
	}
	if s.Remove(8) || !s.Remove(7) || s.Remove(7) {
		t.Fatal("Remove reporting wrong")
	}
	if got := s.Cardinality(); got != 0 {
		t.Fatalf("card = %d, want 0", got)
	}
	if got := s.Elements(); len(got) != 0 {
		t.Fatalf("elements = %v, want empty", got)
	}
}
