// Package sedt computes the exact squared Euclidean distance transform (SEDT)
// of a finite 2D binary grid: for every pixel, the squared distance to the
// nearest pixel of the opposite value, plus that pixel's coordinates.
// Ties are broken by row, then column. If the whole grid has one value,
// every pixel reports "no target".
//
// The transform is exact (integer arithmetic only) and runs in O(W*H) time
// using the separable lower-envelope algorithm of Felzenszwalb &
// Huttenlocher, extended to propagate source coordinates with a
// (distance, row, column) total order.
package sedt

// inf is a sentinel squared distance meaning "no seed reachable". It is
// small enough that inf+maxCoord^2 never overflows int64 for any sane grid.
const inf = int64(1) << 60

// zSentinel must be strictly smaller than any split value dt1d can produce
// (split returns at least -inf), so the envelope base case never pops below 0.
const zSentinel = -(int64(1) << 62)

// Point is a pixel coordinate (row, column), zero-based.
type Point struct {
	Row int
	Col int
}

// Cell is the distance-transform result for one pixel.
type Cell struct {
	HasTarget bool  // false when no opposite-value pixel exists
	Dist2     int64 // squared Euclidean distance to Src; -1 when !HasTarget
	Src       Point // nearest opposite-value pixel; valid iff HasTarget
}

// noTarget is the cell value when the grid has no opposite-value pixel.
var noTarget = Cell{HasTarget: false, Dist2: -1}

// floorDiv returns floor(a/b) for b > 0, correct for negative a.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// tiePrefer reports whether candidate a is preferred over candidate b under
// the (row, column) lexicographic order of their source pixels. Source
// coordinates are unique within one 1D transform, so the order is total.
func tiePrefer(a, b int, srcR, srcC []int) bool {
	if srcR[a] != srcR[b] {
		return srcR[a] < srcR[b]
	}
	return srcC[a] < srcC[b]
}

// split returns the smallest integer x at which parabola q beats parabola r
// (r < q), where parabola p has value (x-p)^2 + f[p]. "Beats" includes the
// tie-break: at an exact equality point the candidate with the
// lexicographically smaller source wins.
func split(r, q int, f []int64, srcR, srcC []int) int64 {
	fr, fq := f[r], f[q]
	switch {
	case fq >= inf:
		return inf // q never wins (or both empty: irrelevant)
	case fr >= inf:
		return -inf // q wins everywhere
	}
	// Intersection t solves (t-q)^2+fq = (t-r)^2+fr  =>  t = num/den.
	num := (fq + int64(q)*int64(q)) - (fr + int64(r)*int64(r))
	den := int64(2 * (q - r))
	fl := floorDiv(num, den)
	if num == fl*den && tiePrefer(q, r, srcR, srcC) {
		return fl // t is integral and q wins the tie there
	}
	return fl + 1
}

// dt1d computes the 1D squared distance transform of f in place semantics:
// out[q] = min over p of (q-p)^2 + f[p], propagating source coordinates with
// (distance, row, column) tie-breaking. f, srcR, srcC have length n; the out
// slices must also have length n and may not alias the inputs.
func dt1d(f []int64, srcR, srcC []int, outF []int64, outR, outC []int) {
	n := len(f)
	if n == 0 {
		return
	}
	v := make([]int, n)     // parabolas in the lower envelope
	z := make([]int64, n+1) // z[k] = first x where v[k] leads
	k := 0
	v[0] = 0
	z[0] = zSentinel
	z[1] = inf
	for q := 1; q < n; q++ {
		for {
			s := split(v[k], q, f, srcR, srcC)
			if s <= z[k] {
				k-- // parabola v[k] is fully dominated
			} else {
				k++
				v[k] = q
				z[k] = s
				z[k+1] = inf
				break
			}
		}
	}
	k = 0
	for q := 0; q < n; q++ {
		// z[k+1] is the first integer position where parabola v[k+1]
		// leads (split returns the first winning position, tie-breaks
		// included), so the boundary itself already belongs to v[k+1].
		for z[k+1] <= int64(q) {
			k++
		}
		p := v[k]
		d := int64(q - p)
		outF[q] = d*d + f[p]
		outR[q] = srcR[p]
		outC[q] = srcC[p]
	}
}

// edt2 computes, for every pixel, the squared distance to the nearest seed
// pixel and that seed's coordinates. seed is row-major, length w*h.
// Distance inf means "no seed". Runs in O(w*h) time and allocations.
func edt2(w, h int, seed []bool) (dist []int64, srcR, srcC []int) {
	n := w * h
	dist = make([]int64, n)
	srcR = make([]int, n)
	srcC = make([]int, n)

	// Pass 1: 1D transform down each column. Seeds carry their own coords.
	fy := make([]int64, h)
	sry := make([]int, h)
	scy := make([]int, h)
	oy := make([]int64, h)
	ory := make([]int, h)
	ocy := make([]int, h)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			if seed[y*w+x] {
				fy[y] = 0
			} else {
				fy[y] = inf
			}
			sry[y] = y
			scy[y] = x
		}
		dt1d(fy, sry, scy, oy, ory, ocy)
		for y := 0; y < h; y++ {
			i := y*w + x
			dist[i] = oy[y]
			srcR[i] = ory[y]
			srcC[i] = ocy[y]
		}
	}

	// Pass 2: 1D transform across each row, carrying pass-1 sources.
	fx := make([]int64, w)
	srx := make([]int, w)
	scx := make([]int, w)
	ox := make([]int64, w)
	orx := make([]int, w)
	ocx := make([]int, w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			fx[x] = dist[i]
			srx[x] = srcR[i]
			scx[x] = srcC[i]
		}
		dt1d(fx, srx, scx, ox, orx, ocx)
		for x := 0; x < w; x++ {
			i := y*w + x
			dist[i] = ox[x]
			srcR[i] = orx[x]
			srcC[i] = ocx[x]
		}
	}
	return dist, srcR, srcC
}

func makeCell(d int64, r, c int) Cell {
	if d >= inf {
		return noTarget
	}
	return Cell{HasTarget: true, Dist2: d, Src: Point{Row: r, Col: c}}
}

// ComputeField returns the exact squared Euclidean distance transform of the
// binary image cells (row-major, w*h). Each pixel maps to its nearest
// opposite-value pixel. Runs in O(w*h) time, O(w*h) memory.
func ComputeField(w, h int, cells []bool) []Cell {
	n := w * h
	inv := make([]bool, n)
	for i, v := range cells {
		inv[i] = !v
	}
	// Distance to nearest true pixel and to nearest false pixel; each pixel
	// consumes the transform of the opposite set.
	dT, rT, cT := edt2(w, h, cells)
	dF, rF, cF := edt2(w, h, inv)
	out := make([]Cell, n)
	for i := 0; i < n; i++ {
		if cells[i] {
			out[i] = makeCell(dF[i], rF[i], cF[i])
		} else {
			out[i] = makeCell(dT[i], rT[i], cT[i])
		}
	}
	return out
}

// ReferenceField is the brute-force all-pairs definition of the transform,
// O((w*h)^2). It shares no code with ComputeField and is used to validate it.
func ReferenceField(w, h int, cells []bool) []Cell {
	n := w * h
	out := make([]Cell, n)
	for i := 0; i < n; i++ {
		r1, c1 := i/w, i%w
		best := noTarget
		for j := 0; j < n; j++ {
			if cells[j] == cells[i] {
				continue
			}
			r2, c2 := j/w, j%w
			dr, dc := r1-r2, c1-c2
			d := int64(dr*dr + dc*dc)
			if !best.HasTarget || d < best.Dist2 ||
				(d == best.Dist2 && (r2 < best.Src.Row ||
					(r2 == best.Src.Row && c2 < best.Src.Col))) {
				best = Cell{HasTarget: true, Dist2: d, Src: Point{Row: r2, Col: c2}}
			}
		}
		out[i] = best
	}
	return out
}
