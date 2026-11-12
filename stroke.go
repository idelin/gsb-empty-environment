// Package taskpkg builds stroked outlines from piecewise cubic Beziers for a
// vector rendering host.
//
// The stroker turns a centerline described by cubic Bezier segments into one
// or more closed, directed polygon contours at a fixed distance (half the
// stroke width) from the centerline. Subdivision is driven exclusively by
// geometric error tolerances (chord error, tangent turn and offset-curvature
// guards); fixed sampling counts are never used. Miter joins that would exceed
// the configured miter limit fall back to bevels, round joins/caps are
// discretized from a maximum chord error, and zero-length segments as well as
// reverse foldbacks (cusps) receive explicit round-cap treatment.
package taskpkg

import (
	"errors"
	"math"
)

// Join selects how the outer boundary turns at a path vertex.
type Join int

const (
	JoinMiter Join = iota
	JoinBevel
	JoinRound
)

// Cap selects how the two open ends of a subpath are closed.
type Cap int

const (
	CapButt Cap = iota
	CapRound
	CapSquare
)

// Point is a 2D vector in the host coordinate system.
type Point struct {
	X, Y float64
}

// Cubic is a cubic Bezier segment from P0 to P3 with control points P1, P2.
type Cubic struct {
	P0, P1, P2, P3 Point
}

// Options controls stroking.
//
// Width is the full stroke width; the outline sits Width/2 away from the
// centerline. MiterLimit follows SVG semantics as a ratio of miter length
// (outer tip to inner corner) to stroke width: once that ratio is exceeded
// the join degrades to a bevel. Tolerance is the maximum allowed chord error
// when flattening curves and the maximum chord error used to discretize round
// joins and round caps.
type Options struct {
	Width      float64
	Join       Join
	Cap        Cap
	MiterLimit float64
	Tolerance  float64
}

// Path is a piecewise cubic centerline. Closed means the last segment joins
// back to the first; otherwise the path has two open ends. Segments whose
// endpoints do not chain are treated as separate subpaths.
type Path struct {
	Segments []Cubic
	Closed   bool
}

// Contour is a closed, directed polygon loop. Points must be consumed in
// order; the closing edge is implicit (the last point connects back to the
// first). Outer boundaries wind clockwise, inner boundaries counter-clockwise,
// matching the screen-oriented coordinate system of a rendering host.
type Contour struct {
	Points []Point
}

const (
	// cuspCos marks a tangent turn of ~150 degrees or more as a foldback.
	cuspCos = -0.8660254037844386
	// maxSubdiv guards recursion depth against pathological coefficients.
	maxSubdiv = 40
)

// Stroke builds the closed outline contours for path p.
func Stroke(p Path, opt Options) ([]Contour, error) {
	if opt.Width < 0 || math.IsNaN(opt.Width) || math.IsInf(opt.Width, 0) {
		return nil, errors.New("taskpkg: stroke width must be a finite, non-negative number")
	}
	if opt.Tolerance <= 0 || math.IsNaN(opt.Tolerance) || math.IsInf(opt.Tolerance, 1) {
		return nil, errors.New("taskpkg: tolerance must be a finite, positive number")
	}
	if math.IsNaN(opt.MiterLimit) || math.IsInf(opt.MiterLimit, 1) || opt.MiterLimit < 0 {
		return nil, errors.New("taskpkg: miter limit must be a finite, non-negative number")
	}
	if opt.MiterLimit == 0 {
		opt.MiterLimit = 4
	}

	radius := opt.Width / 2
	var outlines []Contour
	for _, sub := range splitSubpaths(p.Segments, p.Closed) {
		outlines = append(outlines, strokeSubpath(sub, radius, opt)...)
	}
	if len(outlines) == 0 && len(p.Segments) > 0 && radius > 0 {
		// A path made entirely of zero-length segments degenerates to a dot.
		center := p.Segments[0].P0
		outlines = append(outlines, Contour{Points: discretizeCircle(center, radius, opt.Tolerance)})
	}
	return outlines, nil
}

// subpath is one chain of Bezier segments sharing endpoints.
type subpath struct {
	segs   []Cubic
	closed bool
}

func splitSubpaths(segs []Cubic, closed bool) []subpath {
	var subs []subpath
	var chain []Cubic
	flush := func() {
		if len(chain) > 0 {
			subs = append(subs, subpath{segs: chain})
			chain = nil
		}
	}
	for i, s := range segs {
		if approxEqual(s.P0, s.P1) && approxEqual(s.P0, s.P2) && approxEqual(s.P0, s.P3) {
			// Zero-length segments carry no geometry and must not break chaining.
			if i == len(segs)-1 {
				flush()
			}
			continue
		}
		if len(chain) == 0 || approxEqual(chain[len(chain)-1].P3, s.P0) {
			chain = append(chain, s)
		} else {
			flush()
			chain = append(chain, s)
		}
		if i == len(segs)-1 {
			flush()
		}
	}
	if closed && len(subs) == 1 && len(segs) > 0 && approxEqual(segs[len(segs)-1].P3, segs[0].P0) {
		subs[0].closed = true
	}
	return subs
}

// sample is one flattened centerline vertex. corner marks an original segment
// boundary where the requested join style applies; cusp marks a reverse
// foldback where the stroke must be split and sealed with round caps.
type sample struct {
	point  Point
	tan    Point // incoming unit tangent
	outTan Point // outgoing unit tangent at corner/cusp vertices
	corner bool
	cusp   bool
}

// flattenSubpath adaptively subdivides every cubic into a polyline. The
// recursion is controlled by chord error, tangent turn (curvature) and an
// offset-curvature guard, never by a fixed number of samples.
func flattenSubpath(sub subpath, tol float64) []sample {
	var out []sample
	for si, seg := range sub.segs {
		if approxEqual(seg.P0, seg.P3) && approxEqual(seg.P0, seg.P1) && approxEqual(seg.P0, seg.P2) {
			// Zero-length segment: remember the location; no geometry.
			if len(out) == 0 {
				out = append(out, sample{point: seg.P0})
			}
			continue
		}
		if len(out) == 0 {
			out = append(out, sample{point: seg.P0, tan: robustTangent(seg, 0)})
		} else if last := out[len(out)-1]; approxEqual(last.point, seg.P0) {
			startTan := robustTangent(seg, 0)
			if !approxEqualUnit(last.tan, startTan) {
				// The new segment leaves the shared point in a different
				// direction: give it its own start sample so the foldback is
				// detected and the two runs keep their correct tangents.
				out = append(out, sample{point: seg.P0, tan: startTan})
			}
		}
		flattenCubic(seg, tol, 0, &out)

		end := sample{point: seg.P3, tan: robustTangent(seg, 1), corner: true}
		if si < len(sub.segs)-1 || sub.closed {
			next := sub.segs[(si+1)%len(sub.segs)]
			nt := robustTangent(next, 0)
			if approxEqualUnit(end.tan, nt) {
				end.corner = false
			} else {
				// Keep the incoming tangent and record the outgoing one so
				// the join sees the full turn at the shared vertex.
				end.outTan = nt
			}
		}
		last := &out[len(out)-1]
		if approxEqual(last.point, end.point) {
			last.tan = end.tan
			last.outTan = end.outTan
			last.corner = end.corner
		} else {
			out = append(out, end)
		}
	}
	repairTangents(out)
	if sub.closed && len(out) > 1 && approxEqual(out[0].point, out[len(out)-1].point) {
		// Merge the duplicated closing-seam vertex: the first sample keeps
		// its outgoing tangent while inheriting the incoming direction and
		// corner flag of the final sample.
		first, last := &out[0], &out[len(out)-1]
		if first.outTan == (Point{}) {
			first.outTan = first.tan
		}
		first.tan = last.tan
		first.corner = first.corner || last.corner
		out = out[:len(out)-1]
	}
	seamIn, seamOut := out[0].tan, out[0].outTan
	if seamOut == (Point{}) {
		seamOut = seamIn
	}
	if sub.closed && len(out) > 2 && dot(seamIn, seamOut) <= cuspCos {
		// Foldback across the closing seam: rotate so the seam becomes an
		// interior cut, then seal the open ends with round caps.
		rotated := make([]sample, 0, len(out))
		rotated = append(rotated, out[1:]...)
		rotated = append(rotated, out[0])
		out = rotated
		sub.closed = false
	}
	out = insertCuspSplits(out)
	return out
}

// appendCap closes an open end of the offset loop. side is +1 at the end of
// the centerline and -1 at the start, matching the traversal direction of the
// boundary under construction.
func appendCap(loop []Point, end sample, capStyle Cap, radius, side float64, tol float64) []Point {
	center := end.point
	normal := rotate90(end.tan)
	switch capStyle {
	case CapButt:
		// Nothing: the two offset sides share the end-cap edge.
	case CapSquare:
		ext := Point{center.X + radius*side*end.tan.X, center.Y + radius*side*end.tan.Y}
		loop = append(loop,
			Point{ext.X + radius*normal.X, ext.Y + radius*normal.Y},
			Point{ext.X - radius*normal.X, ext.Y - radius*normal.Y})
	case CapRound:
		from := Point{center.X + radius*normal.X, center.Y + radius*normal.Y}
		to := Point{center.X - radius*normal.X, center.Y - radius*normal.Y}
		loop = append(loop, from)
		loop = appendArc(loop, center, from, to, side*math.Pi, radius, tol)
		loop = append(loop, to)
	}
	return loop
}

// appendArc appoints points along a circular arc of radius r around center,
// starting at from and ending at to and sweeping the signed angle sweep. The
// segment count is derived solely from the maximum chord error tol.
func appendArc(out []Point, center, from, to Point, sweep, r, tol float64) []Point {
	delta := wrapAngle(sweep)
	if math.Abs(delta) < 1e-12 {
		return append(out, to)
	}
	// Chord sagitta of a regular arc: r*(1-cos(a/2)) <= tol, hence
	// a <= 2*acos(1-tol/r).
	safe := math.Max(1e-12, math.Min(1, tol/r))
	maxStep := 2 * math.Acos(1-safe)
	if !(maxStep > 0) {
		maxStep = math.Abs(delta)
	}
	segments := int(math.Ceil(math.Abs(delta) / maxStep))
	if segments < 1 {
		segments = 1
	}

	start := math.Atan2(from.Y-center.Y, from.X-center.X)
	step := delta / float64(segments)
	for k := 1; k < segments; k++ {
		a := start + step*float64(k)
		out = append(out, Point{center.X + r*math.Cos(a), center.Y + r*math.Sin(a)})
	}
	return append(out, to)
}

// discretizeCircle returns a full counter-clockwise circle polygon, again
// sized from the maximum chord error rather than a fixed sample count.
func discretizeCircle(center Point, r, tol float64) []Point {
	safe := math.Max(1e-12, math.Min(1, tol/r))
	maxStep := 2 * math.Acos(1-safe)
	segments := int(math.Ceil(2 * math.Pi / maxStep))
	if segments < 3 {
		segments = 3
	}
	points := make([]Point, 0, segments)
	for k := 0; k < segments; k++ {
		a := 2 * math.Pi * float64(k) / float64(segments)
		points = append(points, Point{center.X + r*math.Cos(a), center.Y + r*math.Sin(a)})
	}
	return points
}

//
// --- small geometry helpers -------------------------------------------------
//

func sub(a, b Point) Point     { return Point{a.X - b.X, a.Y - b.Y} }
func dot(a, b Point) float64   { return a.X*b.X + a.Y*b.Y }
func cross(a, b Point) float64 { return a.X*b.Y - a.Y*b.X }

func dist(a, b Point) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func lerpPoint(a, b Point, t float64) Point {
	return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}

func unit(v Point) Point {
	length := math.Hypot(v.X, v.Y)
	if length < 1e-18 {
		return Point{}
	}
	return Point{v.X / length, v.Y / length}
}

// rotate90 returns the screen-coordinate left normal of tangent v, i.e. the
// normal obtained by rotating v 90 degrees clockwise in a y-down frame.
func rotate90(v Point) Point { return Point{v.Y, -v.X} }

func angleBetween(a, b Point) float64 {
	cos := dot(a, b)
	return math.Acos(math.Max(-1, math.Min(1, cos)))
}

// signedAngle returns the signed angle from a to b in [-pi, pi].
func signedAngle(a, b Point) float64 {
	return math.Atan2(cross(a, b), dot(a, b))
}

func wrapAngle(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a < -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

const epsilon = 1e-10

func approxEqual(a, b Point) bool {
	return dist(a, b) <= epsilon
}

func approxEqualUnit(a, b Point) bool {
	return math.Abs(dot(a, b)-1) <= 1e-8
}

func dedupe(points []Point) []Point {
	if len(points) < 2 {
		return points
	}
	out := points[:0]
	for _, p := range points {
		if len(out) == 0 || !approxEqual(out[len(out)-1], p) {
			out = append(out, p)
		}
	}
	if len(out) > 1 && approxEqual(out[0], out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

func reversePoints(points []Point) []Point {
	out := make([]Point, len(points))
	for i, p := range points {
		out[len(points)-1-i] = p
	}
	return out
}

// signedArea returns twice the signed area of a polygon loop.
func signedArea(points []Point) float64 {
	area := 0.0
	for i := range points {
		j := (i + 1) % len(points)
		area += cross(points[i], points[j])
	}
	return area
}

func orientCCW(points []Point) {
	if signedArea(points) < 0 {
		reverseInPlace(points)
	}
}

func orientCW(points []Point) {
	if signedArea(points) > 0 {
		reverseInPlace(points)
	}
}

func reverseInPlace(points []Point) {
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
}

// flattenCubic appends the flattened interior/end vertices of c (its start is
// already held by the caller).
func flattenCubic(c Cubic, tol float64, depth int, out *[]sample) {
	if flatEnough(c, tol) {
		*out = append(*out, sample{point: c.P3, tan: robustTangent(c, 1)})
		return
	}
	if depth >= maxSubdiv {
		// Recursion cap reached only for a near-exact cusp: emit the fold
		// point twice with opposite tangents so it is round-capped below.
		inTan := robustTangent(c, 0)
		outTan := robustTangent(c, 1)
		if math.Hypot(inTan.X, inTan.Y) < 0.5 || math.Hypot(outTan.X, outTan.Y) < 0.5 {
			inTan, outTan = Point{-1, 0}, Point{1, 0}
		}
		*out = append(*out,
			sample{point: c.P0, tan: inTan, outTan: inTan, cusp: true},
			sample{point: c.P3, tan: outTan, outTan: outTan, cusp: true})
		return
	}
	a, b := splitCubic(c, 0.5)
	flattenCubic(a, tol, depth+1, out)
	flattenCubic(b, tol, depth+1, out)
}

// robustTangent returns the unit derivative direction at t, falling back to
// the chord direction when the derivative itself vanishes (an exact cusp).
func robustTangent(c Cubic, t float64) Point {
	if v := cubicTangent(c, t); math.Hypot(v.X, v.Y) > 0.5 {
		return v
	}
	if chord := unit(sub(c.P3, c.P0)); math.Hypot(chord.X, chord.Y) > 0.5 {
		return chord
	}
	return Point{}
}

// repairTangents fills any remaining zero tangents (zero-length end pieces)
// from the nearest valid neighbor.
func repairTangents(s []sample) {
	for i := range s {
		if math.Hypot(s[i].tan.X, s[i].tan.Y) > 0.5 {
			continue
		}
		for d := 1; d < len(s); d++ {
			found := false
			for _, j := range []int{i - d, i + d} {
				if j >= 0 && j < len(s) && math.Hypot(s[j].tan.X, s[j].tan.Y) > 0.5 {
					s[i].tan = s[j].tan
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
}

// insertCuspSplits detects reverse foldbacks between adjacent flattened
// vertices and duplicates the fold point: the first copy keeps the incoming
// direction and closes the outgoing run, the second keeps the outgoing
// direction and opens the next run. Both are round-capped later.
func insertCuspSplits(s []sample) []sample {
	var out []sample
	for i, cur := range s {
		if i > 0 && dot(s[i-1].tan, cur.tan) <= cuspCos {
			out = append(out,
				sample{point: cur.point, tan: s[i-1].tan, outTan: s[i-1].tan, cusp: true},
				sample{point: cur.point, tan: cur.tan, outTan: cur.tan, cusp: true})
			continue
		}
		if cur.corner && cur.outTan != (Point{}) && dot(cur.tan, cur.outTan) <= cuspCos {
			out = append(out,
				sample{point: cur.point, tan: cur.tan, outTan: cur.tan, cusp: true},
				sample{point: cur.point, tan: cur.outTan, outTan: cur.outTan, cusp: true})
			continue
		}
		out = append(out, cur)
	}
	return out
}

// flatEnough decides whether c may be replaced by its P0-P3 chord. It combines
// the classic control-point chord error with a curvature guard so that tight
// curls are subdivided even when their chord error happens to be small.
func flatEnough(c Cubic, tol float64) bool {
	chord := sub(c.P3, c.P0)
	chordLen := math.Hypot(chord.X, chord.Y)

	d1 := sub(c.P1, c.P0)
	d2 := sub(c.P2, c.P1)
	d3 := sub(c.P3, c.P2)
	if chordLen < 1e-14 {
		// Degenerate (near-loop) chord: size by the control polygon.
		return math.Hypot(d1.X, d1.Y)+math.Hypot(d2.X, d2.Y)+math.Hypot(d3.X, d3.Y) <= tol
	}

	// Distance of the two interior control points from the chord.
	ux, uy := chord.X/chordLen, chord.Y/chordLen
	err1 := math.Abs(d1.X*uy - d1.Y*ux)
	err2 := math.Abs((sub(c.P2, c.P0)).X*uy - (sub(c.P2, c.P0)).Y*ux)
	if math.Max(err1, err2) > tol {
		return false
	}

	// Curvature guard: the tangent must not turn by more than the angle
	// subtended by one tolerance-sized chord.
	t0 := cubicTangent(c, 0)
	t1 := cubicTangent(c, 1)
	if math.Hypot(t0.X, t0.Y) < 0.5 || math.Hypot(t1.X, t1.Y) < 0.5 {
		// A vanishing derivative signals an exact cusp inside the segment.
		return false
	}
	maxTurn := 2 * math.Asin(math.Min(1, tol/(2*chordLen)))
	if angleBetween(t0, t1) > maxTurn {
		return false
	}
	return true
}

// splitCubic splits c at parameter t using de Casteljau's algorithm.
func splitCubic(c Cubic, t float64) (Cubic, Cubic) {
	p01 := lerpPoint(c.P0, c.P1, t)
	p12 := lerpPoint(c.P1, c.P2, t)
	p23 := lerpPoint(c.P2, c.P3, t)
	p012 := lerpPoint(p01, p12, t)
	p123 := lerpPoint(p12, p23, t)
	p0123 := lerpPoint(p012, p123, t)
	return Cubic{P0: c.P0, P1: p01, P2: p012, P3: p0123},
		Cubic{P0: p0123, P1: p123, P2: p23, P3: c.P3}
}

// cubicTangent returns the unit derivative direction at t.
func cubicTangent(c Cubic, t float64) Point {
	u := 1 - t
	d := Point{
		X: 3 * (u*u*(c.P1.X-c.P0.X) + 2*u*t*(c.P2.X-c.P1.X) + t*t*(c.P3.X-c.P2.X)),
		Y: 3 * (u*u*(c.P1.Y-c.P0.Y) + 2*u*t*(c.P2.Y-c.P1.Y) + t*t*(c.P3.Y-c.P2.Y)),
	}
	return unit(d)
}

func polylineLength(s []sample) float64 {
	total := 0.0
	for i := 1; i < len(s); i++ {
		total += dist(s[i-1].point, s[i].point)
	}
	return total
}

func strokeSubpath(sub subpath, radius float64, opt Options) []Contour {
	samples := flattenSubpath(sub, opt.Tolerance)

	// A path that collapsed to a single location is stroked as a round dot,
	// the only well-defined cap for zero-length centerlines.
	if len(samples) < 2 || polylineLength(samples) == 0 {
		if radius == 0 {
			return nil
		}
		center := sub.segs[0].P0
		if len(samples) > 0 {
			center = samples[0].point
		}
		return []Contour{{Points: discretizeCircle(center, radius, opt.Tolerance)}}
	}

	runs := splitAtCusps(samples, sub.closed)
	var contours []Contour
	for _, run := range runs {
		if polylineLength(run.samples) == 0 {
			if radius > 0 {
				contours = append(contours, Contour{
					Points: discretizeCircle(run.samples[0].point, radius, opt.Tolerance),
				})
			}
			continue
		}
		runClosed := sub.closed && len(runs) == 1
		startCap, endCap := CapButt, CapButt
		if !runClosed {
			if run.startCusp || run.endCusp {
				// Foldback cuts are always sealed with round caps.
				startCap, endCap = CapRound, CapRound
			} else {
				startCap, endCap = opt.Cap, opt.Cap
			}
		}
		contours = append(contours, strokeRun(run.samples, runClosed, startCap, endCap, radius, opt)...)
	}
	return contours
}

// cuspRun is one cusp-free stretch of the flattened centerline.
type cuspRun struct {
	samples            []sample
	startCusp, endCusp bool
}

func splitAtCusps(samples []sample, closed bool) []cuspRun {
	var runs []cuspRun
	start := 0
	startCusp := false
	for i := 1; i < len(samples)-1; i++ {
		if !(samples[i].cusp && samples[i+1].cusp && approxEqual(samples[i].point, samples[i+1].point)) {
			continue
		}
		// The duplicated fold point terminates one run and opens the next.
		runs = append(runs, cuspRun{samples: samples[start : i+1], startCusp: startCusp, endCusp: true})
		start = i + 1
		startCusp = true
		i++
	}
	if start == 0 {
		return []cuspRun{{samples: samples}}
	}
	runs = append(runs, cuspRun{samples: samples[start:], startCusp: startCusp})

	// Drop zero-length runs (a fold point with no segment of its own) and
	// transfer their cusp markers to the neighboring real runs.
	kept := runs[:0]
	for i, r := range runs {
		if polylineLength(r.samples) > 0 {
			kept = append(kept, r)
			continue
		}
		if len(kept) > 0 {
			kept[len(kept)-1].endCusp = true
		}
		if i+1 < len(runs) {
			runs[i+1].startCusp = true
		}
	}
	return kept
}

func strokeRun(samples []sample, closed bool, startCap, endCap Cap, radius float64, opt Options) []Contour {
	if radius == 0 {
		return nil
	}
	left := offsetSide(samples, closed, radius, opt)
	right := offsetSide(samples, closed, -radius, opt)
	if len(left) < 2 || len(right) < 2 {
		return nil
	}

	if closed {
		outer := dedupe(append([]Point(nil), left...))
		inner := dedupe(reversePoints(right))
		orientCW(outer)
		orientCCW(inner)
		return []Contour{{Points: outer}, {Points: inner}}
	}

	var loop []Point
	loop = append(loop, left...)
	loop = appendCap(loop, samples[len(samples)-1], endCap, radius, +1, opt.Tolerance)
	for i := len(right) - 1; i >= 0; i-- {
		loop = append(loop, right[i])
	}
	loop = appendCap(loop, samples[0], startCap, radius, -1, opt.Tolerance)

	loop = dedupe(loop)
	if len(loop) < 3 {
		return nil
	}
	// Assembled left-forward/right-backward, the boundary already winds
	// clockwise in the screen-oriented coordinate system.
	orientCW(loop)
	return []Contour{{Points: loop}}
}

// offsetSide constructs one offset boundary. signedRadius is positive for the
// left side (normal = tangent rotated +90 degrees) and negative for the right.
func offsetSide(samples []sample, closed bool, signedRadius float64, opt Options) []Point {
	n := len(samples)
	normals := make([]Point, n)
	for i, s := range samples {
		normals[i] = rotate90(s.tan)
	}

	var out []Point
	add := func(p Point) {
		if len(out) == 0 || !approxEqual(out[len(out)-1], p) {
			out = append(out, p)
		}
	}
	offsetPoint := func(center Point, normal Point) Point {
		return Point{center.X + signedRadius*normal.X, center.Y + signedRadius*normal.Y}
	}

	for i := 0; i < n; i++ {
		isVertex := closed || (i > 0 && i < n-1)
		if !isVertex {
			add(offsetPoint(samples[i].point, normals[i]))
			continue
		}

		// At the vertex the incoming edge ends along n0 and the outgoing
		// edge begins along n1; non-corner vertices carry a single tangent.
		n0 := normals[i]
		n1 := n0
		if samples[i].outTan != (Point{}) {
			n1 = rotate90(samples[i].outTan)
		}
		bisector := Point{n0.X + n1.X, n0.Y + n1.Y}
		bLen := math.Hypot(bisector.X, bisector.Y)
		join := effectiveJoin(samples[i], opt, n0, n1, signedRadius)

		if bLen < 1e-12 {
			// Opposite normals: bevel across the two offset points. Genuine
			// foldback cusps are handled by round caps at the run level.
			add(offsetPoint(samples[i].point, n0))
			add(offsetPoint(samples[i].point, n1))
			continue
		}

		// Full miter length (outer tip to the inner corner, SVG semantics):
		// 2r/cos(theta/2). The miter-ratio limit multiplies stroke width.
		miterLen := math.Abs(signedRadius) * 4 / bLen
		limitDist := opt.MiterLimit * opt.Width
		if join == JoinMiter && miterLen <= limitDist {
			scale := signedRadius / bLen
			add(Point{samples[i].point.X + scale*bisector.X,
				samples[i].point.Y + scale*bisector.Y})
			continue
		}

		switch join {
		case JoinRound:
			from := offsetPoint(samples[i].point, n0)
			to := offsetPoint(samples[i].point, n1)
			add(from)
			turn := signedAngle(n0, n1)
			sweep := turn
			if signedRadius < 0 {
				sweep = -turn
			}
			out = appendArc(out, samples[i].point, from, to, sweep, math.Abs(signedRadius), opt.Tolerance)
		default: // Bevel, including miter-limit fallback.
			add(offsetPoint(samples[i].point, n0))
			add(offsetPoint(samples[i].point, n1))
		}
	}
	return out
}

// effectiveJoin resolves the join style at one vertex. Interior subdivision
// vertices are near-collinear and always miter. At real corners the inner
// side of a turn is beveled (its offset edges intersect behind the vertex),
// while the outer side honors the requested join style; an over-limit miter
// on the outer side degrades to a bevel.
func effectiveJoin(s sample, opt Options, n0, n1 Point, signedRadius float64) Join {
	if !s.corner {
		return JoinMiter
	}
	// On the inner side of a turn the two offset edges converge, so the
	// successive normals point into the turn (positive dot product) and a
	// miter would land behind the centerline vertex; bevel there. On the
	// outer side the normals diverge (negative dot product).
	if dot(n0, n1) > 0 {
		return JoinBevel
	}
	return opt.Join
}
