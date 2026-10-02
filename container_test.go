package roaring

import (
	"math/rand"
	"testing"
)

func TestArrayToBitmapAutoConversion(t *testing.T) {
	b := New()
	for i := 0; i < arrayMaxSize; i++ {
		b.Add(uint64(i * 2)) // 0,2,4,...,8190: sparse, stays array
	}
	if _, ok := b.cs[0].(*arrayContainer); !ok {
		t.Fatalf("cardinality %d: expected arrayContainer, got %T", arrayMaxSize, b.cs[0])
	}
	b.Add(16382) // 4097th distinct value crosses the threshold
	if _, ok := b.cs[0].(*bitmapContainer); !ok {
		t.Fatalf("cardinality %d: expected bitmapContainer, got %T", arrayMaxSize+1, b.cs[0])
	}
	if got := b.Cardinality(); got != uint64(arrayMaxSize+1) {
		t.Fatalf("cardinality = %d, want %d", got, arrayMaxSize+1)
	}
	for i := 0; i < arrayMaxSize; i++ {
		if !b.Contains(uint64(i * 2)) {
			t.Fatalf("missing value %d after conversion", i*2)
		}
	}
}

func TestDenseRangeBecomesRunContainer(t *testing.T) {
	b := New()
	b.AddRange(0, 65536)
	if _, ok := b.cs[0].(*runContainer); !ok {
		t.Fatalf("expected runContainer for contiguous range, got %T", b.cs[0])
	}
	if !b.cs[0].isFull() {
		t.Fatal("expected full run container")
	}
	if got := b.Cardinality(); got != 65536 {
		t.Fatalf("cardinality = %d, want 65536", got)
	}
}

func TestRunContainerMergingBoundaries(t *testing.T) {
	r := &runContainer{}
	// Build [10,20] and [30,40], then bridge them via adjacency.
	for v := uint16(10); v <= 20; v++ {
		r = r.add(v).(*runContainer)
	}
	for v := uint16(30); v <= 40; v++ {
		r = r.add(v).(*runContainer)
	}
	if len(r.runs) != 2 {
		t.Fatalf("runs = %v, want 2 intervals", r.runs)
	}
	for v := uint16(21); v <= 29; v++ {
		r = r.add(v).(*runContainer)
	}
	if len(r.runs) != 1 || r.runs[0] != (interval{10, 40}) {
		t.Fatalf("runs = %v, want single [10,40]", r.runs)
	}
	// Boundary values 0 and 65535.
	r = &runContainer{}
	r = r.add(1).(*runContainer)
	r = r.add(0).(*runContainer) // prepend at absolute minimum
	r = r.add(65534).(*runContainer)
	r = r.add(65535).(*runContainer) // append at absolute maximum
	if len(r.runs) != 2 || r.runs[0] != (interval{0, 1}) || r.runs[1] != (interval{65534, 65535}) {
		t.Fatalf("boundary runs = %v", r.runs)
	}
	if !r.contains(0) || !r.contains(65535) || r.contains(2) || r.contains(65533) {
		t.Fatal("boundary contains failed")
	}
}

func TestRunAddIntervalMergesOverlaps(t *testing.T) {
	r := &runContainer{}
	r.addInterval(100, 200)
	r.addInterval(300, 400)
	r.addInterval(500, 600)
	r.addInterval(150, 550) // overlaps/adjacent to all three
	if len(r.runs) != 1 || r.runs[0] != (interval{100, 600}) {
		t.Fatalf("runs = %v, want single [100,600]", r.runs)
	}
	r.addInterval(0, 65535) // swallow everything
	if !r.isFull() {
		t.Fatalf("runs = %v, want full run", r.runs)
	}
}

func TestOptimizeChoosesSmallestEncoding(t *testing.T) {
	// Dense random data -> bitmap container stays cheapest.
	b := New()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		b.Add(uint64(rng.Intn(65536)))
	}
	b.Optimize()
	if _, ok := b.cs[0].(*bitmapContainer); !ok {
		t.Fatalf("dense random: expected bitmapContainer, got %T", b.cs[0])
	}
	// Contiguous data -> run container.
	c := New()
	c.AddRange(1000, 5000)
	c.Optimize()
	if _, ok := c.cs[0].(*runContainer); !ok {
		t.Fatalf("contiguous: expected runContainer, got %T", c.cs[0])
	}
	// Few scattered values -> array container.
	d := New()
	for _, v := range []uint64{5, 1000, 40000} {
		d.Add(v)
	}
	d.Optimize()
	if _, ok := d.cs[0].(*arrayContainer); !ok {
		t.Fatalf("sparse: expected arrayContainer, got %T", d.cs[0])
	}
}

func TestContainerOpsCrossType(t *testing.T) {
	// array x run
	a := &arrayContainer{}
	for i := uint16(0); i < 100; i++ {
		a = a.add(i * 10).(*arrayContainer) // 0,10,...,990
	}
	r := &runContainer{runs: []interval{{500, 1500}}}
	got := andContainers(a, r)
	if got.cardinality() != 50 { // 500,510,...,990
		t.Fatalf("array AND run cardinality = %d, want 50", got.cardinality())
	}
	// run x run
	r2 := &runContainer{runs: []interval{{0, 600}}}
	got = andContainers(r, r2)
	if got.cardinality() != 101 { // [500,600]
		t.Fatalf("run AND run cardinality = %d, want 101", got.cardinality())
	}
	// bitmap x run
	bc := r2.toBitmap()
	got = andContainers(bc, r)
	if got.cardinality() != 101 {
		t.Fatalf("bitmap AND run cardinality = %d, want 101", got.cardinality())
	}
	// full-run fast paths
	full := &runContainer{runs: []interval{{0, 0xffff}}}
	if got := andContainers(full, a); got.cardinality() != a.cardinality() {
		t.Fatal("full AND array mismatch")
	}
	if got := orContainers(full, a); !got.isFull() {
		t.Fatal("full OR array should be full")
	}
	// xor cancels duplicates
	x := xorContainers(a, a.clone())
	if x.cardinality() != 0 {
		t.Fatalf("a XOR a cardinality = %d, want 0", x.cardinality())
	}
}

func TestContainerOpsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for iter := 0; iter < 100; iter++ {
		var sets [2]map[uint16]bool
		var cs [2]container
		for k := 0; k < 2; k++ {
			sets[k] = map[uint16]bool{}
			var c container = &arrayContainer{}
			switch iter % 3 {
			case 0: // sparse
				for i := 0; i < rng.Intn(3000); i++ {
					v := uint16(rng.Intn(65536))
					sets[k][v] = true
					c = c.add(v)
				}
			case 1: // dense
				for i := 0; i < 20000; i++ {
					v := uint16(rng.Intn(65536))
					sets[k][v] = true
					c = c.add(v)
				}
			case 2: // runs
				c = &runContainer{}
				for i := 0; i < 5; i++ {
					lo := uint16(rng.Intn(60000))
					hi := lo + uint16(rng.Intn(5000))
					for v := uint32(lo); v <= uint32(hi); v++ {
						sets[k][uint16(v)] = true
					}
					rc := c.(*runContainer)
					rc.addInterval(lo, hi)
					c = rc
				}
			}
			cs[k] = c
			if got := c.cardinality(); got != len(sets[k]) {
				t.Fatalf("iter %d: cardinality = %d, want %d", iter, got, len(sets[k]))
			}
		}
		check := func(c container, want map[uint16]bool, op string) {
			t.Helper()
			if c.cardinality() != len(want) {
				t.Fatalf("iter %d %s: cardinality = %d, want %d", iter, op, c.cardinality(), len(want))
			}
			for v := range want {
				if !c.contains(v) {
					t.Fatalf("iter %d %s: missing %d", iter, op, v)
				}
			}
		}
		and, or, xor := map[uint16]bool{}, map[uint16]bool{}, map[uint16]bool{}
		for v := range sets[0] {
			or[v] = true
			if sets[1][v] {
				and[v] = true
			} else {
				xor[v] = true
			}
		}
		for v := range sets[1] {
			or[v] = true
			if !sets[0][v] {
				xor[v] = true
			}
		}
		check(andContainers(cs[0], cs[1]), and, "AND")
		check(orContainers(cs[0], cs[1]), or, "OR")
		check(xorContainers(cs[0], cs[1]), xor, "XOR")
	}
}
