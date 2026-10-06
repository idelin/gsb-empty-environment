package trajsimp

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

func ratInt(v int64) *big.Rat { return big.NewRat(v, 1) }

func ratFrac(num, den int64) *big.Rat { return big.NewRat(num, den) }

func mustSolve(t *testing.T, pts []Point, thr *big.Rat, mandatory []int) []int {
	t.Helper()
	got, err := MinKeepIndices(pts, thr, mandatory)
	if err != nil {
		t.Fatalf("MinKeepIndices: %v", err)
	}
	return got
}

func TestSegDist2Exact(t *testing.T) {
	cases := []struct {
		name    string
		a, b, p Point
		want    *big.Rat
	}{
		{"on segment", IntPoint(0, 0), IntPoint(4, 0), IntPoint(2, 0), ratInt(0)},
		{"perpendicular", IntPoint(0, 0), IntPoint(6, 0), IntPoint(3, 4), ratInt(16)},
		{"foot beyond b", IntPoint(0, 0), IntPoint(1, 0), IntPoint(3, 4), ratInt(20)},
		{"foot before a", IntPoint(0, 0), IntPoint(1, 0), IntPoint(-1, 0), ratInt(1)},
		{"degenerate segment", IntPoint(2, 2), IntPoint(2, 2), IntPoint(5, 6), ratInt(25)},
		{"point equals endpoint", IntPoint(2, 2), IntPoint(2, 2), IntPoint(2, 2), ratInt(0)},
		{"rational result", IntPoint(0, 0), IntPoint(3, 1), IntPoint(1, 1), ratFrac(2, 5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SegDist2(tc.a, tc.b, tc.p); got.Cmp(tc.want) != 0 {
				t.Fatalf("SegDist2 = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestThresholdBoundaryIsInclusive(t *testing.T) {
	// Apex (3,4) sits at squared distance exactly 16 from segment (0,0)-(6,0).
	pts := IntPoints([][2]int64{{0, 0}, {3, 4}, {6, 0}})
	if got := mustSolve(t, pts, ratInt(16), nil); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("threshold equal to dist^2 must keep 2 points, got %v", got)
	}
	if got := mustSolve(t, pts, ratInt(15), nil); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("threshold below dist^2 must keep 3 points, got %v", got)
	}
}

func TestRationalThresholdExactness(t *testing.T) {
	// dist^2 of (1,1) to segment (0,0)-(3,1) is exactly 2/5.
	pts := IntPoints([][2]int64{{0, 0}, {1, 1}, {3, 1}})
	if got := mustSolve(t, pts, ratFrac(2, 5), nil); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("threshold exactly 2/5 must allow the edge, got %v", got)
	}
	below := new(big.Rat).Sub(ratFrac(2, 5), ratFrac(1, 1000))
	if got := mustSolve(t, pts, below, nil); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("threshold 2/5 - 1/1000 must reject the edge, got %v", got)
	}
}

func TestCollinearZeroThreshold(t *testing.T) {
	pts := IntPoints([][2]int64{{0, 0}, {1, 0}, {2, 0}, {3, 0}})
	if got := mustSolve(t, pts, ratInt(0), nil); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("collinear points with zero threshold, got %v", got)
	}
}

func TestDuplicateCoordinates(t *testing.T) {
	// Runs of identical points collapse: zero threshold still allows skipping.
	pts := IntPoints([][2]int64{{0, 0}, {0, 0}, {0, 0}, {5, 0}})
	if got := mustSolve(t, pts, ratInt(0), nil); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("duplicate coordinates, got %v", got)
	}
	// Degenerate (zero-length) edge: middle point measured to the point.
	zig := IntPoints([][2]int64{{1, 1}, {2, 2}, {1, 1}})
	if got := mustSolve(t, zig, ratInt(2), nil); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("degenerate edge with sufficient threshold, got %v", got)
	}
	if got := mustSolve(t, zig, ratInt(1), nil); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("degenerate edge with tight threshold, got %v", got)
	}
}

func TestMandatoryPoints(t *testing.T) {
	pts := IntPoints([][2]int64{{0, 0}, {1, 0}, {2, 0}, {3, 0}})
	if got := mustSolve(t, pts, ratInt(0), []int{1}); !reflect.DeepEqual(got, []int{0, 1, 3}) {
		t.Fatalf("mandatory point must be kept, got %v", got)
	}
	if got := mustSolve(t, pts, ratInt(0), []int{2, 1, 2}); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("unsorted duplicated mandatory input, got %v", got)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	// With threshold 1 both [0,2,4] and [0,3,4] are legal 3-point
	// simplifications (max intermediate dist^2 is 16/25 on edge (0,3)),
	// while [0,4] (dist^2 2), [0,1,4] (16/13 on edge (1,4)) and every
	// 2-point sequence are illegal. The lexicographically smallest
	// minimum-length sequence must win.
	pts := IntPoints([][2]int64{{0, 0}, {1, 2}, {2, 4}, {3, 4}, {4, 4}})
	if got := mustSolve(t, pts, ratInt(1), nil); !reflect.DeepEqual(got, []int{0, 2, 4}) {
		t.Fatalf("lexicographic tie-break, got %v, want [0 2 4]", got)
	}
}

func TestSingleAndEmpty(t *testing.T) {
	if got := mustSolve(t, IntPoints([][2]int64{{7, 9}}), ratInt(0), nil); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("single point, got %v", got)
	}
	got, err := MinKeepIndices(nil, ratInt(1), nil)
	if err != nil || got != nil {
		t.Fatalf("empty input: got %v, err %v", got, err)
	}
}

func TestInputErrors(t *testing.T) {
	pts := IntPoints([][2]int64{{0, 0}, {1, 1}})
	if _, err := MinKeepIndices(pts, nil, nil); err == nil {
		t.Fatal("nil threshold must error")
	}
	if _, err := MinKeepIndices(pts, ratInt(-1), nil); err == nil {
		t.Fatal("negative threshold must error")
	}
	if _, err := MinKeepIndices(pts, ratInt(1), []int{2}); err == nil {
		t.Fatal("out-of-range mandatory index must error")
	}
	if _, err := MinKeepIndices(pts, ratInt(1), []int{-1}); err == nil {
		t.Fatal("negative mandatory index must error")
	}
}

// TestAgainstBruteForce cross-checks the optimized solver against
// exhaustive subset enumeration on many small random instances, including
// duplicate coordinates, zero thresholds and random mandatory sets.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 3000; iter++ {
		n := 1 + rng.Intn(11)
		coords := make([][2]int64, n)
		for i := range coords {
			coords[i] = [2]int64{int64(rng.Intn(7) - 3), int64(rng.Intn(7) - 3)}
		}
		pts := IntPoints(coords)
		thr := ratFrac(int64(rng.Intn(13)), int64(1+rng.Intn(4)))
		var mandatory []int
		for i := 0; i < n; i++ {
			if rng.Intn(4) == 0 {
				mandatory = append(mandatory, i)
			}
		}

		got, err := MinKeepIndices(pts, thr, mandatory)
		if err != nil {
			t.Fatalf("iter %d: MinKeepIndices error: %v", iter, err)
		}
		want, err := BruteForceMinKeep(pts, thr, mandatory)
		if err != nil {
			t.Fatalf("iter %d: BruteForceMinKeep error: %v", iter, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: pts=%v thr=%v mandatory=%v\ngot  %v\nwant %v",
				iter, coords, thr, mandatory, got, want)
		}
		if !SequenceValid(pts, thr, got) {
			t.Fatalf("iter %d: solver returned invalid sequence %v", iter, got)
		}
	}
}
