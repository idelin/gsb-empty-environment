package dtri

import (
	"bytes"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func ip(id string, x, y int64) InputPoint {
	return InputPoint{ID: id, X: big.NewInt(x), Y: big.NewInt(y)}
}

func ipBig(id string, x, y *big.Int) InputPoint {
	return InputPoint{ID: id, X: x, Y: y}
}

func mustTriangulate(t *testing.T, in []InputPoint) *Output {
	t.Helper()
	out, err := Triangulate(in)
	if err != nil {
		t.Fatalf("Triangulate: %v", err)
	}
	if err := Validate(out); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return out
}

func shuffled(in []InputPoint, r *rand.Rand) []InputPoint {
	out := make([]InputPoint, len(in))
	copy(out, in)
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func encode(t *testing.T, out *Output) []byte {
	t.Helper()
	b, err := EncodeOutput(out)
	if err != nil {
		t.Fatalf("EncodeOutput: %v", err)
	}
	return b
}

// TestGridSubsets exhaustively triangulates and validates every subset of a
// 3x3 integer grid (512 subsets, covering collinear runs, cocircular
// quadruples, points on edges, duplicates-free degenerate mixes), and checks
// that a reversed input yields the identical canonical output.
func TestGridSubsets(t *testing.T) {
	var grid []InputPoint
	for x := int64(0); x < 3; x++ {
		for y := int64(0); y < 3; y++ {
			grid = append(grid, ip(fmt.Sprintf("g%d%d", x, y), x, y))
		}
	}
	n := len(grid)
	for mask := 0; mask < (1 << uint(n)); mask++ {
		var sub []InputPoint
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, grid[i])
			}
		}
		out := mustTriangulate(t, sub)
		// Reversed input must give the identical canonical output.
		rev := make([]InputPoint, len(sub))
		for i := range sub {
			rev[i] = sub[len(sub)-1-i]
		}
		out2 := mustTriangulate(t, rev)
		if !bytes.Equal(encode(t, out), encode(t, out2)) {
			t.Fatalf("mask %d: reversed input produced different output", mask)
		}
	}
}

// TestCocircularFour checks the four cocircular points (+-1,0),(0,+-1) under
// all 24 input permutations: the triangulation must be identical and valid.
func TestCocircularFour(t *testing.T) {
	base := []InputPoint{ip("e", 1, 0), ip("n", 0, 1), ip("w", -1, 0), ip("s", 0, -1)}
	perm := []int{0, 1, 2, 3}
	var ref []byte
	count := 0
	var rec func(k int)
	rec = func(k int) {
		if k == len(perm) {
			in := make([]InputPoint, 4)
			for i, p := range perm {
				in[i] = base[p]
			}
			out := mustTriangulate(t, in)
			if out.Status != StatusTriangulated {
				t.Fatalf("perm %v: status %s", perm, out.Status)
			}
			enc := encode(t, out)
			if ref == nil {
				ref = enc
			} else if !bytes.Equal(ref, enc) {
				t.Fatalf("perm %v: non-canonical output", perm)
			}
			count++
			return
		}
		for i := k; i < len(perm); i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
	if count != 24 {
		t.Fatalf("expected 24 permutations, got %d", count)
	}
}

// TestCocircularTwelve uses 12 exactly cocircular integer points
// (x^2+y^2=25) plus an interior point, under random shuffles.
func TestCocircularTwelve(t *testing.T) {
	var base []InputPoint
	pts := [][2]int64{{5, 0}, {-5, 0}, {0, 5}, {0, -5},
		{3, 4}, {3, -4}, {-3, 4}, {-3, -4},
		{4, 3}, {4, -3}, {-4, 3}, {-4, -3},
		{1, 1}} // interior point
	for i, p := range pts {
		base = append(base, ip(fmt.Sprintf("c%d", i), p[0], p[1]))
	}
	ref := encode(t, mustTriangulate(t, base))
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 25; trial++ {
		out := mustTriangulate(t, shuffled(base, r))
		if !bytes.Equal(ref, encode(t, out)) {
			t.Fatalf("trial %d: non-canonical output for shuffled cocircular set", trial)
		}
	}
}

// TestLongCollinearChain inserts 500 collinear points in random order and
// expects a single ordered chain, no triangles.
func TestLongCollinearChain(t *testing.T) {
	const n = 500
	var in []InputPoint
	for i := int64(0); i < n; i++ {
		in = append(in, ip(fmt.Sprintf("p%d", i), 3*i-700, 2*i+5)) // on y=(2x+... )/3 line
	}
	r := rand.New(rand.NewSource(1))
	in = shuffled(in, r)
	out := mustTriangulate(t, in)
	if out.Status != StatusCollinear {
		t.Fatalf("status = %s, want collinear", out.Status)
	}
	if len(out.Chain) != n {
		t.Fatalf("chain length %d, want %d", len(out.Chain), n)
	}
	for i := 1; i < n; i++ {
		prev, cur := out.Points[out.Chain[i-1]], out.Points[out.Chain[i]]
		if prev.X.Cmp(cur.X) >= 0 {
			t.Fatalf("chain not ordered at %d", i)
		}
	}
}

// TestInsertOnEdge places points exactly on triangulation edges and on a
// diagonal, including a point on the convex hull edge.
func TestInsertOnEdge(t *testing.T) {
	in := []InputPoint{
		ip("a", 0, 0), ip("b", 10, 0), ip("c", 10, 10), ip("d", 0, 10),
		ip("e", 5, 0),  // on hull edge a-b
		ip("f", 5, 5),  // on diagonal a-c
		ip("g", 7, 0),  // on hull edge a-b as well
		ip("h", 5, 10), // on hull edge d-c
		ip("i", 3, 3),  // on diagonal a-c
	}
	out := mustTriangulate(t, in)
	if out.Status != StatusTriangulated {
		t.Fatalf("status = %s", out.Status)
	}
	// Every vertex must have degree >= 2 and no zero-area triangles
	// (already covered by Validate); additionally the on-edge points must
	// appear as hull vertices where applicable.
	hullSet := map[int]bool{}
	for _, h := range out.Hull {
		hullSet[h] = true
	}
	for idx, p := range out.Points {
		if p.Y.Sign() == 0 || (p.Y.Cmp(big.NewInt(10)) == 0) {
			if !hullSet[idx] {
				t.Fatalf("boundary point %v not on hull", p)
			}
		}
	}
}

// TestLargeCoordinates uses coordinates around 2^100 where float64 and
// int64 arithmetic would overflow; includes an exactly cocircular set scaled
// to huge coordinates.
func TestLargeCoordinates(t *testing.T) {
	pow := new(big.Int).Lsh(big.NewInt(1), 100) // 2^100
	mk := func(dx, dy int64) (x, y *big.Int) {
		return new(big.Int).Add(new(big.Int).Set(pow), big.NewInt(dx)),
			new(big.Int).Add(new(big.Int).Set(pow), big.NewInt(dy))
	}
	var in []InputPoint
	coords := [][2]int64{{0, 0}, {9, 0}, {0, 9}, {9, 9}, {3, 3}, {6, 7}, {1, 8}}
	for i, c := range coords {
		x, y := mk(c[0], c[1])
		in = append(in, ipBig(fmt.Sprintf("L%d", i), x, y))
	}
	mustTriangulate(t, in)

	// Exactly cocircular at huge scale: (+-C,0),(0,+-C) with C = 3*2^90+1.
	c := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(3), 90), big.NewInt(1))
	neg := new(big.Int).Neg(c)
	zero := big.NewInt(0)
	in2 := []InputPoint{
		ipBig("p1", c, zero), ipBig("p2", neg, zero),
		ipBig("p3", zero, c), ipBig("p4", zero, neg),
		ipBig("p5", big.NewInt(1), big.NewInt(2)),
	}
	ref := encode(t, mustTriangulate(t, in2))
	r := rand.New(rand.NewSource(3))
	for trial := 0; trial < 10; trial++ {
		out := mustTriangulate(t, shuffled(in2, r))
		if !bytes.Equal(ref, encode(t, out)) {
			t.Fatalf("trial %d: non-canonical output at huge scale", trial)
		}
	}
}

// TestDuplicateMerge checks that identical coordinates merge and keep all
// source IDs, sorted, independent of input order.
func TestDuplicateMerge(t *testing.T) {
	in := []InputPoint{
		ip("z1", 1, 1), ip("a2", 0, 0), ip("m3", 1, 1),
		ip("b4", 2, 0), ip("c5", 0, 2), ip("q6", 1, 1),
	}
	out := mustTriangulate(t, in)
	if len(out.Points) != 4 {
		t.Fatalf("got %d distinct points, want 4", len(out.Points))
	}
	found := false
	for i, p := range out.Points {
		if p.X.Cmp(big.NewInt(1)) == 0 && p.Y.Cmp(big.NewInt(1)) == 0 {
			found = true
			want := []string{"m3", "q6", "z1"}
			if len(out.IDs[i]) != 3 || out.IDs[i][0] != want[0] || out.IDs[i][1] != want[1] || out.IDs[i][2] != want[2] {
				t.Fatalf("merged IDs = %v, want %v", out.IDs[i], want)
			}
		}
	}
	if !found {
		t.Fatal("merged point (1,1) not found")
	}
}

// TestFewPoints covers 0, 1, 2 distinct points and all-duplicates input.
func TestFewPoints(t *testing.T) {
	out := mustTriangulate(t, nil)
	if out.Status != StatusEmpty {
		t.Fatalf("status = %s, want empty", out.Status)
	}
	out = mustTriangulate(t, []InputPoint{ip("only", 5, -3)})
	if out.Status != StatusPoint || len(out.Chain) != 1 {
		t.Fatalf("status = %s, want point", out.Status)
	}
	out = mustTriangulate(t, []InputPoint{ip("x", 0, 0), ip("y", 4, 7)})
	if out.Status != StatusSegment || len(out.Chain) != 2 {
		t.Fatalf("status = %s, want segment", out.Status)
	}
	out = mustTriangulate(t, []InputPoint{ip("a", 2, 2), ip("b", 2, 2), ip("c", 2, 2)})
	if out.Status != StatusPoint {
		t.Fatalf("status = %s, want point (all duplicates)", out.Status)
	}
	if len(out.IDs[0]) != 3 {
		t.Fatalf("merged IDs = %v, want 3", out.IDs[0])
	}
}

// TestReproducible generates fixed-seed random integer point sets and checks
// that triangulation+validation succeed and that shuffled inputs reproduce
// the byte-identical canonical output. Re-run any time: same seed, same
// result.
func TestReproducible(t *testing.T) {
	for _, cfg := range []struct {
		seed  int64
		n, ub int64
	}{
		{42, 60, 1000},
		{1234, 120, 100},
		{99, 150, 1000000},
	} {
		r := rand.New(rand.NewSource(cfg.seed))
		var in []InputPoint
		for i := int64(0); i < cfg.n; i++ {
			in = append(in, ip(fmt.Sprintf("r%d", i), r.Int63n(cfg.ub), r.Int63n(cfg.ub)))
		}
		ref := encode(t, mustTriangulate(t, in))
		for trial := 0; trial < 3; trial++ {
			out := mustTriangulate(t, shuffled(in, r))
			if !bytes.Equal(ref, encode(t, out)) {
				t.Fatalf("seed %d trial %d: non-reproducible output", cfg.seed, trial)
			}
		}
	}
}

// TestJSONRoundTrip exercises DecodeInput/EncodeOutput, including big
// coordinates given as JSON strings and numeric IDs.
func TestJSONRoundTrip(t *testing.T) {
	doc := []byte(`{"points": [
		{"id": "A", "x": "170141183460469231731687303715884105728", "y": 0},
		{"id": 7, "x": 0, "y": "170141183460469231731687303715884105728"},
		{"id": "C", "x": -5, "y": -5},
		{"id": "D", "x": 3, "y": 4},
		{"id": "A-dup", "x": "170141183460469231731687303715884105728", "y": 0}
	]}`)
	pts, err := DecodeInput(doc)
	if err != nil {
		t.Fatalf("DecodeInput: %v", err)
	}
	if len(pts) != 5 || pts[1].ID != "7" {
		t.Fatalf("decoded %+v", pts)
	}
	out := mustTriangulate(t, pts)
	enc := encode(t, out)
	if !bytes.Contains(enc, []byte("170141183460469231731687303715884105728")) {
		t.Fatal("big coordinate lost in JSON output")
	}
	if !bytes.Contains(enc, []byte(`"A-dup"`)) {
		t.Fatal("merged duplicate ID missing in JSON output")
	}
}
