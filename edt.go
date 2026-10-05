// Package edt computes the exact squared Euclidean distance transform of a
// binary image: for every pixel it finds the nearest foreground pixel and
// reports the squared distance plus the source pixel itself.
//
// The algorithm is the separable parabola-envelope method of Felzenszwalb
// and Huttenlocher, running in O(H*W) time — linear in the number of pixels.
// All distance and parabola-intersection comparisons are performed with
// integer (int64) arithmetic only; no floating point is used anywhere.
//
// Ties between equally near foreground pixels are broken by choosing the
// source with the lexicographically smallest (row, col) coordinate. The
// tie-break is made exact by folding it into a single composite integer key
// distance*m + rank, where m exceeds every possible rank, so the envelope
// comparisons stay pure integer operations.
package edt

import "math"

// Point is a pixel coordinate in the image.
type Point struct {
	Row, Col int
}

// inf is the internal sentinel for "no foreground site". It is only ever
// assigned and compared, never scaled, so it cannot overflow.
const inf = int64(1) << 62

// DistanceTransform returns, for each pixel of fg, the squared Euclidean
// distance to the nearest foreground (true) pixel and that pixel's
// coordinates. When several foreground pixels are equally near, the one with
// the lexicographically smallest (row, col) wins.
//
// If the image contains no foreground pixel, every distance is -1 and every
// source is Point{-1, -1}.
//
// The transform is exact for images up to roughly 10^4 x 10^4 pixels; beyond
// that the int64 intersection arithmetic could overflow.
func DistanceTransform(fg [][]bool) (dist [][]int64, src [][]Point) {
	h := len(fg)
	dist = make([][]int64, h)
	src = make([][]Point, h)
	if h == 0 {
		return dist, src
	}
	w := len(fg[0])
	for r := 0; r < h; r++ {
		if len(fg[r]) != w {
			panic("edt: ragged input image")
		}
		dist[r] = make([]int64, w)
		src[r] = make([]Point, w)
	}
	if w == 0 {
		return dist, src
	}

	// Pass 1: 1D transform of every column. g holds the column distances,
	// sr the row index of the winning site in that column.
	g := make([][]int64, h)
	sr := make([][]int32, h)
	for r := range g {
		g[r] = make([]int64, w)
		sr[r] = make([]int32, w)
	}
	f1 := make([]int64, h)
	r1 := make([]int64, h)
	d1 := make([]int64, h)
	s1 := make([]int32, h)
	for r := 0; r < h; r++ {
		r1[r] = int64(r)
	}
	for c := 0; c < w; c++ {
		for r := 0; r < h; r++ {
			if fg[r][c] {
				f1[r] = 0
			} else {
				f1[r] = inf
			}
		}
		transform1D(f1, r1, int64(h), d1, s1)
		for r := 0; r < h; r++ {
			g[r][c] = d1[r]
			sr[r][c] = s1[r]
		}
	}

	// Pass 2: 1D transform of every row of g. The rank of column c is the
	// lexicographic rank of its candidate source (sr[r][c], c), so global
	// ties resolve to the smallest source coordinate.
	m2 := int64(h) * int64(w)
	ww := int64(w)
	f2 := make([]int64, w)
	r2 := make([]int64, w)
	d2 := make([]int64, w)
	s2 := make([]int32, w)
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			f2[c] = g[r][c]
			r2[c] = int64(sr[r][c])*ww + int64(c)
		}
		transform1D(f2, r2, m2, d2, s2)
		for c := 0; c < w; c++ {
			if d2[c] == inf {
				dist[r][c] = -1
				src[r][c] = Point{-1, -1}
			} else {
				dist[r][c] = d2[c]
				j := s2[c]
				src[r][c] = Point{int(sr[r][j]), int(j)}
			}
		}
	}
	return dist, src
}

// transform1D computes the 1D squared distance transform of f:
//
//	dist[k] = min over sites i of (k-i)^2 + f[i]
//	src[k]  = the argmin site, ties broken by the smaller rank[i]
//
// m must be greater than every rank[i]; sites with f[i] == inf are ignored.
// If no finite site exists, every dist[k] is inf and every src[k] is -1.
//
// Internally each site i is the parabola P_i(k) = m*(k-i)^2 + f[i]*m +
// rank[i]. Because ranks are distinct and below m, minimising this composite
// value is exactly minimising (distance, rank) lexicographically, and no two
// parabolas ever tie — so the lower envelope is unambiguous.
func transform1D(f, rank []int64, m int64, dist []int64, src []int32) {
	n := len(f)
	v := make([]int, n)    // envelope stack: site indices
	b := make([]int64, n)  // b[t]: first k where v[t] becomes the best site
	fc := make([]int64, n) // composite value f[i]*m + rank[i] of finite sites
	top := -1
	for q := 0; q < n; q++ {
		if f[q] == inf {
			continue
		}
		fc[q] = f[q]*m + rank[q]
		var s int64
		for top >= 0 {
			p := v[top]
			pi, qi := int64(p), int64(q)
			// P_q(k) < P_p(k)  <=>  k > num/den, so the first integer k
			// where q strictly beats p is floor(num/den)+1.
			num := m*(qi*qi-pi*pi) + fc[q] - fc[p]
			den := 2 * m * (qi - pi)
			s = floorDiv(num, den) + 1
			if s > b[top] {
				break
			}
			top--
		}
		top++
		v[top] = q
		if top == 0 {
			b[0] = math.MinInt64 // sentinel: first site wins from -infinity
		} else {
			b[top] = s
		}
	}
	if top < 0 {
		for k := 0; k < n; k++ {
			dist[k] = inf
			src[k] = -1
		}
		return
	}
	ptr := 0
	for k := 0; k < n; k++ {
		kk := int64(k)
		for ptr < top && b[ptr+1] <= kk {
			ptr++
		}
		s := v[ptr]
		src[k] = int32(s)
		d := kk - int64(s)
		dist[k] = d*d + f[s]
	}
}

// floorDiv returns floor(a/b) for b > 0, without floating point.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}
