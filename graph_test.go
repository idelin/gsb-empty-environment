package cgraph

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func mustBuild(t *testing.T, n uint32, edges []Edge) *Graph {
	t.Helper()
	g, err := Build(n, edges)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return g
}

func TestBasicGraph(t *testing.T) {
	edges := []Edge{
		{0, 1},
		{0, 2}, {0, 2}, {0, 2}, // parallel edge x3
		{1, 2},
		{2, 2}, // self-loop
		{3, 0},
	}
	g := mustBuild(t, 4, edges)

	ids, ms, err := g.Neighbors(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []uint32{1, 2, 3}) || !reflect.DeepEqual(ms, []uint64{1, 3, 1}) {
		t.Fatalf("node 0 neighbors = %v/%v, want [1 2 3]/[1 3 1]", ids, ms)
	}

	// Self-loop preserved on node 2, undirected projection on the rest.
	ids, ms, err = g.Neighbors(2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []uint32{0, 1, 2}) || !reflect.DeepEqual(ms, []uint64{3, 1, 1}) {
		t.Fatalf("node 2 neighbors = %v/%v, want [0 1 2]/[3 1 1]", ids, ms)
	}

	if d, _ := g.Degree(0); d != 5 {
		t.Fatalf("degree(0) = %d, want 5", d)
	}
	if d, _ := g.Degree(2); d != 5 {
		t.Fatalf("degree(2) = %d, want 5 (parallel x3 + edge + self-loop)", d)
	}
	if c, _ := g.DistinctNeighbors(2); c != 3 {
		t.Fatalf("distinct(2) = %d, want 3", c)
	}

	if m, _ := g.Multiplicity(0, 2); m != 3 {
		t.Fatalf("mult(0,2) = %d, want 3", m)
	}
	if m, _ := g.Multiplicity(2, 0); m != 3 {
		t.Fatalf("mult(2,0) = %d, want 3 (undirected)", m)
	}
	if ok, _ := g.HasEdge(2, 2); !ok {
		t.Fatal("self-loop (2,2) lost")
	}
	if ok, _ := g.HasEdge(1, 3); ok {
		t.Fatal("phantom edge (1,3)")
	}

	common, err := g.Intersection(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(common, []uint32{2}) {
		t.Fatalf("intersection(0,1) = %v, want [2]", common)
	}
}

func TestEmptyAndIsolated(t *testing.T) {
	g := mustBuild(t, 3, nil)
	for u := uint32(0); u < 3; u++ {
		if d, _ := g.Degree(u); d != 0 {
			t.Fatalf("degree(%d) = %d, want 0", u, d)
		}
		ids, _, err := g.Neighbors(u)
		if err != nil || len(ids) != 0 {
			t.Fatalf("neighbors(%d) = %v, %v", u, ids, err)
		}
	}
	if ok, _ := g.HasEdge(0, 1); ok {
		t.Fatal("phantom edge in empty graph")
	}
	common, err := g.Intersection(0, 1)
	if err != nil || len(common) != 0 {
		t.Fatalf("intersection = %v, %v", common, err)
	}
}

// Exercises the skip index: degrees well above SkipInterval.
func TestSkipIndexSeek(t *testing.T) {
	const n = 201
	var edges []Edge
	for v := uint32(0); v < n; v++ {
		if v != 100 {
			edges = append(edges, Edge{100, v})
		}
	}
	g := mustBuild(t, n, edges)
	if c, _ := g.DistinctNeighbors(100); c != n-1 {
		t.Fatalf("distinct(100) = %d, want %d", c, n-1)
	}
	for v := uint32(0); v < n; v++ {
		ok, err := g.HasEdge(100, v)
		if err != nil {
			t.Fatal(err)
		}
		if want := v != 100; ok != want {
			t.Fatalf("hasEdge(100,%d) = %v, want %v", v, ok, want)
		}
	}
	if ok, _ := g.HasEdge(100, 500); ok {
		t.Fatal("edge to node outside graph")
	}
}

// Random graph cross-checked against a plain map reference.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 300
	const m = 6000
	ref := make([]map[uint32]uint64, n)
	add := func(u, v uint32) {
		if ref[u] == nil {
			ref[u] = make(map[uint32]uint64)
		}
		ref[u][v]++
	}
	var edges []Edge
	for i := 0; i < m; i++ {
		u, v := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		if rng.Intn(10) == 0 {
			v = u // self-loops
		}
		if rng.Intn(5) == 0 && len(edges) > 0 {
			e := edges[rng.Intn(len(edges))]
			u, v = e.From, e.To // parallel edges
		}
		edges = append(edges, Edge{u, v})
		add(u, v)
		if u != v {
			add(v, u)
		}
	}
	g := mustBuild(t, n, edges)

	for u := uint32(0); u < n; u++ {
		ids, ms, err := g.Neighbors(u)
		if err != nil {
			t.Fatalf("neighbors(%d): %v", u, err)
		}
		if len(ids) != len(ref[u]) {
			t.Fatalf("node %d: got %d neighbors, want %d", u, len(ids), len(ref[u]))
		}
		if !sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] }) {
			t.Fatalf("node %d: neighbors not sorted", u)
		}
		var deg uint64
		for i, v := range ids {
			if ms[i] != ref[u][v] {
				t.Fatalf("mult(%d,%d) = %d, want %d", u, v, ms[i], ref[u][v])
			}
			deg += ms[i]
		}
		if d, _ := g.Degree(u); d != deg {
			t.Fatalf("degree(%d) = %d, want %d", u, d, deg)
		}
	}

	for i := 0; i < 20000; i++ {
		u, v := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		got, err := g.Multiplicity(u, v)
		if err != nil {
			t.Fatal(err)
		}
		if got != ref[u][v] {
			t.Fatalf("mult(%d,%d) = %d, want %d", u, v, got, ref[u][v])
		}
	}

	for i := 0; i < 2000; i++ {
		u, w := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		got, err := g.Intersection(u, w)
		if err != nil {
			t.Fatal(err)
		}
		var want []uint32
		for v := range ref[u] {
			if _, ok := ref[w][v]; ok {
				want = append(want, v)
			}
		}
		sort.Slice(want, func(a, b int) bool { return want[a] < want[b] })
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("intersection(%d,%d) = %v, want %v", u, w, got, want)
		}
	}
}

func TestCorruptDeltaOutOfRange(t *testing.T) {
	// node 0: neighbors 1,3,5 -> header [03 03], records [01 00][02 00][02 00]
	g := mustBuild(t, 8, []Edge{{0, 1}, {0, 3}, {0, 5}})
	g.buf[2] = 100 // first delta now decodes to id 100 >= 8 nodes
	if err := g.Validate(); err == nil {
		t.Fatal("Validate accepted out-of-range delta")
	}
	if _, _, err := g.Neighbors(0); err == nil {
		t.Fatal("Neighbors accepted out-of-range delta")
	}
	if _, err := g.Multiplicity(0, 5); err == nil {
		t.Fatal("Multiplicity accepted out-of-range delta")
	}
}

func TestCorruptZeroDelta(t *testing.T) {
	g := mustBuild(t, 8, []Edge{{0, 1}, {0, 3}, {0, 5}})
	g.buf[4] = 0 // second record delta = 0 -> non-increasing ids
	if err := g.Validate(); err == nil {
		t.Fatal("Validate accepted zero delta")
	}
}

func TestCorruptTruncated(t *testing.T) {
	g := mustBuild(t, 8, []Edge{{0, 1}, {0, 3}, {0, 5}, {2, 7}})
	g.buf = g.buf[:len(g.buf)-1] // drop last record's multiplicity byte
	if err := g.Validate(); err == nil {
		t.Fatal("Validate accepted truncated buffer")
	}
}

func TestCorruptHeaderCount(t *testing.T) {
	g := mustBuild(t, 8, []Edge{{0, 1}, {0, 3}, {0, 5}})
	g.buf[0] = 2 // header claims 2 records, 3 are encoded -> trailing bytes
	if err := g.Validate(); err == nil {
		t.Fatal("Validate accepted header/directory mismatch")
	}
}

func TestBuildRejectsOutOfRangeEdge(t *testing.T) {
	if _, err := Build(4, []Edge{{0, 4}}); err == nil {
		t.Fatal("Build accepted edge referencing node >= numNodes")
	}
}

func TestQueryRejectsOutOfRangeNode(t *testing.T) {
	g := mustBuild(t, 4, []Edge{{0, 1}})
	if _, err := g.Degree(9); err == nil {
		t.Fatal("Degree accepted out-of-range node")
	}
	if _, err := g.Multiplicity(9, 0); err == nil {
		t.Fatal("Multiplicity accepted out-of-range source")
	}
	if m, err := g.Multiplicity(0, 9); err != nil || m != 0 {
		t.Fatalf("Multiplicity(0,9) = %d, %v; want 0, nil", m, err)
	}
	if _, err := g.Intersection(0, 9); err == nil {
		t.Fatal("Intersection accepted out-of-range node")
	}
}

func TestEncodingIsActuallyCompact(t *testing.T) {
	// Clustered ids -> small deltas -> ~2 bytes/record, far below the
	// 4 bytes a raw []uint32 adjacency would cost. Undirected storage
	// doubles the record count; nearly every node is non-empty here.
	const n = 20037
	var edges []Edge
	for i := 0; i < 5000; i++ {
		base := uint32(i * 4)
		for k := uint32(0); k < 40; k++ {
			edges = append(edges, Edge{base, base + k + 1})
		}
	}
	g := mustBuild(t, n, edges)
	var records uint64
	for u := uint32(0); u < n; u++ {
		c, _ := g.DistinctNeighbors(u)
		records += uint64(c)
	}
	perRecord := float64(g.EncodedBytes()) / float64(records)
	t.Logf("encoded bytes/record = %.2f (raw uint32 = 4.00)", perRecord)
	if perRecord >= 3.0 {
		t.Fatalf("encoding not compact: %.2f bytes/record", perRecord)
	}
}
