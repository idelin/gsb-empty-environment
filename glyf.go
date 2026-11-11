package ttfglyph

import "math"

// Outline is the fully materialized outline of one glyph in font units.
type Outline struct {
	Contours []Contour
}

// Simple-glyph flag bits.
const (
	flagOnCurve = 0x01
	flagXShort  = 0x02
	flagYShort  = 0x04
	flagRepeat  = 0x08
	flagSameX   = 0x10
	flagSameY   = 0x20
)

// Composite-glyph flag bits.
const (
	compArg12Words      = 0x0001
	compArgsAreXY       = 0x0002
	compRoundToGrid     = 0x0004
	compHasScale        = 0x0008
	compMoreComponents  = 0x0020
	compHasXYScale      = 0x0040
	compHas2x2          = 0x0080
	compHasInstructions = 0x0100
	compUseMyMetrics    = 0x0200
	compOverlap         = 0x0400
)

// component is one decoded composite-glyph component descriptor.
type component struct {
	glyphIndex uint16
	arg1, arg2 int32
	argsAreXY  bool
	affine     [4]float64 // A, B, C, D: x'=A*x+C*y, y'=B*x+D*y
}

type rawGlyph struct {
	contours []Contour
	flat     []Point // all raw points, contours concatenated
}

// GlyphOutline resolves glyph gid (including composite glyphs) and returns its
// contours with all implicit quadratic midpoints materialized.
func (f *Font) GlyphOutline(gid uint16) (*Outline, error) {
	raw, err := f.loadRawGlyph(gid, 0)
	if err != nil {
		return nil, err
	}
	out := &Outline{Contours: make([]Contour, 0, len(raw.contours))}
	total := 0
	for _, c := range raw.contours {
		expanded := expandContour(c.Points)
		total += len(expanded)
		if total > maxTotalPoints {
			return nil, corruptf("glyph %d exceeds %d expanded points", gid, maxTotalPoints)
		}
		out.Contours = append(out.Contours, Contour{Points: expanded})
	}
	return out, nil
}

func (f *Font) loadRawGlyph(gid uint16, depth int) (*rawGlyph, error) {
	if depth > maxRecursion {
		return nil, corruptf("glyph %d composite recursion exceeds depth %d", gid, maxRecursion)
	}
	start, end, err := f.glyphRange(gid)
	if err != nil {
		return nil, err
	}
	if start == end {
		return &rawGlyph{contours: []Contour{}}, nil
	}
	glyf, ok := f.tables["glyf"]
	if !ok {
		return nil, ErrNotFound
	}
	b, err := f.readTable(glyf, start, end-start)
	if err != nil {
		return nil, err
	}
	c := newCursor(b)
	numContours, err := c.i16()
	if err != nil {
		return nil, err
	}
	switch {
	case numContours == 0:
		return &rawGlyph{contours: []Contour{}}, nil
	case numContours > 0:
		return f.parseSimpleGlyph(c, int(numContours))
	case numContours == -1:
		return f.parseCompositeGlyph(c, depth)
	default:
		return nil, corruptf("glyph %d has invalid numberOfContours %d", gid, numContours)
	}
}

func (f *Font) parseSimpleGlyph(c *cursor, numContours int) (*rawGlyph, error) {
	if numContours > maxContours {
		return nil, corruptf("simple glyph declares %d contours (limit %d)", numContours, maxContours)
	}
	// Bounding box: xMin, yMin, xMax, yMax.
	for i := 0; i < 4; i++ {
		if _, err := c.i16(); err != nil {
			return nil, err
		}
	}

	endPts := make([]uint16, numContours)
	for i := range endPts {
		v, err := c.u16()
		if err != nil {
			return nil, err
		}
		if i > 0 && v <= endPts[i-1] {
			return nil, corruptf("endPtsOfContours not strictly increasing at %d", i)
		}
		endPts[i] = v
	}
	nPoints := int(endPts[numContours-1]) + 1
	if nPoints > maxPoints {
		return nil, corruptf("simple glyph declares %d points (limit %d)", nPoints, maxPoints)
	}
	if nPoints > maxTotalPoints {
		return nil, corruptf("simple glyph exceeds %d total points", maxTotalPoints)
	}

	instrLen, err := c.u16()
	if err != nil {
		return nil, err
	}
	if c.pos+int(instrLen) > len(c.b) {
		return nil, corruptf("instruction length %d exceeds glyph data", instrLen)
	}
	c.pos += int(instrLen)

	flags := make([]byte, 0, nPoints)
	for len(flags) < nPoints {
		flag, err := c.u8()
		if err != nil {
			return nil, err
		}
		count := 1
		if flag&flagRepeat != 0 {
			rep, err := c.u8()
			if err != nil {
				return nil, err
			}
			if rep == 0 {
				return nil, corruptf("zero repeat count in glyph flags")
			}
			count = int(rep) + 1
			if len(flags)+count > nPoints {
				return nil, corruptf("repeated flags overflow point count")
			}
		}
		for i := 0; i < count; i++ {
			flags = append(flags, flag)
		}
	}

	xs, err := readCoordinateDeltas(c, flags, flagXShort, flagSameX)
	if err != nil {
		return nil, err
	}
	ys, err := readCoordinateDeltas(c, flags, flagYShort, flagSameY)
	if err != nil {
		return nil, err
	}

	points := make([]Point, nPoints)
	var x, y int32
	for i, flag := range flags {
		x += xs[i]
		y += ys[i]
		points[i] = Point{X: x, Y: y, OnCurve: flag&flagOnCurve != 0}
	}

	raw := &rawGlyph{contours: make([]Contour, numContours), flat: points}
	start := 0
	for i, end := range endPts {
		raw.contours[i] = Contour{Points: points[start : int(end)+1 : int(end)+1]}
		start = int(end) + 1
	}
	return raw, nil
}

// readCoordinateDeltas reads one coordinate axis (x or y) using the TrueType
// delta encoding driven by the per-point flags.
func readCoordinateDeltas(c *cursor, flags []byte, shortBit, sameBit byte) ([]int32, error) {
	deltas := make([]int32, len(flags))
	for i, flag := range flags {
		switch {
		case flag&shortBit != 0:
			v, err := c.u8()
			if err != nil {
				return nil, err
			}
			d := int32(v)
			if flag&sameBit == 0 {
				d = -d
			}
			deltas[i] = d
		case flag&sameBit != 0:
			// delta is zero
		default:
			v, err := c.i16()
			if err != nil {
				return nil, err
			}
			deltas[i] = int32(v)
		}
	}
	return deltas, nil
}

func (f *Font) parseCompositeGlyph(c *cursor, depth int) (*rawGlyph, error) {
	// Bounding box.
	for i := 0; i < 4; i++ {
		if _, err := c.i16(); err != nil {
			return nil, err
		}
	}

	var comps []component
	hasInstructions := false
	for {
		if len(comps) >= maxContours {
			return nil, corruptf("composite glyph declares too many components")
		}
		flags, err := c.u16()
		if err != nil {
			return nil, err
		}
		gid, err := c.u16()
		if err != nil {
			return nil, err
		}
		comp := component{
			glyphIndex: gid,
			affine:     [4]float64{1, 0, 0, 1},
			argsAreXY:  flags&compArgsAreXY != 0,
		}
		if flags&compHasInstructions != 0 {
			hasInstructions = true
		}
		if flags&compArg12Words != 0 {
			a, err := c.i16()
			if err != nil {
				return nil, err
			}
			b, err := c.i16()
			if err != nil {
				return nil, err
			}
			comp.arg1, comp.arg2 = int32(a), int32(b)
		} else {
			a, err := c.u8()
			if err != nil {
				return nil, err
			}
			b, err := c.u8()
			if err != nil {
				return nil, err
			}
			if comp.argsAreXY {
				comp.arg1, comp.arg2 = int32(int8(a)), int32(int8(b))
			} else {
				comp.arg1, comp.arg2 = int32(a), int32(b)
			}
		}

		scaleKinds := 0
		if flags&compHasScale != 0 {
			scaleKinds++
		}
		if flags&compHasXYScale != 0 {
			scaleKinds++
		}
		if flags&compHas2x2 != 0 {
			scaleKinds++
		}
		if scaleKinds > 1 {
			return nil, corruptf("component %d has multiple scale descriptions", gid)
		}
		switch {
		case flags&compHasScale != 0:
			s, err := readF2Dot14(c)
			if err != nil {
				return nil, err
			}
			comp.affine = [4]float64{s, 0, 0, s}
		case flags&compHasXYScale != 0:
			sx, err := readF2Dot14(c)
			if err != nil {
				return nil, err
			}
			sy, err := readF2Dot14(c)
			if err != nil {
				return nil, err
			}
			comp.affine = [4]float64{sx, 0, 0, sy}
		case flags&compHas2x2 != 0:
			values := [4]float64{}
			for i := range values {
				v, err := readF2Dot14(c)
				if err != nil {
					return nil, err
				}
				values[i] = v
			}
			// Stored order: xscale(A), scale01(B), scale10(C), yscale(D).
			comp.affine = [4]float64{values[0], values[1], values[2], values[3]}
		}
		comps = append(comps, comp)
		if flags&compMoreComponents == 0 {
			break
		}
	}
	if hasInstructions {
		// Composite instruction stream: a u16 length followed by the bytes.
		instrLen, err := c.u16()
		if err != nil {
			return nil, err
		}
		if c.pos+int(instrLen) > len(c.b) {
			return nil, corruptf("composite instruction length %d exceeds glyph", instrLen)
		}
		c.pos += int(instrLen)
	}

	// Resolve components after parsing every descriptor: point-alignment
	// references points produced by earlier components.
	raw := &rawGlyph{contours: []Contour{}}
	for _, comp := range comps {
		child, err := f.loadRawGlyph(comp.glyphIndex, depth+1)
		if err != nil {
			return nil, err
		}
		dx, dy := comp.arg1, comp.arg2
		if !comp.argsAreXY {
			if int(comp.arg1) >= len(child.flat) || int(comp.arg2) >= len(raw.flat) {
				return nil, corruptf("component %d point index out of range (%d,%d)",
					comp.glyphIndex, comp.arg1, comp.arg2)
			}
			src := transformPoint(child.flat[comp.arg1], comp.affine)
			dst := raw.flat[comp.arg2]
			dx = dst.X - src.X
			dy = dst.Y - src.Y
		}
		if len(raw.flat)+len(child.flat) > maxTotalPoints {
			return nil, corruptf("composite glyph exceeds %d accumulated points", maxTotalPoints)
		}
		for _, contour := range child.contours {
			moved := make([]Point, len(contour.Points))
			for i, p := range contour.Points {
				t := transformPoint(p, comp.affine)
				moved[i] = Point{X: t.X + dx, Y: t.Y + dy, OnCurve: p.OnCurve}
			}
			raw.contours = append(raw.contours, Contour{Points: moved})
			raw.flat = append(raw.flat, moved...)
		}
	}
	return raw, nil
}

func readF2Dot14(c *cursor) (float64, error) {
	v, err := c.i16()
	if err != nil {
		return 0, err
	}
	return float64(v) / 16384.0, nil
}

func transformPoint(p Point, m [4]float64) Point {
	x := math.Round(m[0]*float64(p.X) + m[2]*float64(p.Y))
	y := math.Round(m[1]*float64(p.X) + m[3]*float64(p.Y))
	return Point{X: int32(x), Y: int32(y), OnCurve: p.OnCurve}
}

// expandContour converts TrueType on/off-curve point sequences into a
// renderer-friendly form by inserting the implied on-curve midpoint between
// every pair of consecutive off-curve points. The contour is a closed loop,
// so the last point is adjacent to the first.
func expandContour(pts []Point) []Point {
	n := len(pts)
	if n == 0 {
		return nil
	}
	firstOn := -1
	for i, p := range pts {
		if p.OnCurve {
			firstOn = i
			break
		}
	}

	var out []Point
	if firstOn == -1 {
		// All points are off-curve: start at the implied midpoint of the last
		// and first control points, then walk the n edges once.
		out = append(out, midpoint(pts[n-1], pts[0]))
		for i := 0; i < n; i++ {
			cur := pts[i]
			next := pts[(i+1)%n]
			out = append(out, cur)
			if !next.OnCurve {
				out = append(out, midpoint(cur, next))
			}
		}
		return out
	}

	// Start on an on-curve point and walk every edge exactly once. An
	// implied on-curve point exists only when both ends of an edge are
	// off-curve; an on/off edge is already a complete quadratic segment.
	for k := 0; k < n; k++ {
		i := (firstOn + k) % n
		cur := pts[i]
		next := pts[(i+1)%n]
		out = append(out, cur)
		if !cur.OnCurve && !next.OnCurve {
			out = append(out, midpoint(cur, next))
		}
	}
	return out
}

// midpoint returns the implied on-curve point between two off-curve controls.
// Go integer division truncates toward zero, matching the (a+b)/2 convention
// used by common TrueType rasterizers for integer font coordinates.
func midpoint(a, b Point) Point {
	return Point{
		X:       (a.X + b.X) / 2,
		Y:       (a.Y + b.Y) / 2,
		OnCurve: true,
	}
}
