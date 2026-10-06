// Package polysimp computes minimum-vertex polyline simplifications under an
// exact squared-distance tolerance, honoring mandatory retained points.
//
// A simplification is a subsequence of the input polyline that always keeps
// the first and last vertices plus every mandatory vertex. An edge of the
// simplification is legal only when the exact squared distance from every
// skipped intermediate vertex to the edge segment does not exceed a rational
// threshold. Among all simplifications using the fewest retained vertices,
// Simplify returns the one whose index sequence is lexicographically
// smallest. All distance arithmetic is exact (math/big.Rat), and duplicate
// coordinates are handled naturally.
package polysimp

import (
	"math/big"
)

// Point is a vertex of the input polyline with integer coordinates.
type Point struct {
	X, Y int64
}

func rat(x int64) *big.Rat { return new(big.Rat).SetInt64(x) }

func mul(x, y *big.Rat) *big.Rat { return new(big.Rat).Mul(x, y) }

// SegDistSq returns the exact squared Euclidean distance from p to the
// closed segment ab. A degenerate segment (a == b) reduces to the squared
// distance to that single endpoint.
func SegDistSq(p, a, b Point) *big.Rat {
	ax, ay := rat(a.X), rat(a.Y)
	bx, by := rat(b.X), rat(b.Y)
	px, py := rat(p.X), rat(p.Y)

	dist2 := func(qx, qy *big.Rat) *big.Rat {
		ex := new(big.Rat).Sub(px, qx)
		ey := new(big.Rat).Sub(py, qy)
		return new(big.Rat).Add(mul(ex, ex), mul(ey, ey))
	}

	dx := new(big.Rat).Sub(bx, ax)
	dy := new(big.Rat).Sub(by, ay)
	len2 := new(big.Rat).Add(mul(dx, dx), mul(dy, dy))
	if len2.Sign() == 0 {
		return dist2(ax, ay)
	}
	// Projection parameter t = ((p-a)·(b-a)) / |b-a|^2.
	wx := new(big.Rat).Sub(px, ax)
	wy := new(big.Rat).Sub(py, ay)
	t := new(big.Rat).Quo(new(big.Rat).Add(mul(wx, dx), mul(wy, dy)), len2)
	if t.Sign() <= 0 {
		return dist2(ax, ay)
	}
	if t.Cmp(big.NewRat(1, 1)) >= 0 {
		return dist2(bx, by)
	}
	qx := new(big.Rat).Add(ax, mul(t, dx))
	qy := new(big.Rat).Add(ay, mul(t, dy))
	return dist2(qx, qy)
}

// EdgeValid reports whether every vertex strictly between indices i and j
// lies within the squared-distance threshold of the segment pts[i]-pts[j].
// Adjacent indices are always valid. A nil threshold is treated as zero.
func EdgeValid(pts []Point, i, j int, threshold *big.Rat) bool {
	if threshold == nil {
		threshold = new(big.Rat)
	}
	if i > j {
		i, j = j, i
	}
	for k := i + 1; k < j; k++ {
		if SegDistSq(pts[k], pts[i], pts[j]).Cmp(threshold) > 0 {
			return false
		}
	}
	return true
}

// Simplify returns the indices of the retained vertices as an increasing
// sequence. The result always contains 0, n-1 and every mandatory index,
// uses the minimum possible number of vertices, and is lexicographically
// smallest among all minimum-cardinality solutions. It returns nil for an
// empty polyline and panics on an out-of-range mandatory index.
//
// The algorithm is exact: it evaluates every candidate edge with rational
// arithmetic (O(n^3) exact operations) and solves a shortest-path DP, so it
// is not a greedy farthest-point heuristic.
func Simplify(pts []Point, mandatory []int, threshold *big.Rat) []int {
	n := len(pts)
	if n == 0 {
		return nil
	}
	if threshold == nil {
		threshold = new(big.Rat)
	}

	keep := make([]bool, n)
	keep[0] = true
	keep[n-1] = true
	for _, m := range mandatory {
		if m < 0 || m >= n {
			panic("polysimp: mandatory index out of range")
		}
		keep[m] = true
	}
	// pref[i] counts mandatory vertices among indices [0, i).
	pref := make([]int, n+1)
	for i := 0; i < n; i++ {
		pref[i+1] = pref[i]
		if keep[i] {
			pref[i+1]++
		}
	}
	// mandatoryBetween counts mandatory vertices strictly between i and j.
	mandatoryBetween := func(i, j int) int { return pref[j] - pref[i+1] }

	// valid[i][j] for i < j: edge legality under the distance threshold.
	valid := make([][]bool, n)
	for i := range valid {
		valid[i] = make([]bool, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			valid[i][j] = EdgeValid(pts, i, j, threshold)
		}
	}

	// dp[i] = minimum number of edges of a valid simplification of pts[i:].
	// An edge i->j may not skip a mandatory vertex.
	const inf = int(^uint(0) >> 1)
	dp := make([]int, n)
	dp[n-1] = 0
	for i := n - 2; i >= 0; i-- {
		dp[i] = inf
		for j := i + 1; j < n; j++ {
			if !valid[i][j] || mandatoryBetween(i, j) > 0 || dp[j] == inf {
				continue
			}
			if dp[j]+1 < dp[i] {
				dp[i] = dp[j] + 1
			}
		}
	}
	if dp[0] == inf {
		return nil // unreachable: consecutive edges are always legal
	}

	// Lexicographically smallest reconstruction: at each step take the
	// smallest next index that still admits an optimal completion.
	seq := []int{0}
	for cur := 0; cur != n-1; {
		for j := cur + 1; j < n; j++ {
			if valid[cur][j] && mandatoryBetween(cur, j) == 0 && dp[j] == dp[cur]-1 {
				seq = append(seq, j)
				cur = j
				break
			}
		}
	}
	return seq
}
