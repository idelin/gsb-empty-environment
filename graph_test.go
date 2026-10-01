package graphindex

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func buildComplete(t *testing.T, n uint32, edges [][3]uint32) *Graph {
	t.Helper()
	b := NewBuilder(n)
	for _, e := range edges {
		u, v, count := e[0], e[1], e[2]
		for k := uint32(0); k < count; k++ {
			if err := b.AddEdge(u, v); err != nil {
				t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
			}
		}
	}
	g := b.Build()
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return g
}

func TestRoundTripSorted(t *testing.T) {
	g := buildComplete(t, 100, [][3]uint32{
		{0, 5, 1}, {0, 1, 1}, {0, 99, 1}, {0, 42, 1}, {0, 7, 1},
		{1, 0, 1}, {1, 50, 1},
	})
	got, err := g.Neighbors(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{1, 5, 7, 42, 99}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Neighbors(0)=%v, want %v", got, want)
	}
	d, err := g.Degree(0)
	if err != nil || d != 5 {
		t.Fatalf("Degree(0)=%d,%v want 5", d, err)
	}
	d, err = g.Degree(2)
	if err != nil || d != 0 {
		t.Fatalf("Degree(2)=%d,%v want 0", d, err)
	}
}

func TestSelfLoopPreserved(t *testing.T) {
	g := buildComplete(t, 10, [][3]uint32{{3, 3, 1}, {3, 4, 1}})
	got, err := g.Neighbors(3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []uint32{3, 4}) {
		t.Fatalf("Neighbors(3)=%v, want [3 4] (self-loop kept)", got)
	}
	ok, err := g.HasEdge(3, 3)
	if err != nil || !ok {
		t.Fatalf("HasEdge(3,3)=%v,%v want true", ok, err)
	}
}

func TestParallelMultiplicity(t *testing.T) {
	g := buildComplete(t, 10, [][3]uint32{
		{1, 2, 5}, // 5 parallel edges
		{1, 3, 1},
		{1, 1, 3}, // 3 parallel self-loops
	})
	d, _ := g.Degree(1)
	if d != 3 {
		t.Fatalf("Degree(1)=%d, want 3 distinct neighbors", d)
	}
	m, err := g.Multiplicity(1, 2)
	if err != nil || m != 5 {
		t.Fatalf("Multiplicity(1,2)=%d,%v want 5", m, err)
	}
	m, err = g.Multiplicity(1, 1)
	if err != nil || m != 3 {
		t.Fatalf("Multiplicity(1,1)=%d,%v want 3", m, err)
	}
	m, err = g.Multiplicity(1, 3)
	if err != nil || m != 1 {
		t.Fatalf("Multiplicity(1,3)=%d,%v want 1", m, err)
	}
	m, err = g.Multiplicity(1, 9)
	if err != nil || m != 0 {
		t.Fatalf("Multiplicity(1,9)=%d,%v want 0", m, err)
	}
	w, err := g.WeightedDegree(1)
	if err != nil || w != 9 { // 5+1+3
		t.Fatalf("WeightedDegree(1)=%d,%v want 9", w, err)
	}
}

func TestMembershipAcrossBlocks(t *testing.T) {
	// Node 0 gets neighbors 0..199 (25 blocks), verify skip search hits
	// every id and rejects absent ones.
	b := NewBuilder(300)
	for v := uint32(0); v < 200; v++ {
		if err := b.AddEdge(0, v); err != nil {
			t.Fatal(err)
		}
	}
	g := b.Build()
	for v := uint32(0); v < 300; v++ {
		ok, err := g.HasEdge(0, v)
		if err != nil {
			t.Fatal(err)
		}
		if want := v < 200; ok != want {
			t.Fatalf("HasEdge(0,%d)=%v, want %v", v, ok, want)
		}
	}
}

func TestIntersect(t *testing.T) {
	g := buildComplete(t, 50, [][3]uint32{
		{0, 1, 1}, {0, 2, 1}, {0, 3, 1}, {0, 7, 1}, {0, 9, 1},
		{4, 2, 1}, {4, 3, 1}, {4, 9, 1}, {4, 20, 1},
	})
	got, err := g.Intersect(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []uint32{2, 3, 9}) {
		t.Fatalf("Intersect(0,4)=%v, want [2 3 9]", got)
	}
	got, err = g.Intersect(0, 5) // node 5 has no neighbors
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Intersect(0,5)=%v, want empty", got)
	}
}

func TestOutOfRangeQueries(t *testing.T) {
	g := buildComplete(t, 4, [][3]uint32{{0, 1, 1}})
	if _, err := g.Neighbors(4); err == nil {
		t.Fatal("Neighbors(4) should fail")
	}
	if _, err := g.HasEdge(0, 4); err == nil {
		t.Fatal("HasEdge(0,4) should fail")
	}
	if _, err := g.Degree(99); err == nil {
		t.Fatal("Degree(99) should fail")
	}
}

// corruptDelta flips one payload delta byte of node 0 so the cumulative id
// runs past numNodes; decode must reject it.
func TestCorruptDeltaOutOfRangeRejected(t *testing.T) {
	g := buildComplete(t, 16, [][3]uint32{{0, 3, 1}, {0, 5, 1}, {0, 8, 1}})

	// Locate the first payload delta inside node 0's section and inflate it.
	sec := g.section(0)
	// header: deg(1B) nskips(1B) skip absID(1B) skip offset(1B) => payload at 4
	if len(sec) < 5 {
		t.Fatalf("section too short: %v", sec)
	}
	g.data[g.offsets[0]+4] = 0x7f // delta 127 -> id 3+127 >= 16

	if _, err := g.Neighbors(0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Neighbors(0) err=%v, want ErrCorrupt", err)
	}
	if err := g.Validate(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Validate err=%v, want ErrCorrupt", err)
	}
	if _, err := g.HasEdge(0, 5); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("HasEdge err=%v, want ErrCorrupt", err)
	}
}

func TestCorruptSkipAbsIDRejected(t *testing.T) {
	g := buildComplete(t, 10, [][3]uint32{{0, 2, 1}, {0, 4, 1}})
	// skip absID is the 3rd byte of the section (deg, nskips, absID...).
	g.data[g.offsets[0]+2] = 0x7f // absID 127 >= numNodes
	if err := g.Validate(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Validate err=%v, want ErrCorrupt", err)
	}
}

func TestCorruptSkipOffsetRejected(t *testing.T) {
	g := buildComplete(t, 100, [][3]uint32{{0, 2, 1}, {0, 4, 1}, {0, 6, 1}})
	// skip offset is the 4th byte; push it beyond the payload.
	g.data[g.offsets[0]+3] = 0x7f
	if err := g.Validate(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Validate err=%v, want ErrCorrupt", err)
	}
}

func TestCorruptMultiplicityRejected(t *testing.T) {
	g := buildComplete(t, 10, [][3]uint32{{0, 1, 3}})
	// mult section of node 0: count=1, idx=0, extra=2 -> break idx.
	g.mult[g.multOff[0]+1] = 0x7f // idx 127 >= degree 1
	if err := g.Validate(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Validate err=%v, want ErrCorrupt", err)
	}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 500
	const edges = 20000
	ref := make(map[uint32]map[uint32]uint32)
	b := NewBuilder(n)
	for i := 0; i < edges; i++ {
		u, v := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		if err := b.AddEdge(u, v); err != nil {
			t.Fatal(err)
		}
		m := ref[u]
		if m == nil {
			m = make(map[uint32]uint32)
			ref[u] = m
		}
		m[v]++
	}
	g := b.Build()
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for u := uint32(0); u < n; u++ {
		// neighbors + degree
		got, err := g.Neighbors(u)
		if err != nil {
			t.Fatal(err)
		}
		var want []uint32
		for v := range ref[u] {
			want = append(want, v)
		}
		sort32(want)
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("node %d neighbors mismatch", u)
		}
		d, _ := g.Degree(u)
		if int(d) != len(want) {
			t.Fatalf("node %d degree %d, want %d", u, d, len(want))
		}
		// membership + multiplicity for a sample of targets
		for _, v := range sampleTargets(rng, n, 8) {
			ok, err := g.HasEdge(u, v)
			if err != nil {
				t.Fatal(err)
			}
			cnt := ref[u][v]
			if ok != (cnt > 0) {
				t.Fatalf("HasEdge(%d,%d)=%v, want %v", u, v, ok, cnt > 0)
			}
			m, err := g.Multiplicity(u, v)
			if err != nil {
				t.Fatal(err)
			}
			if m != cnt {
				t.Fatalf("Multiplicity(%d,%d)=%d, want %d", u, v, m, cnt)
			}
		}
	}
	// intersection cross-check on sampled pairs
	for k := 0; k < 200; k++ {
		u, v := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		got, err := g.Intersect(u, v)
		if err != nil {
			t.Fatal(err)
		}
		want := intersectRef(ref[u], ref[v])
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Intersect(%d,%d) mismatch", u, v)
		}
	}
	// stats sanity: total equals sum of parts and real buffer sizes
	st := g.Stats()
	if st.TotalBytes != st.OffsetBytes+st.HeaderBytes+st.SkipBytes+st.PayloadBytes+st.MultBytes {
		t.Fatalf("stats total inconsistent: %+v", st)
	}
	if st.PayloadBytes+st.SkipBytes+st.HeaderBytes != uint64(len(g.data)) {
		t.Fatalf("data bytes mismatch: %+v vs len(data)=%d", st, len(g.data))
	}
	if st.MultBytes != uint64(len(g.mult)) {
		t.Fatalf("mult bytes mismatch")
	}
	if st.TotalBytes >= st.NaiveBytes {
		t.Fatalf("compressed %d bytes not smaller than naive %d", st.TotalBytes, st.NaiveBytes)
	}
	t.Logf("stats: %+v", st)
}

func sort32(s []uint32) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sampleTargets(rng *rand.Rand, n uint32, k int) []uint32 {
	out := make([]uint32, k)
	for i := range out {
		out[i] = uint32(rng.Intn(int(n)))
	}
	return out
}

func intersectRef(a, b map[uint32]uint32) []uint32 {
	var out []uint32
	for v := range a {
		if b[v] > 0 {
			out = append(out, v)
		}
	}
	sort32(out)
	return out
}
