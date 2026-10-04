// Package edtgrid implements an exact squared Euclidean distance transform
// (EDT) over a finite binary 2D integer grid. For every pixel it computes
// the squared distance to the nearest opposite-valued pixel together with
// that pixel's coordinates. Ties are broken by row, then column. When the
// whole grid holds a single value, every pixel reports the no-target marker.
//
// Rectangular region flips produce a new immutable version; older snapshots
// keep answering queries with their old distances. Each flip reports the
// exact set of pixels whose distance or nearest point changed. The change
// set is never restricted to the edited rectangle: deleting the unique
// nearest point may affect the whole image, so the update falls back to a
// full recomputation and records how many pixels were examined.
package edtgrid

import (
	"errors"
	"fmt"
	"math"
)

// Point is a pixel coordinate on the grid.
type Point struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

// Rect is a half-open rectangle [R0,R1) x [C0,C1) in row/column space.
type Rect struct {
	R0 int `json:"row0"`
	C0 int `json:"col0"`
	R1 int `json:"row1"`
	C1 int `json:"col1"`
}

// Cell is the query result for one pixel.
type Cell struct {
	Value     uint8 `json:"value"`
	HasTarget bool  `json:"hasTarget"`
	// Dist is the squared Euclidean distance to Source. Valid only when
	// HasTarget is true; otherwise it is the no-target marker (-1).
	Dist   int64 `json:"dist"`
	Source Point `json:"source"`
}

// NoTargetDist is the distance marker used when no opposite-valued pixel
// exists anywhere on the grid.
const NoTargetDist int64 = -1

// Grid is an immutable snapshot of a binary mask and its exact EDT.
// Flip returns a new Grid; the receiver is never mutated, so old snapshots
// keep serving old distances.
type Grid struct {
	rows int
	cols int
	vals []uint8 // 0 or 1, row-major
	dist []int64 // squared distance to nearest opposite-valued pixel
	src  []Point // nearest opposite-valued pixel (tie: row, then column)
	has  []bool  // false => no-target marker
}

// ErrEmptyGrid is returned when a grid dimension is not positive.
var ErrEmptyGrid = errors.New("edtgrid: grid dimensions must be positive")

// New builds a grid of rows x cols filled with fill (0 or 1) and computes
// its distance transform.
func New(rows, cols int, fill uint8) (*Grid, error) {
	if rows < 1 || cols < 1 {
		return nil, ErrEmptyGrid
	}
	if fill > 1 {
		return nil, errors.New("edtgrid: fill value must be 0 or 1")
	}
	vals := make([]uint8, rows*cols)
	if fill == 1 {
		for i := range vals {
			vals[i] = 1
		}
	}
	return FromValues(rows, cols, vals)
}

// FromValues builds a grid from an explicit row-major mask of 0/1 values.
// The slice is copied.
func FromValues(rows, cols int, vals []uint8) (*Grid, error) {
	if rows < 1 || cols < 1 {
		return nil, ErrEmptyGrid
	}
	if len(vals) != rows*cols {
		return nil, fmt.Errorf("edtgrid: got %d values for %dx%d grid", len(vals), rows, cols)
	}
	cp := make([]uint8, len(vals))
	for i, v := range vals {
		if v > 1 {
			return nil, fmt.Errorf("edtgrid: value at index %d is %d, want 0 or 1", i, v)
		}
		cp[i] = v
	}
	g := &Grid{rows: rows, cols: cols, vals: cp}
	g.dist, g.src, g.has = computeEDT(cp, rows, cols)
	return g, nil
}

// Rows returns the grid height.
func (g *Grid) Rows() int { return g.rows }

// Cols returns the grid width.
func (g *Grid) Cols() int { return g.cols }

// InBounds reports whether (row, col) lies on the grid.
func (g *Grid) InBounds(row, col int) bool {
	return row >= 0 && row < g.rows && col >= 0 && col < g.cols
}

// Value returns the mask value (0 or 1) at (row, col).
func (g *Grid) Value(row, col int) uint8 {
	return g.vals[row*g.cols+col]
}

// At returns the full query result for one pixel of this snapshot.
func (g *Grid) At(row, col int) Cell {
	i := row*g.cols + col
	c := Cell{Value: g.vals[i], HasTarget: g.has[i], Dist: NoTargetDist}
	if g.has[i] {
		c.Dist = g.dist[i]
		c.Source = g.src[i]
	}
	return c
}

// Diff describes the outcome of a Flip.
type Diff struct {
	// Changed lists every pixel whose distance or nearest point changed,
	// sorted by row, then column. It is exact and not limited to the
	// edited rectangle.
	Changed []Point `json:"changed"`
	// ExaminedPixels records how many grid pixels the update algorithm
	// inspected. A full recomputation examines rows*cols pixels; a no-op
	// flip examines none.
	ExaminedPixels int `json:"examinedPixels"`
	// FullRecompute is true when the update degenerated to a full
	// recomputation of the transform.
	FullRecompute bool `json:"fullRecompute"`
	// NoOp is true when the flip did not change any mask value.
	NoOp bool `json:"noOp"`
}

// Flip inverts every pixel inside r (clamped to the grid) and returns a new
// snapshot plus the exact change set. The receiver is left untouched.
//
// Deleting the unique nearest point of a pixel may shift nearest points
// anywhere on the grid, so the update currently always degenerates to a
// full recomputation (O(rows*cols)) and reports ExaminedPixels accordingly.
// Flips that change no value are no-ops and examine zero pixels.
func (g *Grid) Flip(r Rect) (*Grid, Diff) {
	r0 := clamp(r.R0, 0, g.rows)
	r1 := clamp(r.R1, 0, g.rows)
	c0 := clamp(r.C0, 0, g.cols)
	c1 := clamp(r.C1, 0, g.cols)
	if r0 >= r1 || c0 >= c1 {
		return g, Diff{Changed: nil, ExaminedPixels: 0, NoOp: true}
	}

	vals := make([]uint8, len(g.vals))
	copy(vals, g.vals)
	for row := r0; row < r1; row++ {
		for col := c0; col < c1; col++ {
			i := row*g.cols + col
			vals[i] = 1 - vals[i]
		}
	}

	ng := &Grid{rows: g.rows, cols: g.cols, vals: vals}
	ng.dist, ng.src, ng.has = computeEDT(vals, g.rows, g.cols)

	changed := make([]Point, 0, 16)
	for i := range vals {
		if g.has[i] != ng.has[i] ||
			(g.has[i] && (g.dist[i] != ng.dist[i] || g.src[i] != ng.src[i])) {
			changed = append(changed, Point{Row: i / g.cols, Col: i % g.cols})
		}
	}
	return ng, Diff{
		Changed:        changed,
		ExaminedPixels: g.rows * g.cols,
		FullRecompute:  true,
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lessPoint(a, b Point) bool {
	return a.Row < b.Row || (a.Row == b.Row && a.Col < b.Col)
}

func sqDist(r1, c1 int, p Point) int64 {
	dr := int64(r1 - p.Row)
	dc := int64(c1 - p.Col)
	return dr*dr + dc*dc
}

// infDist is a sentinel larger than any real squared distance but small
// enough to keep threshold arithmetic overflow-free.
const infDist = math.MaxInt64 / 4

func floorDiv(a, b int64) int64 { // b > 0
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 { // b > 0
	return -floorDiv(-a, b)
}

// threshold returns the smallest integer x at which parabola a (position pa,
// offset fa, source label la) wins over parabola b. Parabola a sits to the
// right of b (pa > pb). a is strictly lower than b for x > s0 where
// s0 = ((fa+pa^2)-(fb+pb^2)) / (2(pa-pb)); at x == s0 the tie is broken by
// the source labels (row, then column).
func threshold(pa, fa int64, la Point, pb, fb int64, lb Point) int64 {
	num := (fa + pa*pa) - (fb + pb*pb)
	den := 2 * (pa - pb)
	if lessPoint(la, lb) {
		return ceilDiv(num, den)
	}
	return floorDiv(num, den) + 1
}

// edt1d computes the 1D squared distance transform of f along one line,
// Felzenszwalb-Huttenlocher style, with deterministic tie-breaking: among
// parabolas giving the same minimal value at a position, the one with the
// smallest source label (row, then column) wins. Labels must be distinct.
// f entries may be infDist. out receives the minima, outLabel the winning
// labels (infDist is propagated when no finite parabola exists).
func edt1d(f []int64, labels []Point, out []int64, outLabel []Point) {
	n := len(f)
	v := make([]int, n)     // positions of parabolas on the lower envelope
	z := make([]int64, n+1) // z[k]: first integer where v[k] wins
	k := 0
	v[0] = 0
	z[0] = math.MinInt64
	z[1] = math.MaxInt64
	for q := 1; q < n; q++ {
		t := threshold(int64(q), f[q], labels[q], int64(v[k]), f[v[k]], labels[v[k]])
		for t <= z[k] {
			k--
			t = threshold(int64(q), f[q], labels[q], int64(v[k]), f[v[k]], labels[v[k]])
		}
		k++
		v[k] = q
		z[k] = t
		z[k+1] = math.MaxInt64
	}
	k = 0
	for p := 0; p < n; p++ {
		for z[k+1] <= int64(p) {
			k++
		}
		w := v[k]
		if f[w] >= infDist {
			out[p] = infDist
		} else {
			d := int64(p - w)
			out[p] = d*d + f[w]
		}
		outLabel[p] = labels[w]
	}
}

// edtFromSources computes, for every pixel, the squared distance to the
// nearest pixel whose value equals srcVal, plus that pixel's coordinates.
// Exact separable transform: one 1D pass along every column, then one 1D
// pass along every row. O(rows*cols).
func edtFromSources(vals []uint8, rows, cols int, srcVal uint8) (dist []int64, src []Point, has []bool) {
	n := rows * cols
	colDist := make([]int64, n) // after pass 1: distance to nearest source in the same column
	colRow := make([]int, n)    // after pass 1: row of that source

	f := make([]int64, rows)
	labels := make([]Point, rows)
	out := make([]int64, rows)
	outLabel := make([]Point, rows)
	for c := 0; c < cols; c++ {
		for r := 0; r < rows; r++ {
			if vals[r*cols+c] == srcVal {
				f[r] = 0
			} else {
				f[r] = infDist
			}
			labels[r] = Point{Row: r, Col: c}
		}
		edt1d(f, labels, out, outLabel)
		for r := 0; r < rows; r++ {
			colDist[r*cols+c] = out[r]
			colRow[r*cols+c] = outLabel[r].Row
		}
	}

	dist = make([]int64, n)
	src = make([]Point, n)
	has = make([]bool, n)
	f2 := make([]int64, cols)
	labels2 := make([]Point, cols)
	out2 := make([]int64, cols)
	outLabel2 := make([]Point, cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			f2[c] = colDist[r*cols+c]
			labels2[c] = Point{Row: colRow[r*cols+c], Col: c}
		}
		edt1d(f2, labels2, out2, outLabel2)
		for c := 0; c < cols; c++ {
			i := r*cols + c
			dist[i] = out2[c]
			src[i] = outLabel2[c]
			has[i] = out2[c] < infDist
		}
	}
	return dist, src, has
}

// computeEDT runs the exact squared Euclidean distance transform: for every
// pixel, the squared distance to the nearest opposite-valued pixel and that
// pixel's coordinates, ties broken by row then column. It evaluates the
// separable transform twice (nearest 0-pixel and nearest 1-pixel) and keeps
// the opposite-valued side per pixel. O(rows*cols) time and memory.
func computeEDT(vals []uint8, rows, cols int) (dist []int64, src []Point, has []bool) {
	d0, s0, h0 := edtFromSources(vals, rows, cols, 0)
	d1, s1, h1 := edtFromSources(vals, rows, cols, 1)
	n := rows * cols
	dist = make([]int64, n)
	src = make([]Point, n)
	has = make([]bool, n)
	for i := 0; i < n; i++ {
		if vals[i] == 0 {
			dist[i], src[i], has[i] = d1[i], s1[i], h1[i]
		} else {
			dist[i], src[i], has[i] = d0[i], s0[i], h0[i]
		}
	}
	return dist, src, has
}
