package bitmapckpt

import (
	"bytes"
	"math/rand"
	"sort"
	"testing"
)

// makeSet builds a random uint16 set mixing sparse points and dense runs.
func makeSet(r *rand.Rand, sparseN, runsN, maxRunLen int) map[uint16]bool {
	s := make(map[uint16]bool)
	for i := 0; i < sparseN; i++ {
		s[uint16(r.Intn(65536))] = true
	}
	for i := 0; i < runsN; i++ {
		start := r.Intn(65536)
		length := r.Intn(maxRunLen)
		for j := 0; j < length && start+j < 65536; j++ {
			s[uint16(start+j)] = true
		}
	}
	return s
}

func setToArray(s map[uint16]bool) *arrayContainer {
	a := &arrayContainer{}
	for v := range s {
		a.data = append(a.data, v)
	}
	sort.Slice(a.data, func(i, j int) bool { return a.data[i] < a.data[j] })
	return a
}

func setToBitmap(s map[uint16]bool) *bitmapContainer {
	return setToArray(s).toBitmap()
}

func setToRun(s map[uint16]bool) *runContainer {
	return setToArray(s).toRun()
}

func containerToSet(c container) map[uint16]bool {
	s := make(map[uint16]bool)
	c.foreach(func(v uint16) bool {
		s[v] = true
		return true
	})
	return s
}

func setsEqual(a, b map[uint16]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if !b[v] {
			return false
		}
	}
	return true
}

// TestContainerOpsAllCombos checks AND/OR/XOR for every pair of container
// representations against a naive reference set.
func TestContainerOpsAllCombos(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	builders := map[string]func(map[uint16]bool) container{
		"array":  func(s map[uint16]bool) container { return setToArray(s) },
		"bitmap": func(s map[uint16]bool) container { return setToBitmap(s) },
		"run":    func(s map[uint16]bool) container { return setToRun(s) },
	}
	ops := map[string]struct {
		ref func(a, b map[uint16]bool) map[uint16]bool
		fn  func(a, b container) container
	}{
		"and": {
			ref: func(a, b map[uint16]bool) map[uint16]bool {
				out := make(map[uint16]bool)
				for v := range a {
					if b[v] {
						out[v] = true
					}
				}
				return out
			},
			fn: andContainers,
		},
		"or": {
			ref: func(a, b map[uint16]bool) map[uint16]bool {
				out := make(map[uint16]bool)
				for v := range a {
					out[v] = true
				}
				for v := range b {
					out[v] = true
				}
				return out
			},
			fn: orContainers,
		},
		"xor": {
			ref: func(a, b map[uint16]bool) map[uint16]bool {
				out := make(map[uint16]bool)
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
			},
			fn: xorContainers,
		},
	}
	for trial := 0; trial < 10; trial++ {
		setA := makeSet(r, trial*37%1500, trial%6, 1+trial*101%4000)
		setB := makeSet(r, trial*53%1500, trial%5, 1+trial*211%4000)
		for nameA, buildA := range builders {
			for nameB, buildB := range builders {
				for opName, op := range ops {
					got := containerToSet(op.fn(buildA(setA), buildB(setB)))
					want := op.ref(setA, setB)
					if !setsEqual(got, want) {
						t.Fatalf("trial %d %s %s %s: got %d values, want %d",
							trial, nameA, opName, nameB, len(got), len(want))
					}
				}
			}
		}
	}
}

func TestArrayConvertsToBitmap(t *testing.T) {
	var c container = newArrayContainer()
	for i := 0; i < arrayMaxSize; i++ {
		c = c.add(uint16(i * 7 % 65536))
	}
	if c.typeID() != typeArray {
		t.Fatalf("expected array at %d elements, got %d", c.cardinality(), c.typeID())
	}
	for i := 0; i < 5000; i++ {
		c = c.add(uint16(i))
	}
	if c.typeID() != typeBitmap {
		t.Fatalf("expected bitmap after exceeding %d, got %d", arrayMaxSize, c.typeID())
	}
}

func TestBitmapConvertsToFullRun(t *testing.T) {
	b := &bitmapContainer{}
	var c container = b
	for i := 0; i < 65536; i++ {
		c = c.add(uint16(i))
	}
	if c.typeID() != typeRun {
		t.Fatalf("expected full container to become run, got %d", c.typeID())
	}
	if c.cardinality() != 65536 {
		t.Fatalf("cardinality = %d, want 65536", c.cardinality())
	}
}

func TestRunMergesAdjacentAndOverlapping(t *testing.T) {
	rc := &runContainer{}
	var c container = rc
	c = c.addRange(10, 20)
	c = c.addRange(21, 30) // adjacent: merges
	c = c.addRange(15, 25) // overlapping: merges
	c = c.addRange(100, 200)
	got := c.(*runContainer)
	if len(got.runs) != 2 {
		t.Fatalf("expected 2 runs, got %v", got.runs)
	}
	if got.runs[0] != (interval{10, 30}) || got.runs[1] != (interval{100, 200}) {
		t.Fatalf("unexpected runs %v", got.runs)
	}
	if got.card != 21+101 {
		t.Fatalf("card = %d, want %d", got.card, 21+101)
	}
}

func TestRunSingletonExplosionConvertsToBitmap(t *testing.T) {
	var c container = &runContainer{}
	for i := 0; i < runMaxCount+10; i++ {
		c = c.add(uint16(i * 31 % 65536))
	}
	if c.typeID() != typeBitmap {
		t.Fatalf("expected conversion to bitmap with many singleton runs, got %d", c.typeID())
	}
}

func TestRunFlipRange(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 80; trial++ {
		ref := make(map[uint16]bool)
		rc := &runContainer{}
		// random initial content
		for i := 0; i < 5; i++ {
			lo := uint16(r.Intn(65536))
			hi := lo + uint16(r.Intn(2000))
			if hi < lo {
				hi = 0xffff
			}
			rc.addRange(lo, hi)
			for v := uint32(lo); v <= uint32(hi); v++ {
				ref[uint16(v)] = true
			}
		}
		// flip a random range
		lo := uint16(r.Intn(65536))
		hi := lo + uint16(r.Intn(10000))
		if hi < lo {
			hi = 0xffff
		}
		rc.flipRange(lo, hi)
		for v := uint32(lo); v <= uint32(hi); v++ {
			ref[uint16(v)] = !ref[uint16(v)]
			if !ref[uint16(v)] {
				delete(ref, uint16(v))
			}
		}
		got := containerToSet(rc)
		if !setsEqual(got, ref) {
			t.Fatalf("trial %d: flipRange mismatch: got %d, want %d", trial, len(got), len(ref))
		}
		if rc.card != len(ref) {
			t.Fatalf("trial %d: card = %d, want %d", trial, rc.card, len(ref))
		}
	}
}

func TestNormalizeRunPrefersArrayWhenSparse(t *testing.T) {
	rc := &runContainer{runs: []interval{{1, 1}, {100, 100}, {5000, 5001}}, card: 4}
	c := normalizeRun(rc)
	if c.typeID() != typeArray {
		t.Fatalf("expected array for tiny cardinality, got %d", c.typeID())
	}
}

func TestContainerRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	sets := []map[uint16]bool{
		makeSet(r, 10, 0, 0),
		makeSet(r, 5000, 0, 0),
		makeSet(r, 0, 3, 15000),
		makeSet(r, 100, 5, 5000),
		{},
	}
	for i, s := range sets {
		for _, c := range []container{setToArray(s), setToBitmap(s), setToRun(s)} {
			var buf bytes.Buffer
			if err := c.writeTo(&buf); err != nil {
				t.Fatal(err)
			}
			back, err := readContainer(c.typeID(), &buf)
			if err != nil {
				t.Fatal(err)
			}
			if !setsEqual(containerToSet(back), s) {
				t.Fatalf("set %d type %d: round trip mismatch", i, c.typeID())
			}
			if back.cardinality() != len(s) {
				t.Fatalf("set %d type %d: card = %d, want %d", i, c.typeID(), back.cardinality(), len(s))
			}
		}
	}
}
