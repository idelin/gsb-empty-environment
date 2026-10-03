package cbitmap

import (
	"math/rand"
	"sort"
	"testing"
)

// model is a reference implementation used to validate the bitmap.
type model map[uint32]struct{}

func (m model) add(x uint32)      { m[x] = struct{}{} }
func (m model) has(x uint32) bool { _, ok := m[x]; return ok }

func (m model) and(o model) model {
	out := model{}
	for k := range m {
		if o.has(k) {
			out.add(k)
		}
	}
	return out
}

func (m model) or(o model) model {
	out := model{}
	for k := range m {
		out.add(k)
	}
	for k := range o {
		out.add(k)
	}
	return out
}

func (m model) xor(o model) model {
	out := model{}
	for k := range m {
		if !o.has(k) {
			out.add(k)
		}
	}
	for k := range o {
		if !m.has(k) {
			out.add(k)
		}
	}
	return out
}

func (m model) sorted() []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func checkEqual(t *testing.T, bm *Bitmap, m model, ctx string) {
	t.Helper()
	if got := bm.Cardinality(); got != uint64(len(m)) {
		t.Fatalf("%s: cardinality=%d want %d", ctx, got, len(m))
	}
	got := bm.ToArray()
	want := m.sorted()
	if len(got) != len(want) {
		t.Fatalf("%s: iteration produced %d values, want %d", ctx, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: iteration mismatch at %d: got %d want %d", ctx, i, got[i], want[i])
		}
	}
	// spot-check Contains
	for k := range m {
		if !bm.Contains(k) {
			t.Fatalf("%s: Contains(%d)=false", ctx, k)
		}
	}
}

func TestAddBoundaries(t *testing.T) {
	bm := New()
	vals := []uint32{0, 1, 65535, 65536, 65537, 1<<32 - 1, 1 << 31, 1<<32 - 2}
	for _, v := range vals {
		bm.Add(v)
	}
	for _, v := range vals {
		if !bm.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	if bm.Contains(2) || bm.Contains(65534) || bm.Contains(1<<32-3) {
		t.Fatal("unexpected membership")
	}
	if bm.Cardinality() != uint64(len(vals)) {
		t.Fatalf("cardinality=%d", bm.Cardinality())
	}
	// duplicate adds are no-ops
	for _, v := range vals {
		bm.Add(v)
	}
	if bm.Cardinality() != uint64(len(vals)) {
		t.Fatalf("cardinality after dup adds=%d", bm.Cardinality())
	}
}

func TestAddRangeBoundaries(t *testing.T) {
	bm := New()
	bm.AddRange(0, 0)     // empty
	bm.AddRange(100, 100) // empty
	if !bm.IsEmpty() {
		t.Fatal("empty ranges must be no-ops")
	}
	bm.AddRange(0, 1)           // single
	bm.AddRange(65535, 65537)   // crosses container boundary
	bm.Add(1<<32 - 1)           // last possible value
	bm.AddRange(200000, 300000) // spans multiple containers
	for _, v := range []uint32{0, 65535, 65536, 1<<32 - 1, 200000, 250000, 299999} {
		if !bm.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	for _, v := range []uint32{1, 65534, 65537, 199999, 300000, 1<<32 - 2} {
		if bm.Contains(v) {
			t.Fatalf("unexpected %d", v)
		}
	}
	if got, want := bm.Cardinality(), uint64(1+2+1+100000); got != want {
		t.Fatalf("cardinality=%d want %d", got, want)
	}
}

func TestAddRangeFullKeySpace(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1<<32-1) // all but the last value
	bm.Add(1<<32 - 1)
	if got := bm.Cardinality(); got != 1<<32 {
		t.Fatalf("cardinality=%d want 2^32", got)
	}
	for _, v := range []uint32{0, 1, 65535, 65536, 1 << 31, 1<<32 - 1} {
		if !bm.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	// every container should be a single-run container (4 bytes payload)
	st := bm.Stats()
	if st.Containers != 65536 || st.RunContainers != 65536 {
		t.Fatalf("stats=%+v", st)
	}
	if st.PayloadBytes != 65536*4 {
		t.Fatalf("payload=%d want %d", st.PayloadBytes, 65536*4)
	}
}

func TestContainerAutoSelection(t *testing.T) {
	// sparse: array container
	bm := New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		bm.Add(uint32(rng.Intn(65536)))
	}
	if _, ok := bm.cons[0].(*arrayContainer); !ok {
		t.Fatalf("sparse container type = %T, want array", bm.cons[0])
	}
	// exceed 4096 -> bitmap container
	bm2 := New()
	for i := 0; i < 5000; i++ {
		bm2.Add(uint32(i * 7 % 65536))
	}
	if _, ok := bm2.cons[0].(*bitmapContainer); !ok {
		t.Fatalf("dense container type = %T, want bitmap", bm2.cons[0])
	}
	// contiguous range: run container
	bm3 := New()
	bm3.AddRange(10, 60000)
	if _, ok := bm3.cons[0].(*runContainer); !ok {
		t.Fatalf("range container type = %T, want run", bm3.cons[0])
	}
	if bm3.Cardinality() != 59990 {
		t.Fatalf("cardinality=%d", bm3.Cardinality())
	}
}

func TestRunContainerMerging(t *testing.T) {
	bm := New()
	bm.AddRange(100, 200)
	bm.AddRange(300, 400)
	bm.Add(250) // not adjacent
	st := bm.Stats()
	if st.RunContainers != 1 {
		t.Fatalf("runs=%d", st.RunContainers)
	}
	rc := bm.cons[0].(*runContainer)
	if len(rc.runs) != 3 {
		t.Fatalf("run count=%d want 3", len(rc.runs))
	}
	bm.Add(200) // bridges first run and 250? no: 200 adjacent to run end 199
	bm.AddRange(201, 250)
	bm.Add(251)
	bm.AddRange(252, 300) // now 100..399 contiguous except check
	for v := uint32(100); v < 400; v++ {
		if !bm.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	rc = bm.cons[0].(*runContainer)
	if len(rc.runs) != 1 {
		t.Fatalf("run count after merge=%d want 1", len(rc.runs))
	}
}

func TestMixedOpsAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	build := func(seedLabel string) (*Bitmap, model) {
		bm := New()
		m := model{}
		// sparse random ids across many containers
		for i := 0; i < 20000; i++ {
			v := uint32(rng.Intn(1 << 22))
			bm.Add(v)
			m.add(v)
		}
		// dense contiguous ranges
		for i := 0; i < 30; i++ {
			lo := uint32(rng.Intn(1 << 22))
			hi := lo + uint32(rng.Intn(200000))
			bm.AddRange(lo, hi)
			for v := lo; v < hi; v++ {
				m.add(v)
			}
		}
		// very dense random fills to force bitmap containers
		for i := 0; i < 5; i++ {
			base := uint32(rng.Intn(1<<22)) &^ 0xffff
			for j := 0; j < 20000; j++ {
				v := base + uint32(rng.Intn(65536))
				bm.Add(v)
				m.add(v)
			}
		}
		_ = seedLabel
		return bm, m
	}
	a, ma := build("a")
	b, mb := build("b")
	checkEqual(t, a, ma, "a")
	checkEqual(t, b, mb, "b")
	checkEqual(t, And(a, b), ma.and(mb), "and")
	checkEqual(t, Or(a, b), ma.or(mb), "or")
	checkEqual(t, Xor(a, b), ma.xor(mb), "xor")
	// batch ops
	checkEqual(t, AndAll(a, b, a), ma.and(mb), "andall")
	checkEqual(t, OrAll(a, b), ma.or(mb), "orall")
	checkEqual(t, XorAll(a, b, b), ma.xor(mb).xor(mb), "xorall")
}

func TestContainerOpsExhaustiveSmall(t *testing.T) {
	// exhaustive-ish: all container type pairs via small crafted sets
	rng := rand.New(rand.NewSource(99))
	mkSet := func(kind string, seed int64) ([]uint32, model) {
		r := rand.New(rand.NewSource(seed))
		m := model{}
		var vals []uint32
		add := func(v uint32) {
			if !m.has(v) {
				m.add(v)
				vals = append(vals, v)
			}
		}
		switch kind {
		case "array":
			for i := 0; i < 500; i++ {
				add(uint32(r.Intn(65536)))
			}
		case "bitmap":
			for i := 0; i < 30000; i++ {
				add(uint32(r.Intn(65536)))
			}
		case "run":
			lo := uint32(r.Intn(30000))
			for v := lo; v < lo+20000; v++ {
				add(v)
			}
		case "mixed":
			for i := 0; i < 300; i++ {
				add(uint32(r.Intn(65536)))
			}
			lo := uint32(r.Intn(30000))
			for v := lo; v < lo+8000; v++ {
				add(v)
			}
		}
		return vals, m
	}
	kinds := []string{"array", "bitmap", "run", "mixed"}
	for _, ka := range kinds {
		for _, kb := range kinds {
			va, ma := mkSet(ka, 1)
			vb, mb := mkSet(kb, 2)
			ba, bb := New(), New()
			for _, v := range va {
				ba.Add(v)
			}
			for _, v := range vb {
				bb.Add(v)
			}
			ctx := ka + "-" + kb
			checkEqual(t, And(ba, bb), ma.and(mb), ctx+" and")
			checkEqual(t, Or(ba, bb), ma.or(mb), ctx+" or")
			checkEqual(t, Xor(ba, bb), ma.xor(mb), ctx+" xor")
		}
	}
	_ = rng
}

func TestCloneIndependence(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100000)
	for i := 0; i < 5000; i++ {
		bm.Add(uint32(i * 3))
	}
	c := bm.Clone()
	c.Add(1 << 31)
	c.AddRange(500000, 600000)
	if bm.Contains(1 << 31) {
		t.Fatal("clone mutation leaked into original")
	}
	if bm.Cardinality() == c.Cardinality() {
		t.Fatal("cardinalities should diverge")
	}
}

func TestIteratorEmpty(t *testing.T) {
	bm := New()
	it := bm.Iterator()
	if _, ok := it.Next(); ok {
		t.Fatal("empty bitmap iterator must be exhausted")
	}
	if got := bm.ToArray(); len(got) != 0 {
		t.Fatal("empty ToArray")
	}
}
