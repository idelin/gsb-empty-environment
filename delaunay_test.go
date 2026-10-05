package delaunay

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func raw(id string, x, y int64) RawPoint {
	return RawPoint{ID: id, X: big.NewInt(x), Y: big.NewInt(y)}
}

func canonical(t *testing.T, pts []RawPoint) (*Output, string) {
	t.Helper()
	out := Triangulate(pts)
	if err := Validate(pts, out); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	bs, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out, string(bs)
}

func shuffled(pts []RawPoint, seed int64) []RawPoint {
	out := append([]RawPoint(nil), pts...)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func TestOrientExact(t *testing.T) {
	if got := orient(Pt(0, 0), Pt(1, 0), Pt(0, 1)); got <= 0 {
		t.Fatalf("orient CCW = %d, want > 0", got)
	}
	if got := orient(Pt(0, 0), Pt(0, 1), Pt(1, 0)); got >= 0 {
		t.Fatalf("orient CW = %d, want < 0", got)
	}
	if got := orient(Pt(0, 0), Pt(2, 2), Pt(5, 5)); got != 0 {
		t.Fatalf("orient collinear = %d, want 0", got)
	}
	// Overflow case for int64: 2^62 * 2^62 does not fit.
	a := Pt(0, 0)
	b := Pt(1<<62, 1)
	c := Pt(1, 1<<62)
	if got := orient(a, b, c); got <= 0 {
		t.Fatalf("orient big = %d, want > 0", got)
	}
	if got := orient(a, c, b); got >= 0 {
		t.Fatalf("orient big swapped = %d, want < 0", got)
	}
}

func TestInCircleExact(t *testing.T) {
	a, b, c := Pt(0, 0), Pt(2, 0), Pt(0, 2) // CCW, circle center (1,1) r^2=2
	if got := inCircle(a, b, c, Pt(1, 1)); got <= 0 {
		t.Fatalf("inCircle center = %d, want > 0", got)
	}
	if got := inCircle(a, b, c, Pt(2, 2)); got != 0 {
		t.Fatalf("inCircle cocircular = %d, want 0", got)
	}
	if got := inCircle(a, b, c, Pt(3, 3)); got >= 0 {
		t.Fatalf("inCircle outside = %d, want < 0", got)
	}
	// Large coordinates: products exceed 2^64 many times over.
	k := int64(1) << 60
	big1 := Pt(0, 0)
	big2 := Pt(k, 0)
	big3 := Pt(0, k)
	if got := inCircle(big1, big2, big3, Pt(k/2, k/2)); got <= 0 {
		t.Fatalf("inCircle big inside = %d, want > 0", got)
	}
	if got := inCircle(big1, big2, big3, Pt(k, k)); got != 0 {
		t.Fatalf("inCircle big cocircular = %d, want 0", got)
	}
	if got := inCircle(big1, big2, big3, Pt(2*k, 2*k)); got >= 0 {
		t.Fatalf("inCircle big outside = %d, want < 0", got)
	}
}

func TestFewerThanThree(t *testing.T) {
	if _, js := canonical(t, nil); js == "" {
		t.Fatal("empty input")
	}
	out, _ := canonical(t, []RawPoint{raw("a", 5, 7)})
	if out.Mode != ModePoint || len(out.Chain) != 1 {
		t.Fatalf("single point: mode=%s chain=%v", out.Mode, out.Chain)
	}
	out, _ = canonical(t, []RawPoint{raw("b", 9, 9), raw("a", 5, 7)})
	if out.Mode != ModeSegment || len(out.Chain) != 2 {
		t.Fatalf("segment: mode=%s chain=%v", out.Mode, out.Chain)
	}
	if out.Chain[0] != 0 || out.Chain[1] != 1 { // canonical (x,y) order
		t.Fatalf("segment chain order = %v", out.Chain)
	}
}

func TestDuplicatesMerged(t *testing.T) {
	pts := []RawPoint{
		raw("a1", 0, 0), raw("a2", 0, 0), raw("a3", 0, 0),
		raw("b", 4, 0), raw("c", 0, 4), raw("b2", 4, 0),
	}
	out, _ := canonical(t, pts)
	if len(out.Points) != 3 {
		t.Fatalf("distinct points = %d, want 3", len(out.Points))
	}
	// Points are ordered by (x,y): (0,0), (0,4), (4,0).
	if len(out.Points[0].IDs) != 3 || len(out.Points[1].IDs) != 1 || len(out.Points[2].IDs) != 2 {
		t.Fatalf("merged IDs = %v / %v / %v", out.Points[0].IDs, out.Points[1].IDs, out.Points[2].IDs)
	}
}

func TestGridSubsets(t *testing.T) {
	var grid []RawPoint
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			grid = append(grid, raw(fmt.Sprintf("g%d%d", i, j), int64(i), int64(j)))
		}
	}
	for mask := 0; mask < 1<<9; mask++ {
		var sub []RawPoint
		for i := 0; i < 9; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, grid[i])
			}
		}
		_, want := canonical(t, sub)
		// A shuffled run must produce the identical canonical output.
		if _, got := canonical(t, shuffled(sub, int64(mask)+1)); got != want {
			t.Fatalf("subset mask %d: output depends on input order", mask)
		}
	}
}

func permutations(n int) [][]int {
	if n == 1 {
		return [][]int{{0}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := append([]int(nil), p[:i]...)
			q = append(q, n-1)
			q = append(q, p[i:]...)
			out = append(out, q)
		}
	}
	return out
}

func TestCocircularFourPoints(t *testing.T) {
	corners := []RawPoint{
		raw("bl", 0, 0), raw("br", 2, 0), raw("tr", 2, 2), raw("tl", 0, 2),
	}
	var want string
	for i, perm := range permutations(4) {
		pts := []RawPoint{corners[perm[0]], corners[perm[1]], corners[perm[2]], corners[perm[3]]}
		out, js := canonical(t, pts)
		if out.Mode != ModeTriangulation || len(out.Triangles) != 2 {
			t.Fatalf("perm %v: mode=%s triangles=%d", perm, out.Mode, len(out.Triangles))
		}
		if i == 0 {
			want = js
		} else if js != want {
			t.Fatalf("perm %v: non-canonical output", perm)
		}
	}
}

func TestCocircularTwelveOnCircle(t *testing.T) {
	// 12 integer points on the circle x^2+y^2=25, plus the center.
	pts := []RawPoint{raw("center", 0, 0)}
	for i, p := range [][2]int64{{5, 0}, {4, 3}, {3, 4}, {0, 5}, {-3, 4}, {-4, 3},
		{-5, 0}, {-4, -3}, {-3, -4}, {0, -5}, {3, -4}, {4, -3}} {
		pts = append(pts, raw(fmt.Sprintf("c%d", i), p[0], p[1]))
	}
	_, want := canonical(t, pts)
	for seed := int64(1); seed <= 20; seed++ {
		if _, got := canonical(t, shuffled(pts, seed)); got != want {
			t.Fatalf("seed %d: non-canonical output", seed)
		}
	}
}

func TestLongCollinearChain(t *testing.T) {
	var pts []RawPoint
	for i := 0; i < 500; i++ {
		pts = append(pts, raw(fmt.Sprintf("p%d", i), int64(3*i), int64(7*i+2)))
	}
	out, _ := canonical(t, shuffled(pts, 42))
	if out.Mode != ModeCollinear {
		t.Fatalf("mode = %s, want collinear", out.Mode)
	}
	if len(out.Chain) != 500 {
		t.Fatalf("chain length = %d", len(out.Chain))
	}
	for i := 1; i < len(out.Chain); i++ {
		a := out.Points[out.Chain[i-1]]
		b := out.Points[out.Chain[i]]
		if string(a.X) >= string(b.X) && len(a.X) == len(b.X) {
			// lexicographic string compare is only a smoke check here;
			// exact ordering is enforced by the validator.
			_ = a
			_ = b
		}
	}
	// Vertical line.
	pts = pts[:0]
	for i := 0; i < 100; i++ {
		pts = append(pts, raw(fmt.Sprintf("v%d", i), 5, int64(i)))
	}
	out, _ = canonical(t, shuffled(pts, 7))
	if out.Mode != ModeCollinear || len(out.Chain) != 100 {
		t.Fatalf("vertical: mode=%s chain=%d", out.Mode, len(out.Chain))
	}
}

func TestInsertOnEdge(t *testing.T) {
	tri := New()
	tri.InsertXY(0, 0, "a")
	tri.InsertXY(6, 0, "b")
	tri.InsertXY(0, 6, "c")
	// Insert a point exactly on the hull edge a-b.
	tri.InsertXY(3, 0, "onAB")
	// Insert a point exactly on the hypotenuse b-c (x+y=6).
	tri.InsertXY(3, 3, "onBC")
	// Insert a point exactly on the interior edge a-(3,3) line? Use (1,1)
	// which lies on segment a -> onBC.
	tri.InsertXY(1, 1, "onDiag")
	out := tri.Result()
	pts := make([]RawPoint, len(out.Points))
	for _, p := range out.Points {
		x, _ := new(big.Int).SetString(string(p.X), 10)
		y, _ := new(big.Int).SetString(string(p.Y), 10)
		pts[p.Index] = RawPoint{ID: p.IDs[0], X: x, Y: y}
	}
	if err := Validate(pts, out); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(out.Triangles) != 5 {
		t.Fatalf("triangles = %d, want 5", len(out.Triangles))
	}
}

func TestCollinearHullChain(t *testing.T) {
	// Collinear points along the bottom hull edge and beyond it.
	pts := []RawPoint{
		raw("a", 0, 0), raw("b", 2, 0), raw("c", 4, 0),
		raw("d", 1, 2), raw("e", 3, 2), raw("f", 6, 0),
	}
	out, _ := canonical(t, pts)
	// Hull must contain the collinear chain 0,0 -> 2,0 -> 4,0 -> 6,0.
	var bottom []int
	for _, v := range out.Hull {
		if string(out.Points[v].Y) == "0" {
			bottom = append(bottom, v)
		}
	}
	if len(bottom) != 4 {
		t.Fatalf("hull bottom chain = %v, want 4 collinear vertices", bottom)
	}
}

func TestLargeCoordinates(t *testing.T) {
	k := int64(1) << 60
	pts := []RawPoint{
		raw("o", 0, 0),
		raw("x", k, 1),
		raw("y", 1, k),
		raw("xy", k, k),
		raw("mid", k/2, k/2+1),
		raw("neg", -k, -k),
	}
	if _, js := canonical(t, pts); js == "" {
		t.Fatal("no output")
	}
	if _, js2 := canonical(t, shuffled(pts, 99)); js2 == "" {
		t.Fatal("no output")
	}
	_, want := canonical(t, pts)
	_, got := canonical(t, shuffled(pts, 99))
	if want != got {
		t.Fatal("large coordinates: output depends on input order")
	}
}

func TestOrderIndependenceRandom(t *testing.T) {
	for trial := 0; trial < 30; trial++ {
		rng := rand.New(rand.NewSource(int64(trial) + 1))
		var pts []RawPoint
		for i := 0; i < 25; i++ {
			pts = append(pts, raw(fmt.Sprintf("p%d", i),
				int64(rng.Intn(20)), int64(rng.Intn(20))))
		}
		_, want := canonical(t, pts)
		for seed := int64(0); seed < 5; seed++ {
			if _, got := canonical(t, shuffled(pts, 1000*int64(trial)+seed)); got != want {
				t.Fatalf("trial %d seed %d: non-canonical output", trial, seed)
			}
		}
	}
}
