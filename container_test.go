package roaring

import (
	"math/rand"
	"testing"
)

func TestArrayToBitmapConversion(t *testing.T) {
	var c container = &arrayContainer{}
	for i := 0; i < arrayMaxCardinality; i++ {
		c, _ = c.add(uint16(i * 7 % 65536))
	}
	if c.ctype() != typeArray {
		t.Fatalf("expected array at %d elements, got type %d", arrayMaxCardinality, c.ctype())
	}
	// Add enough distinct values to cross the threshold.
	for i := 0; i < 65536 && c.cardinality() <= arrayMaxCardinality; i++ {
		c, _ = c.add(uint16(i))
	}
	if c.ctype() != typeBitmap {
		t.Fatalf("expected bitmap past %d elements, got type %d", arrayMaxCardinality, c.ctype())
	}
	if c.cardinality() != arrayMaxCardinality+1 {
		t.Fatalf("cardinality = %d, want %d", c.cardinality(), arrayMaxCardinality+1)
	}
}

func TestBitmapDemotedToArray(t *testing.T) {
	// Scattered (non-contiguous) values so the run encoding is not the
	// most compact choice after demotion.
	bc := &bitmapContainer{}
	for i := 0; i < 5000; i++ {
		bc.add(uint16(2 * i))
	}
	if bc.ctype() != typeBitmap {
		t.Fatal("expected bitmap")
	}
	var c container = bc
	for i := 0; i < 1200; i++ {
		c, _ = c.remove(uint16(2 * i))
	}
	c = normalize(c)
	if c.ctype() != typeArray {
		t.Fatalf("expected array after removals, got type %d", c.ctype())
	}
	if c.cardinality() != 3800 {
		t.Fatalf("cardinality = %d, want 3800", c.cardinality())
	}
}

func TestRunContainerSelectedForContiguousData(t *testing.T) {
	var c container = &arrayContainer{}
	c = c.addRange(100, 2000)
	c = normalize(c)
	if c.ctype() != typeRun {
		t.Fatalf("expected run container for contiguous range, got type %d", c.ctype())
	}
	if c.cardinality() != 1901 {
		t.Fatalf("cardinality = %d, want 1901", c.cardinality())
	}
	for _, v := range []uint16{100, 1000, 2000} {
		if !c.contains(v) {
			t.Fatalf("run container missing %d", v)
		}
	}
	for _, v := range []uint16{99, 2001} {
		if c.contains(v) {
			t.Fatalf("run container unexpectedly contains %d", v)
		}
	}
}

func TestArrayAddRangeEdges(t *testing.T) {
	// Full 16-bit range: must terminate and cover everything.
	var c container = &arrayContainer{}
	c = c.addRange(0, 0xFFFF)
	if c.cardinality() != 65536 {
		t.Fatalf("cardinality = %d, want 65536", c.cardinality())
	}
	if !c.contains(0) || !c.contains(0xFFFF) {
		t.Fatal("full range missing endpoints")
	}

	// Single element range.
	var c2 container = &arrayContainer{}
	c2 = c2.addRange(42, 42)
	if c2.cardinality() != 1 || !c2.contains(42) {
		t.Fatal("single-element range broken")
	}

	// Overlapping ranges merge without duplicates.
	var c3 container = &arrayContainer{}
	c3 = c3.addRange(10, 20)
	c3 = c3.addRange(15, 30)
	if c3.cardinality() != 21 {
		t.Fatalf("cardinality = %d, want 21", c3.cardinality())
	}
}

func TestRunAddMergesAdjacent(t *testing.T) {
	rc := &runContainer{}
	for _, v := range []uint16{5, 6, 7, 1, 2, 4, 3} {
		c, added := rc.add(v)
		rc = c.(*runContainer)
		if !added {
			t.Fatalf("add(%d) reported duplicate", v)
		}
	}
	if len(rc.runs) != 1 || rc.runs[0] != (interval{1, 7}) {
		t.Fatalf("runs = %+v, want single [1,7]", rc.runs)
	}
	// Duplicate add.
	if _, added := rc.add(4); added {
		t.Fatal("duplicate add reported as added")
	}
	// Bridging add merges two runs.
	c, _ := rc.add(20)
	rc = c.(*runContainer)
	c, _ = rc.add(22)
	rc = c.(*runContainer)
	c, _ = rc.add(21)
	rc = c.(*runContainer)
	if len(rc.runs) != 2 || rc.runs[1] != (interval{20, 22}) {
		t.Fatalf("runs = %+v", rc.runs)
	}
}

func TestRunAddBoundaryValues(t *testing.T) {
	rc := &runContainer{}
	c, _ := rc.add(0)
	rc = c.(*runContainer)
	c, _ = rc.add(0xFFFF)
	rc = c.(*runContainer)
	c, _ = rc.add(1)
	rc = c.(*runContainer)
	if !rc.contains(0) || !rc.contains(1) || !rc.contains(0xFFFF) {
		t.Fatal("boundary values missing")
	}
	if len(rc.runs) != 2 {
		t.Fatalf("runs = %+v, want 2 runs", rc.runs)
	}
}

func TestRunRemoveSplits(t *testing.T) {
	rc := &runContainer{}
	c := rc.addRange(10, 20)
	rc = c.(*runContainer)
	c, _ = rc.remove(15)
	rc = c.(*runContainer)
	if len(rc.runs) != 2 || rc.runs[0] != (interval{10, 14}) || rc.runs[1] != (interval{16, 20}) {
		t.Fatalf("runs after split = %+v", rc.runs)
	}
	c, _ = rc.remove(10)
	rc = c.(*runContainer)
	c, _ = rc.remove(20)
	rc = c.(*runContainer)
	if rc.runs[0] != (interval{11, 14}) || rc.runs[1] != (interval{16, 19}) {
		t.Fatalf("runs after edge trims = %+v", rc.runs)
	}
	if _, removed := rc.remove(100); removed {
		t.Fatal("remove of absent value reported success")
	}
}

func TestRunAddRangeMerges(t *testing.T) {
	rc := &runContainer{}
	c := rc.addRange(100, 199)
	rc = c.(*runContainer)
	c = rc.addRange(300, 399)
	rc = c.(*runContainer)
	c = rc.addRange(150, 350) // bridges both runs
	rc = c.(*runContainer)
	if len(rc.runs) != 1 || rc.runs[0] != (interval{100, 399}) {
		t.Fatalf("runs = %+v, want single [100,399]", rc.runs)
	}
	// Full-range addRange on a run container.
	c = rc.addRange(0, 0xFFFF)
	rc = c.(*runContainer)
	if len(rc.runs) != 1 || rc.runs[0] != (interval{0, 0xFFFF}) {
		t.Fatalf("runs = %+v, want single [0,65535]", rc.runs)
	}
}

// referenceSet is the ground truth for randomized differential tests.
type referenceSet struct {
	m [65536]bool
	n int
}

func (r *referenceSet) add(x uint16) {
	if !r.m[x] {
		r.m[x] = true
		r.n++
	}
}
func (r *referenceSet) and(o *referenceSet) *referenceSet {
	out := &referenceSet{}
	for v := uint32(0); v < 65536; v++ {
		if r.m[v] && o.m[v] {
			out.add(uint16(v))
		}
	}
	return out
}
func (r *referenceSet) or(o *referenceSet) *referenceSet {
	out := &referenceSet{}
	for v := uint32(0); v < 65536; v++ {
		if r.m[v] || o.m[v] {
			out.add(uint16(v))
		}
	}
	return out
}
func (r *referenceSet) xor(o *referenceSet) *referenceSet {
	out := &referenceSet{}
	for v := uint32(0); v < 65536; v++ {
		if r.m[v] != o.m[v] {
			out.add(uint16(v))
		}
	}
	return out
}
func (r *referenceSet) contains(x uint16) bool { return r.m[x] }

func randomContainer(rng *rand.Rand, kind int) (container, *referenceSet) {
	ref := &referenceSet{}
	var c container
	switch kind {
	case typeArray:
		ac := &arrayContainer{}
		n := rng.Intn(arrayMaxCardinality)
		for i := 0; i < n; i++ {
			v := uint16(rng.Intn(65536))
			var cc container = ac
			cc, _ = cc.add(v)
			ac = cc.(*arrayContainer) // stays array: n <= 4096
			ref.add(v)
		}
		c = ac
	case typeBitmap:
		bc := &bitmapContainer{}
		for i := 0; i < 12000; i++ {
			v := uint16(rng.Intn(65536))
			bc.add(v)
			ref.add(v)
		}
		c = bc
	case typeRun:
		rc := &runContainer{}
		var cc container = rc
		for i := 0; i < 1+rng.Intn(20); i++ {
			lo := uint16(rng.Intn(65536))
			hi := lo + uint16(rng.Intn(3000))
			if hi < lo { // overflowed
				hi = 0xFFFF
			}
			cc = cc.addRange(lo, hi)
			for v := uint32(lo); v <= uint32(hi); v++ {
				ref.add(uint16(v))
			}
		}
		c = cc
	}
	return c, ref
}

func checkAgainstReference(t *testing.T, c container, ref *referenceSet, what string) {
	t.Helper()
	if c == nil {
		if ref.n != 0 {
			t.Fatalf("%s: got empty container, want %d elements", what, ref.n)
		}
		return
	}
	if c.cardinality() != ref.n {
		t.Fatalf("%s: cardinality = %d, want %d", what, c.cardinality(), ref.n)
	}
	// Spot-check membership on a deterministic grid plus randoms.
	for v := uint32(0); v < 65536; v += 7 {
		if c.contains(uint16(v)) != ref.contains(uint16(v)) {
			t.Fatalf("%s: membership mismatch at %d", what, v)
		}
	}
}

func TestContainerOpsDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	kinds := []int{typeArray, typeBitmap, typeRun}
	for trial := 0; trial < 40; trial++ {
		for _, ka := range kinds {
			for _, kb := range kinds {
				ca, ra := randomContainer(rng, ka)
				cb, rb := randomContainer(rng, kb)
				checkAgainstReference(t, containerAnd(ca, cb), ra.and(rb), "AND")
				checkAgainstReference(t, containerOr(ca, cb), ra.or(rb), "OR")
				checkAgainstReference(t, containerXor(ca, cb), ra.xor(rb), "XOR")
			}
		}
	}
}

func TestNormalizeEmpty(t *testing.T) {
	if normalize(&arrayContainer{}) != nil {
		t.Fatal("empty array should normalize to nil")
	}
	if normalize(&bitmapContainer{}) != nil {
		t.Fatal("empty bitmap should normalize to nil")
	}
	if normalize(&runContainer{}) != nil {
		t.Fatal("empty run should normalize to nil")
	}
}
