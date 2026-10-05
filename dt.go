// Package dt computes the squared Euclidean distance transform of a binary
// image in time linear in the number of pixels.
//
// For every pixel it reports the squared Euclidean distance to the nearest
// foreground pixel and the coordinates of that source pixel. When several
// foreground pixels are equally near, the one with lexicographically
// smallest (row, column) coordinates is chosen.
//
// The algorithm is the separable parabola-envelope transform of
// Felzenszwalb and Huttenlocher. All distance and parabola-intersection
// comparisons use exact integer arithmetic (intersection points are kept
// as rationals and compared by cross-multiplication); no floating point
// is used anywhere.
package dt

// NoDist is the distance reported for every pixel when the image contains
// no foreground pixel.
const NoDist int64 = -1

// NoSource is the coordinate reported for every pixel when the image
// contains no foreground pixel.
const NoSource = -1

// Result holds the output of Transform.
type Result struct {
	// Dist[r][c] is the squared Euclidean distance from pixel (r, c) to
	// the nearest foreground pixel, or NoDist if there is none.
	Dist [][]int64
	// SrcR[r][c] and SrcC[r][c] are the row and column of the nearest
	// foreground pixel (lexicographically smallest on ties), or NoSource
	// if there is none.
	SrcR [][]int
	SrcC [][]int
}

// Transform computes the squared Euclidean distance transform of the
// binary image fg, where fg[r][c] == true marks a foreground pixel.
// Rows must all have the same length. It runs in O(len(fg) * width) time.
func Transform(fg [][]bool) Result {
	h := len(fg)
	if h == 0 {
		return Result{Dist: [][]int64{}, SrcR: [][]int{}, SrcC: [][]int{}}
	}
	w := len(fg[0])
	for r := 1; r < h; r++ {
		if len(fg[r]) != w {
			panic("dt: ragged input, all rows must have equal length")
		}
	}

	rowsI64 := func() [][]int64 {
		buf := make([]int64, h*w)
		m := make([][]int64, h)
		for r := range m {
			m[r] = buf[r*w : (r+1)*w]
		}
		return m
	}
	rowsInt := func() [][]int {
		buf := make([]int, h*w)
		m := make([][]int, h)
		for r := range m {
			m[r] = buf[r*w : (r+1)*w]
		}
		return m
	}
	dist := rowsI64()
	srcR := rowsInt()
	srcC := rowsInt()
	if w == 0 {
		return Result{Dist: dist, SrcR: srcR, SrcC: srcC}
	}

	// inf exceeds any real squared distance, (h-1)^2 + (w-1)^2.
	inf := int64(h)*int64(h) + int64(w)*int64(w)

	// Scratch space reused by every 1D transform.
	n := h
	if w > h {
		n = w
	}
	ws := workspace{
		v:  make([]int, n),
		zn: make([]int64, n),
		zd: make([]int64, n),
		d:  make([]int64, n),
		s:  make([]int, n),
	}

	// Pass 1: for each row, the 1D transform over columns.
	// g[r][c] = min over c' of (c-c')^2 with fg[r][c'], and csrc[r][c]
	// is the smallest such c'. Ties favor the smaller column index.
	g := rowsI64()
	csrc := rowsInt()
	any := false
	f := make([]int64, w)
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			if fg[r][c] {
				f[c] = 0
				any = true
			} else {
				f[c] = inf
			}
		}
		ws.dt1d(f, g[r], csrc[r])
	}

	if !any {
		for r := 0; r < h; r++ {
			for c := 0; c < w; c++ {
				dist[r][c] = NoDist
				srcR[r][c] = NoSource
				srcC[r][c] = NoSource
			}
		}
		return Result{Dist: dist, SrcR: srcR, SrcC: srcC}
	}

	// Pass 2: for each column, the 1D transform over rows of g.
	// dist[r][c] = min over r' of (r-r')^2 + g[r'][c]. Ties favor the
	// smaller row index r', which is exactly the first coordinate of the
	// source; combined with pass 1 this yields the lexicographically
	// smallest (row, column) among all equally near foreground pixels.
	fc := make([]int64, h)
	for c := 0; c < w; c++ {
		for r := 0; r < h; r++ {
			fc[r] = g[r][c]
		}
		ws.dt1d(fc, ws.d, ws.s)
		for r := 0; r < h; r++ {
			dist[r][c] = ws.d[r]
			srcR[r][c] = ws.s[r]
			srcC[r][c] = csrc[ws.s[r]][c]
		}
	}
	return Result{Dist: dist, SrcR: srcR, SrcC: srcC}
}

// workspace holds scratch buffers reused across dt1d calls. Every slice
// must have length >= the length of any transformed row or column.
type workspace struct {
	v  []int   // envelope parabola indices
	zn []int64 // envelope boundary numerators
	zd []int64 // envelope boundary denominators
	d  []int64 // output distances
	s  []int   // output source indices
}

// dt1d computes the 1D squared distance transform of f into d and src:
// for each q, d[q] = min over p of (q-p)^2 + f[p] and src[q] is the
// minimizing p. Ties are broken in favor of the smallest p. Each call
// runs in O(len(f)) time.
//
// The lower envelope of the parabolas y = (x-p)^2 + f[p] is maintained.
// All parabolas share the same curvature, so each pair intersects exactly
// once; intersection points are stored as exact rationals num/den with
// den > 0 and compared by integer cross-multiplication.
//
// Tie semantics: for parabolas p < q intersecting at s, p is nearer for
// x < s and q for x > s, and at x == s the smaller index p wins. Hence a
// parabola is dropped from the envelope exactly when its interval becomes
// empty, i.e. when the new intersection s satisfies s <= z[top].
func (ws *workspace) dt1d(f, d []int64, src []int) {
	n := len(f)
	if n == 0 {
		return
	}
	v, zn, zd := ws.v, ws.zn, ws.zd

	// Build the envelope. zn[k]/zd[k] is the left boundary of the k-th
	// parabola; z[0] is implicitly -inf and never read.
	k := 0
	v[0] = 0
	for q := 1; q < n; q++ {
		var sn, sd int64
		for {
			p := v[k]
			// Intersection of p and q (p < q):
			// s = ((f[q]+q^2) - (f[p]+p^2)) / (2q - 2p), den > 0.
			sn = (f[q] + int64(q)*int64(q)) - (f[p] + int64(p)*int64(p))
			sd = int64(2 * (q - p))
			// Drop v[k] while s <= z[k], i.e. sn*zd[k] <= zn[k]*sd.
			if k == 0 || sn*zd[k] > zn[k]*sd {
				break
			}
			k--
		}
		k++
		v[k] = q
		zn[k], zd[k] = sn, sd
	}
	top := k

	// Sweep the envelope left to right.
	k = 0
	for q := 0; q < n; q++ {
		// Advance while the next boundary is < q: zn[k+1] < q*zd[k+1].
		// At an exact integer boundary the left (smaller-index) parabola
		// is kept, implementing the smallest-index tie-break.
		for k < top && zn[k+1] < int64(q)*zd[k+1] {
			k++
		}
		p := v[k]
		d[q] = int64(q-p)*int64(q-p) + f[p]
		src[q] = p
	}
}
