package roaring

import (
	"math/rand"
	"sort"
	"testing"
)

// forcedSet builds a Set whose containers are forced into the given
// representation, to exercise cross-representation operations.
func forcedSet(t *testing.T, cfg Config, typ ctype, vals ...uint32) *Set {
	t.Helper()
	s := New(cfg)
	byBucket := map[uint16][]uint16{}
	for _, v := range vals {
		hi, lo := uint16(v>>16), uint16(v)
		byBucket[hi] = append(byBucket[hi], lo)
	}
	for hi, los := range byBucket {
		c := &container{typ: cArray}
		for _, lo := range los {
			c.add(lo)
		}
		s.buckets[hi] = c.convertTo(typ)
		s.card += uint64(c.card)
	}
	return s
}

func sortedCopy(m map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func expectItems(t *testing.T, s *Set, want map[uint32]struct{}, ctx string) {
	t.Helper()
	got := s.Items()
	exp := sortedCopy(want)
	if len(got) != len(exp) {
		t.Fatalf("%s: cardinality got %d want %d", ctx, len(got), len(exp))
	}
	for i := range got {
		if got[i] != exp[i] {
			t.Fatalf("%s: items differ at %d: got %d want %d", ctx, i, got[i], exp[i])
		}
	}
	if s.Cardinality() != uint64(len(exp)) {
		t.Fatalf("%s: Cardinality got %d want %d", ctx, s.Cardinality(), len(exp))
	}
}

// Elements must be identical before and after every representation switch.
func TestConversionRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	s := New(cfg)
	want := map[uint32]struct{}{}

	check := func(ctx string) {
		t.Helper()
		expectItems(t, s, want, ctx)
		for v := range want {
			if !s.Contains(v) {
				t.Fatalf("%s: Contains(%d) false", ctx, v)
			}
		}
	}

	// Phase 1: consecutive values -> run encoding.
	for i := uint32(0); i < 1000; i++ {
		s.Add(i)
		want[i] = struct{}{}
	}
	if typ, _ := s.bucketType(0); typ != cRun {
		t.Fatalf("consecutive data: got %s, want run", typ)
	}
	check("after run phase")

	// Phase 2: scattered values -> array, then bitmap.
	conversions := 0
	for i := uint32(0); i < 6000; i++ {
		v := (i * 7) % 65536
		s.Add(v)
		want[v] = struct{}{}
		if s.conversions != conversions {
			conversions = s.conversions
			check("after conversion during load")
		}
	}
	if typ, _ := s.bucketType(0); typ != cBitmap {
		t.Fatalf("dense scattered data: got %s, want bitmap", typ)
	}
	check("after bitmap phase")

	// Phase 3: delete the scattered values back out.
	for i := uint32(0); i < 6000; i++ {
		v := (i * 7) % 65536
		if _, ok := want[v]; !ok || v < 1000 {
			continue
		}
		s.Remove(v)
		delete(want, v)
		if s.conversions != conversions {
			conversions = s.conversions
			check("after conversion during drain")
		}
	}
	check("after drain")
	if typ, _ := s.bucketType(0); typ != cRun {
		t.Fatalf("after drain: got %s, want run", typ)
	}
}

// Deleting/adding a single element near a threshold must not oscillate.
func TestHysteresisNoThrashing(t *testing.T) {
	cfg := DefaultConfig() // ArrayMax 4096, BitmapMin 3584
	s := New(cfg)
	// Scattered data so run encoding never wins: card crosses ArrayMax.
	// Step 17 is coprime with 65536, so all 4097 values are distinct.
	for i := uint32(0); i < 4097; i++ {
		s.Add((i * 17) % 65536)
	}
	if typ, _ := s.bucketType(0); typ != cBitmap {
		t.Fatalf("card 4097: got %s, want bitmap", typ)
	}
	if s.conversions != 1 {
		t.Fatalf("expected exactly 1 conversion, got %d", s.conversions)
	}

	// Oscillate cardinality 4096 <-> 4097 many times: no new conversions.
	pivot := uint32((4096 * 17) % 65536)
	for i := 0; i < 1000; i++ {
		s.Remove(pivot) // 4096: still >= BitmapMin, stays bitmap
		if typ, _ := s.bucketType(0); typ != cBitmap {
			t.Fatalf("iter %d: flipped away from bitmap at card 4096", i)
		}
		s.Add(pivot) // 4097: already bitmap
	}
	if s.conversions != 1 {
		t.Fatalf("thrashing detected: %d conversions", s.conversions)
	}

	// Drain below BitmapMin: single switch to array.
	for i := uint32(0); i < 4097; i++ {
		v := (i * 17) % 65536
		if v == pivot {
			continue
		}
		s.Remove(v)
		if s.Cardinality() < 3584 {
			break
		}
	}
	if typ, _ := s.bucketType(0); typ != cArray {
		t.Fatalf("below BitmapMin: got %s, want array", typ)
	}
	if s.conversions != 2 {
		t.Fatalf("expected 2 conversions, got %d", s.conversions)
	}

	// Re-fill inside the hysteresis band: stays array until ArrayMax+1.
	card := s.Cardinality()
	for s.Cardinality() == card {
		// find a value not present to add back
		for v := uint32(0); ; v++ {
			if !s.Contains((v * 16) % 65536) {
				s.Add((v * 16) % 65536)
				break
			}
		}
		card++
		if card > 4096 {
			break
		}
	}
	if s.Cardinality() <= 4096 {
		if typ, _ := s.bucketType(0); typ != cArray {
			t.Fatalf("inside hysteresis band: got %s, want array", typ)
		}
	}
}

// Values on bucket edges must land in the correct high-16-bit bucket.
func TestBucketBoundary(t *testing.T) {
	s := New(DefaultConfig())
	vals := []uint32{0, 65535, 65536, 65537, 131071, 131072, 4294967295}
	for _, v := range vals {
		s.Add(v)
	}
	for _, v := range vals {
		if !s.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	wantBuckets := map[uint16][]uint16{
		0:     {0, 65535},
		1:     {0, 1, 65535},
		2:     {0},
		65535: {65535},
	}
	if len(s.buckets) != len(wantBuckets) {
		t.Fatalf("bucket count got %d want %d", len(s.buckets), len(wantBuckets))
	}
	for hi, los := range wantBuckets {
		c := s.buckets[hi]
		if c == nil {
			t.Fatalf("bucket %d missing", hi)
		}
		for _, lo := range los {
			if !c.contains(lo) {
				t.Fatalf("bucket %d missing low bits %d", hi, lo)
			}
		}
	}
	got := s.Items()
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatalf("Items not sorted: %v", got)
	}
	s.Remove(65536)
	if s.Contains(65536) || !s.Contains(65535) || !s.Contains(65537) {
		t.Fatalf("boundary remove corrupted neighbours")
	}
}

// All pairwise representation combinations must agree with a map reference.
func TestSetOpsAcrossRepresentations(t *testing.T) {
	cfg := DefaultConfig()
	var aVals, bVals []uint32
	for i := uint32(0); i < 1000; i++ { // consecutive block (run-friendly)
		aVals = append(aVals, i)
	}
	for i := uint32(0); i < 400; i++ { // scattered tail
		aVals = append(aVals, 2000+uint32((i*37)%9000))
	}
	for i := uint32(0); i < 800; i++ {
		bVals = append(bVals, 500+uint32(i)) // overlaps a's head
	}
	for i := uint32(0); i < 300; i++ {
		bVals = append(bVals, 3000+uint32((i*53)%9000))
	}

	ref := func(xs, ys []uint32, f func(x, y bool) bool) map[uint32]struct{} {
		xsSet := map[uint32]struct{}{}
		ysSet := map[uint32]struct{}{}
		for _, v := range xs {
			xsSet[v] = struct{}{}
		}
		for _, v := range ys {
			ysSet[v] = struct{}{}
		}
		out := map[uint32]struct{}{}
		for v := range xsSet {
			if f(true, member(ysSet, v)) {
				out[v] = struct{}{}
			}
		}
		for v := range ysSet {
			if f(member(xsSet, v), true) && !member(out, v) {
				if f(member(xsSet, v), true) {
					out[v] = struct{}{}
				}
			}
		}
		return out
	}

	types := []ctype{cArray, cRun, cBitmap}
	ops := []struct {
		name string
		do   func(a, b *Set) *Set
		f    func(x, y bool) bool
	}{
		{"Or", func(a, b *Set) *Set { return a.Or(b) }, func(x, y bool) bool { return x || y }},
		{"And", func(a, b *Set) *Set { return a.And(b) }, func(x, y bool) bool { return x && y }},
		{"AndNot", func(a, b *Set) *Set { return a.AndNot(b) }, func(x, y bool) bool { return x && !y }},
		{"Xor", func(a, b *Set) *Set { return a.Xor(b) }, func(x, y bool) bool { return x != y }},
	}
	for _, ta := range types {
		for _, tb := range types {
			a := forcedSet(t, cfg, ta, aVals...)
			b := forcedSet(t, cfg, tb, bVals...)
			for _, op := range ops {
				got := op.do(a, b)
				expectItems(t, got, ref(aVals, bVals, op.f),
					op.name+" "+ta.String()+"x"+tb.String())
			}
		}
	}
}

func member(m map[uint32]struct{}, v uint32) bool {
	_, ok := m[v]
	return ok
}

// Encoded byte sizes must match the physical layout exactly.
func TestByteSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Sparse scattered data -> array: 2 bytes/value.
	s := New(cfg)
	for i := 0; i < 100; i++ {
		s.Add(uint32(i * 500))
	}
	if got := s.Bytes(); got != 200 {
		t.Fatalf("array bytes got %d want 200", got)
	}

	// Consecutive data -> run: 4 bytes for one interval.
	s2 := New(cfg)
	for i := uint32(0); i < 1000; i++ {
		s2.Add(i)
	}
	if got := s2.Bytes(); got != 4 {
		t.Fatalf("run bytes got %d want 4", got)
	}

	// Dense scattered data -> bitmap: fixed 8192 bytes.
	s3 := New(cfg)
	for i := uint32(0); i < 5000; i++ {
		s3.Add((i * 7) % 65536)
	}
	if got := s3.Bytes(); got != 8192 {
		t.Fatalf("bitmap bytes got %d want 8192", got)
	}

	// Mixed stats across buckets.
	s4 := New(cfg)
	for i := uint32(0); i < 1000; i++ { // bucket 0: run
		s4.Add(i)
	}
	for i := 0; i < 100; i++ { // bucket 1: array
		s4.Add(65536 + uint32(i*500))
	}
	for i := uint32(0); i < 5000; i++ { // bucket 2: bitmap
		s4.Add(131072 + (i*7)%65536)
	}
	st := s4.Stats()
	if st.RunBuckets != 1 || st.ArrayBuckets != 1 || st.BitmapBuckets != 1 {
		t.Fatalf("bucket types: %+v", st)
	}
	if st.RunBytes != 4 || st.ArrayBytes != 200 || st.BitmapBytes != 8192 {
		t.Fatalf("byte split: %+v", st)
	}
	if st.TotalBytes != 4+200+8192 {
		t.Fatalf("total bytes got %d", st.TotalBytes)
	}
}

func TestRunMergeAndSplit(t *testing.T) {
	c := &container{typ: cRun}
	c.add(5)
	c.add(7)
	if len(c.runs) != 2 {
		t.Fatalf("expected 2 runs, got %v", c.runs)
	}
	c.add(6) // bridges the two runs
	if len(c.runs) != 1 || c.runs[0] != (interval{5, 7}) {
		t.Fatalf("merge failed: %v", c.runs)
	}
	c.remove(6) // splits back
	if len(c.runs) != 2 || c.runs[0] != (interval{5, 5}) || c.runs[1] != (interval{7, 7}) {
		t.Fatalf("split failed: %v", c.runs)
	}
	// Edge values of the 16-bit space.
	c2 := &container{typ: cRun}
	c2.add(0)
	c2.add(65535)
	if len(c2.runs) != 2 {
		t.Fatalf("edge runs: %v", c2.runs)
	}
	for v := uint32(0); v < 65536; v++ {
		c2.add(uint16(v))
	}
	if len(c2.runs) != 1 || c2.runs[0] != (interval{0, 65535}) || c2.card != 65536 {
		t.Fatalf("full range: %v card=%d", c2.runs, c2.card)
	}
}

func TestRemoveToEmpty(t *testing.T) {
	s := New(DefaultConfig())
	for i := uint32(0); i < 5000; i++ {
		s.Add((i * 7) % 65536)
	}
	for i := uint32(0); i < 5000; i++ {
		s.Remove((i * 7) % 65536)
	}
	if s.Cardinality() != 0 || len(s.buckets) != 0 || s.Bytes() != 0 {
		t.Fatalf("not empty: card=%d buckets=%d bytes=%d", s.Cardinality(), len(s.buckets), s.Bytes())
	}
	if s.Contains(0) {
		t.Fatalf("phantom element")
	}
}

// Fuzz-style cross-check against a map on random workloads.
func TestRandomAgainstMap(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := New(DefaultConfig())
	ref := map[uint32]struct{}{}
	for step := 0; step < 20000; step++ {
		var v uint32
		switch rng.Intn(3) {
		case 0: // cluster: run-friendly
			v = uint32(rng.Intn(4))<<16 | uint32(rng.Intn(2000))
		case 1: // dense scattered
			v = uint32(rng.Intn(4))<<16 | uint32(rng.Intn(65536))
		default: // full 32-bit range incl. bucket edges
			v = rng.Uint32()
		}
		if rng.Intn(2) == 0 {
			s.Add(v)
			ref[v] = struct{}{}
		} else {
			s.Remove(v)
			delete(ref, v)
		}
	}
	expectItems(t, s, ref, "random workload")
}
