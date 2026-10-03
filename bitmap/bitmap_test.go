package bitmap

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func containerKind(b *Bitmap, key uint16) string {
	i, ok := b.search(key)
	if !ok {
		return "<none>"
	}
	switch b.conts[i].(type) {
	case *arrayContainer:
		return "array"
	case *bitmapContainer:
		return "bitmap"
	case *runContainer:
		return "run"
	}
	return "<unknown>"
}

func TestSparseInsertSelectsArrayContainer(t *testing.T) {
	b := New()
	for i := 0; i < 100; i++ {
		b.Add(uint32(i * 997)) // sparse, well-separated IDs
	}
	if got := containerKind(b, 0); got != "array" {
		t.Fatalf("sparse inserts: want array container, got %s", got)
	}
	if b.Cardinality() != 100 {
		t.Fatalf("cardinality = %d, want 100", b.Cardinality())
	}
	for i := 0; i < 100; i++ {
		if !b.Contains(uint32(i * 997)) {
			t.Fatalf("missing %d", i*997)
		}
	}
}

func TestDenseRangeSelectsRunContainer(t *testing.T) {
	b := New()
	b.AddRange(0, 100000) // dense contiguous interval
	if got := containerKind(b, 0); got != "run" {
		t.Fatalf("dense range: want run container, got %s", got)
	}
	if got := containerKind(b, 1); got != "run" {
		t.Fatalf("dense range: want run container, got %s", got)
	}
	if b.Cardinality() != 100000 {
		t.Fatalf("cardinality = %d, want 100000", b.Cardinality())
	}
}

func TestArrayToBitmapConversionAtThreshold(t *testing.T) {
	b := New()
	// 4096 well-scattered values in one container stay an array.
	for i := 0; i < arrayMaxCardinality; i++ {
		b.Add(uint32(i * 16)) // 0, 16, 32, ... 65520
	}
	if got := containerKind(b, 0); got != "array" {
		t.Fatalf("at %d values: want array, got %s", arrayMaxCardinality, got)
	}
	// One more distinct value crosses the threshold.
	b.Add(7)
	if got := containerKind(b, 0); got != "bitmap" {
		t.Fatalf("at %d values: want bitmap, got %s", arrayMaxCardinality+1, got)
	}
	if b.Cardinality() != arrayMaxCardinality+1 {
		t.Fatalf("cardinality = %d", b.Cardinality())
	}
}

func TestOptimizeBitmapToRun(t *testing.T) {
	b := New()
	// Dense but built by point inserts -> becomes bitmap container.
	for i := 0; i < 5000; i++ {
		b.Add(uint32(i))
	}
	if got := containerKind(b, 0); got != "bitmap" {
		t.Fatalf("before optimize: want bitmap, got %s", got)
	}
	b.Optimize()
	if got := containerKind(b, 0); got != "run" {
		t.Fatalf("after optimize: want run, got %s", got)
	}
	if b.Cardinality() != 5000 {
		t.Fatalf("cardinality = %d, want 5000", b.Cardinality())
	}
}

func TestRunContainerMergesAdjacentInserts(t *testing.T) {
	rc := &runContainer{}
	for _, v := range []uint16{5, 6, 4, 10, 8, 9, 7} {
		rc = rc.add(v).(*runContainer)
	}
	if len(rc.runs) != 1 || rc.runs[0] != (run{4, 10}) {
		t.Fatalf("runs = %+v, want single run [4,10]", rc.runs)
	}
	if rc.cardinality() != 7 {
		t.Fatalf("cardinality = %d, want 7", rc.cardinality())
	}
}

// referenceSet is the ground-truth model used to validate operations.
func bitmapFromSet(s map[uint32]bool) *Bitmap {
	b := New()
	for v := range s {
		b.Add(v)
	}
	return b
}

func setOf(b *Bitmap) map[uint32]bool {
	out := make(map[uint32]bool)
	for _, v := range b.ToSlice() {
		out[v] = true
	}
	return out
}

func randomSets(r *rand.Rand, n int) (map[uint32]bool, map[uint32]bool) {
	a, b := make(map[uint32]bool), make(map[uint32]bool)
	for i := 0; i < n; i++ {
		// Mix sparse random values with dense clusters to exercise
		// all three container kinds and their combinations.
		base := uint32(r.Intn(1 << 20))
		if r.Intn(2) == 0 {
			for j := 0; j < r.Intn(300); j++ {
				a[base+uint32(j)] = true
			}
		} else {
			a[uint32(r.Intn(1<<24))] = true
		}
		base = uint32(r.Intn(1 << 20))
		if r.Intn(2) == 0 {
			for j := 0; j < r.Intn(300); j++ {
				b[base+uint32(j)] = true
			}
		} else {
			b[uint32(r.Intn(1<<24))] = true
		}
	}
	return a, b
}

func TestAndOrXorAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		sa, sb := randomSets(r, 200)
		ba, bb := bitmapFromSet(sa), bitmapFromSet(sb)

		and, or, xor := make(map[uint32]bool), make(map[uint32]bool), make(map[uint32]bool)
		for v := range sa {
			or[v] = true
			if sb[v] {
				and[v] = true
			} else {
				xor[v] = true
			}
		}
		for v := range sb {
			or[v] = true
			if !sa[v] {
				xor[v] = true
			}
		}

		if got := setOf(And(ba, bb)); !reflect.DeepEqual(got, and) {
			t.Fatalf("trial %d: AND mismatch (got %d, want %d)", trial, len(got), len(and))
		}
		if got := setOf(Or(ba, bb)); !reflect.DeepEqual(got, or) {
			t.Fatalf("trial %d: OR mismatch (got %d, want %d)", trial, len(got), len(or))
		}
		if got := setOf(Xor(ba, bb)); !reflect.DeepEqual(got, xor) {
			t.Fatalf("trial %d: XOR mismatch (got %d, want %d)", trial, len(got), len(xor))
		}
	}
}

func TestBatchOperations(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	var sets []map[uint32]bool
	var bms []*Bitmap
	for i := 0; i < 5; i++ {
		s := make(map[uint32]bool)
		for j := 0; j < 500; j++ {
			s[uint32(r.Intn(100000))] = true
		}
		sets = append(sets, s)
		bms = append(bms, bitmapFromSet(s))
	}

	wantAnd := make(map[uint32]bool)
	for v := range sets[0] {
		inAll := true
		for _, s := range sets[1:] {
			if !s[v] {
				inAll = false
				break
			}
		}
		if inAll {
			wantAnd[v] = true
		}
	}
	wantOr := make(map[uint32]bool)
	wantXor := make(map[uint32]bool)
	for _, s := range sets {
		for v := range s {
			wantOr[v] = true
			if wantXor[v] {
				delete(wantXor, v)
			} else {
				wantXor[v] = true
			}
		}
	}

	if got := setOf(AndAll(bms...)); !reflect.DeepEqual(got, wantAnd) {
		t.Fatalf("AndAll mismatch: got %d, want %d", len(got), len(wantAnd))
	}
	if got := setOf(OrAll(bms...)); !reflect.DeepEqual(got, wantOr) {
		t.Fatalf("OrAll mismatch: got %d, want %d", len(got), len(wantOr))
	}
	if got := setOf(XorAll(bms...)); !reflect.DeepEqual(got, wantXor) {
		t.Fatalf("XorAll mismatch: got %d, want %d", len(got), len(wantXor))
	}
	if got := AndAll().Cardinality(); got != 0 {
		t.Fatalf("AndAll() = %d, want 0", got)
	}
}

func TestAddRangeFullKeySpace(t *testing.T) {
	b := New()
	b.AddRange(0, 1<<32) // entire uint32 space: 4,294,967,296 values
	if got := b.Cardinality(); got != 1<<32 {
		t.Fatalf("cardinality = %d, want %d", got, int64(1)<<32)
	}
	for _, v := range []uint32{0, 1, 65535, 65536, 1 << 31, 0xFFFFFFFF} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	_, _, runs := b.ContainerSummary()
	if runs != 65536 {
		t.Fatalf("run containers = %d, want 65536", runs)
	}
}

// TestLargeScaleSetOperations simulates billion-scale data: two bitmaps
// covering most of the 32-bit space (~4.29 billion IDs) must intersect
// and union in microseconds thanks to RLE containers.
func TestLargeScaleSetOperations(t *testing.T) {
	a := New()
	a.AddRange(0, 3500000000) // 3.5e9 IDs
	b := New()
	b.AddRange(1000000000, 1<<32) // ~3.29e9 IDs

	start := time.Now()
	inter := And(a, b)
	andDur := time.Since(start)
	if got := inter.Cardinality(); got != 2500000000 {
		t.Fatalf("AND cardinality = %d, want 2500000000", got)
	}

	start = time.Now()
	union := Or(a, b)
	orDur := time.Since(start)
	if got := union.Cardinality(); got != 1<<32 {
		t.Fatalf("OR cardinality = %d, want %d", got, int64(1)<<32)
	}

	start = time.Now()
	diff := Xor(a, b)
	xorDur := time.Since(start)
	if got := diff.Cardinality(); got != 1000000000+(1<<32-3500000000) {
		t.Fatalf("XOR cardinality = %d", got)
	}

	// The limit guards against algorithmic regressions (e.g. value
	// materialization, which takes minutes at this scale). On normal
	// hardware these ops complete in microseconds-to-milliseconds
	// because cost is O(containers), independent of cardinality.
	limit := 30 * time.Second
	if andDur > limit || orDur > limit || xorDur > limit {
		t.Fatalf("too slow: AND=%v OR=%v XOR=%v (limit %v)", andDur, orDur, xorDur, limit)
	}
	t.Logf("billion-scale ops (4.29e9-id space): AND=%v OR=%v XOR=%v", andDur, orDur, xorDur)
}

func TestEmptyAndDisjoint(t *testing.T) {
	a, b := New(), New()
	if !And(a, b).IsEmpty() || !Or(a, b).IsEmpty() || !Xor(a, b).IsEmpty() {
		t.Fatal("ops on empty bitmaps must be empty")
	}
	a.AddRange(0, 100)
	b.AddRange(100, 200)
	if got := And(a, b).Cardinality(); got != 0 {
		t.Fatalf("disjoint AND = %d, want 0", got)
	}
	if got := Or(a, b).Cardinality(); got != 200 {
		t.Fatalf("disjoint OR = %d, want 200", got)
	}
	if got := Xor(a, b).Cardinality(); got != 200 {
		t.Fatalf("disjoint XOR = %d, want 200", got)
	}
}

func TestResultsAreCompact(t *testing.T) {
	// Intersection of two dense ranges must itself be a run container.
	a, b := New(), New()
	a.AddRange(0, 100000)
	b.AddRange(50000, 150000)
	inter := And(a, b)
	if got := containerKind(inter, 0); got != "run" {
		t.Fatalf("AND of dense ranges: want run container, got %s", got)
	}
	union := Or(a, b)
	if got := containerKind(union, 0); got != "run" && got != "bitmap" {
		t.Fatalf("OR of dense ranges: unexpected container %s", got)
	}
	union.Optimize()
	if got := containerKind(union, 0); got != "run" {
		t.Fatalf("OR of dense ranges after optimize: want run, got %s", got)
	}
}
