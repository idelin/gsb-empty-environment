package bitmap

import (
	"math/rand"
	"sort"
	"testing"
)

func fillContainer(c container, vals ...uint16) container {
	for _, v := range vals {
		c, _ = c.add(v)
	}
	return c.optimize()
}

func containerToSet(c container) map[uint16]bool {
	s := make(map[uint16]bool)
	c.forEach(func(x uint16) { s[x] = true })
	return s
}

func TestArrayContainerBasic(t *testing.T) {
	var c container = newArrayContainer()
	c, changed := c.add(7)
	if !changed || !c.contains(7) {
		t.Fatal("add(7) failed")
	}
	if _, changed := c.add(7); changed {
		t.Fatal("duplicate add reported change")
	}
	if c.cardinality() != 1 {
		t.Fatalf("cardinality=%d want 1", c.cardinality())
	}
	if _, ok := c.remove(8); ok {
		t.Fatal("remove(8) should report no change")
	}
	if _, ok := c.remove(7); !ok || c.contains(7) {
		t.Fatal("remove(7) failed")
	}
}

func TestArraySortedInsert(t *testing.T) {
	var c container = newArrayContainer()
	c = fillContainer(c, 9, 3, 5, 1, 7)
	ac := c.(*arrayContainer)
	for i := 1; i < len(ac.data); i++ {
		if ac.data[i] <= ac.data[i-1] {
			t.Fatalf("array not sorted: %v", ac.data)
		}
	}
}

func TestSparseStaysArray(t *testing.T) {
	var c container = newArrayContainer()
	for i := 0; i < 1000; i++ {
		c, _ = c.add(uint16(i * 61)) // sparse, no runs
	}
	c = c.optimize()
	if _, ok := c.(*arrayContainer); !ok {
		t.Fatalf("sparse container became %T, want *arrayContainer", c)
	}
}

func TestDenseRandomBecomesBitmap(t *testing.T) {
	var c container = newArrayContainer()
	// 5000 even numbers: high cardinality, no runs -> bitmap wins.
	for i := 0; i < 5000; i++ {
		c, _ = c.add(uint16(i * 2))
	}
	c = c.optimize()
	if _, ok := c.(*bitmapContainer); !ok {
		t.Fatalf("dense runless container became %T, want *bitmapContainer", c)
	}
	if c.cardinality() != 5000 {
		t.Fatalf("cardinality=%d want 5000", c.cardinality())
	}
	for i := 0; i < 5000; i++ {
		if !c.contains(uint16(i * 2)) {
			t.Fatalf("missing %d", i*2)
		}
	}
}

func TestContiguousBecomesRun(t *testing.T) {
	var c container = newArrayContainer()
	for i := 0; i < 5000; i++ {
		c, _ = c.add(uint16(i))
	}
	c = c.optimize()
	rc, ok := c.(*runContainer)
	if !ok {
		t.Fatalf("contiguous container became %T, want *runContainer", c)
	}
	if len(rc.runs) != 1 || rc.runs[0].start != 0 || rc.runs[0].last() != 4999 {
		t.Fatalf("runs=%+v, want single [0,4999]", rc.runs)
	}
	if c.cardinality() != 5000 {
		t.Fatalf("cardinality=%d want 5000", c.cardinality())
	}
}

func TestRunMerging(t *testing.T) {
	var c container = &runContainer{}
	c = fillContainer(c, 5, 7, 6, 10, 12, 11)
	rc := c.(*runContainer)
	if len(rc.runs) != 2 {
		t.Fatalf("runs=%+v, want [5,7] and [10,12]", rc.runs)
	}
	if rc.runs[0] != (interval{5, 2}) || rc.runs[1] != (interval{10, 2}) {
		t.Fatalf("runs=%+v", rc.runs)
	}
	// Bridge the two runs.
	c, _ = c.add(8)
	c, _ = c.add(9)
	rc = c.optimize().(*runContainer)
	if len(rc.runs) != 1 || rc.runs[0].start != 5 || rc.runs[0].last() != 12 {
		t.Fatalf("after bridging runs=%+v, want single [5,12]", rc.runs)
	}
}

func TestRunRemoveSplits(t *testing.T) {
	var c container = &runContainer{}
	c = fillContainer(c, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	if _, ok := c.remove(5); !ok {
		t.Fatal("remove(5) failed")
	}
	rc := c.optimize().(*runContainer)
	if len(rc.runs) != 2 || rc.runs[0].last() != 4 || rc.runs[1].start != 6 {
		t.Fatalf("runs=%+v, want [0,4] and [6,9]", rc.runs)
	}
	// Shrink edges.
	c, _ = c.remove(0)
	c, _ = c.remove(9)
	c, _ = c.remove(4) // empties first run? no: [1,4] -> [1,3]
	rc = c.optimize().(*runContainer)
	want := map[uint16]bool{1: true, 2: true, 3: true, 6: true, 7: true, 8: true}
	got := containerToSet(c)
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for v := range want {
		if !got[v] {
			t.Fatalf("missing %d: %v", v, got)
		}
	}
}

func TestFullRangeRun(t *testing.T) {
	var c container = &runContainer{}
	c, _ = c.add(0)
	c, _ = c.add(65535)
	c = fillContainer(c, 1, 2, 3)
	// Extend to full range via run add of the middle.
	c = orContainers(c, &runContainer{runs: []interval{{0, 65535}}})
	if c.cardinality() != 65536 {
		t.Fatalf("cardinality=%d want 65536", c.cardinality())
	}
	if !c.contains(0) || !c.contains(65535) || !c.contains(40000) {
		t.Fatal("full range membership wrong")
	}
}

// TestContainerOpsAgainstOracle fuzzes all three container types and all
// three set operations against a plain map oracle.
func TestContainerOpsAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 120; trial++ {
		ca, sa := randomContainer(rng)
		cb, sb := randomContainer(rng)

		andC := andContainers(ca, cb)
		orC := orContainers(ca, cb)
		xorC := xorContainers(ca, cb)

		andS := make(map[uint16]bool)
		orS := make(map[uint16]bool)
		xorS := make(map[uint16]bool)
		for v := range sa {
			orS[v] = true
			if sb[v] {
				andS[v] = true
			} else {
				xorS[v] = true
			}
		}
		for v := range sb {
			orS[v] = true
			if !sa[v] {
				xorS[v] = true
			}
		}
		assertContainerEq(t, "and", andC, andS)
		assertContainerEq(t, "or", orC, orS)
		assertContainerEq(t, "xor", xorC, xorS)

		// Inputs must be immutable across ops (copy-on-write contract).
		assertContainerEq(t, "a unchanged", ca, sa)
		assertContainerEq(t, "b unchanged", cb, sb)
	}
}

func TestContainerRemoveAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		c, s := randomContainer(rng)
		for i := 0; i < 30; i++ {
			v := uint16(rng.Intn(65536))
			c, _ = c.remove(v)
			delete(s, v)
			c = c.optimize()
		}
		assertContainerEq(t, "after removes", c, s)
	}
}

func randomContainer(rng *rand.Rand) (container, map[uint16]bool) {
	s := make(map[uint16]bool)
	var c container = newArrayContainer()
	switch rng.Intn(3) {
	case 0: // sparse random
		for i, n := 0, rng.Intn(300); i < n; i++ {
			s[uint16(rng.Intn(65536))] = true
		}
	case 1: // dense random
		for i, n := 0, 2000+rng.Intn(8000); i < n; i++ {
			s[uint16(rng.Intn(65536))] = true
		}
	case 2: // clustered runs
		for i, n := 0, 1+rng.Intn(8); i < n; i++ {
			start := rng.Intn(60000)
			length := rng.Intn(5000)
			for j := 0; j < length && start+j < 65536; j++ {
				s[uint16(start+j)] = true
			}
		}
	}
	vals := make([]uint16, 0, len(s))
	for v := range s {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	for _, v := range vals {
		c, _ = c.add(v)
	}
	c = c.optimize()
	// Sanity: container must agree with the oracle before the ops.
	assertContainerEq(nil, "build", c, s)
	return c, s
}

func assertContainerEq(t *testing.T, name string, c container, want map[uint16]bool) {
	if c.cardinality() != len(want) {
		if t != nil {
			t.Fatalf("%s: cardinality=%d want %d (type %T)", name, c.cardinality(), len(want), c)
		}
		panic("cardinality mismatch")
	}
	for v := range want {
		if !c.contains(v) {
			if t != nil {
				t.Fatalf("%s: missing %d (type %T)", name, v, c)
			}
			panic("missing value")
		}
	}
	got := containerToSet(c)
	if len(got) != len(want) {
		if t != nil {
			t.Fatalf("%s: forEach yielded %d values, want %d", name, len(got), len(want))
		}
		panic("forEach mismatch")
	}
}
