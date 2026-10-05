// Package polysimp simplifies polylines with a guaranteed bound on the
// discrete Fréchet distance between the original and the simplified
// polyline. The first and last points are always preserved.
//
// The guarantee is stated explicitly in terms of the discrete Fréchet
// distance: it is NOT a perpendicular-distance (Hausdorff-style) proxy.
// Every result carries a monotone matching as verifiable evidence.
package polysimp

import (
	"errors"
	"fmt"
	"math"
)

// Point is a 2D point. Polylines are []Point.
type Point struct {
	X, Y float64
}

// Dist returns the Euclidean distance between a and b.
func Dist(a, b Point) float64 {
	return math.Hypot(a.X-b.X, a.Y-b.Y)
}

// MatchPair records that p[P] is matched to q[Q] in a matching between two
// polylines p and q.
type MatchPair struct {
	P, Q int
}

// DiscreteFrechet computes the discrete Fréchet distance between polylines
// p and q and returns a monotone matching witnessing it. The returned
// matching covers every point of both polylines and its maximum matched
// pair distance equals the returned distance.
func DiscreteFrechet(p, q []Point) (float64, []MatchPair) {
	n, m := len(p), len(q)
	if n == 0 || m == 0 {
		return 0, nil
	}
	ca := make([][]float64, n)
	par := make([][][2]int, n)
	for i := range ca {
		ca[i] = make([]float64, m)
		par[i] = make([][2]int, m)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			d := Dist(p[i], q[j])
			if i == 0 && j == 0 {
				ca[i][j] = d
				par[i][j] = [2]int{-1, -1}
				continue
			}
			best := math.Inf(1)
			bp := [2]int{-1, -1}
			if i > 0 && ca[i-1][j] < best {
				best = ca[i-1][j]
				bp = [2]int{i - 1, j}
			}
			if i > 0 && j > 0 && ca[i-1][j-1] < best {
				best = ca[i-1][j-1]
				bp = [2]int{i - 1, j - 1}
			}
			if j > 0 && ca[i][j-1] < best {
				best = ca[i][j-1]
				bp = [2]int{i, j - 1}
			}
			ca[i][j] = math.Max(best, d)
			par[i][j] = bp
		}
	}
	var rev []MatchPair
	for i, j := n-1, m-1; i >= 0 && j >= 0; {
		rev = append(rev, MatchPair{P: i, Q: j})
		pi, pj := par[i][j][0], par[i][j][1]
		if pi < 0 {
			break
		}
		i, j = pi, pj
	}
	matching := make([]MatchPair, len(rev))
	for k, v := range rev {
		matching[len(rev)-1-k] = v
	}
	return ca[n-1][m-1], matching
}

// CheckMatching verifies that matching is a valid monotone matching between
// p and q covering every point of both, with all matched pairs at distance
// at most eps. It returns the maximum matched pair distance.
func CheckMatching(p, q []Point, matching []MatchPair, eps float64) (float64, error) {
	if len(p) == 0 || len(q) == 0 {
		return 0, errors.New("polysimp: empty polyline")
	}
	if len(matching) == 0 {
		return 0, errors.New("polysimp: empty matching")
	}
	seenP := make([]bool, len(p))
	seenQ := make([]bool, len(q))
	prevP, prevQ := -1, -1
	maxd := 0.0
	for _, mp := range matching {
		if mp.P < 0 || mp.P >= len(p) || mp.Q < 0 || mp.Q >= len(q) {
			return 0, fmt.Errorf("polysimp: matching pair %v out of range", mp)
		}
		if mp.P < prevP || mp.Q < prevQ || (mp.P == prevP && mp.Q == prevQ) {
			return 0, fmt.Errorf("polysimp: matching not monotone at %v", mp)
		}
		d := Dist(p[mp.P], q[mp.Q])
		if d > eps {
			return 0, fmt.Errorf("polysimp: matched pair %v at distance %v > eps %v", mp, d, eps)
		}
		if d > maxd {
			maxd = d
		}
		seenP[mp.P] = true
		seenQ[mp.Q] = true
		prevP, prevQ = mp.P, mp.Q
	}
	for i, s := range seenP {
		if !s {
			return 0, fmt.Errorf("polysimp: p[%d] unmatched", i)
		}
	}
	for j, s := range seenQ {
		if !s {
			return 0, fmt.Errorf("polysimp: q[%d] unmatched", j)
		}
	}
	return maxd, nil
}
