package taskpkg

import (
	"errors"
	"math"
)

// Options controls ray integration. Zero-valued fields are replaced with
// documented defaults by NewIntegrator.
type Options struct {
	// MaxSteps bounds the number of trilinear coefficient evaluations
	// (sampling steps) spent on a single ray.
	MaxSteps int
	// ColorTol is the absolute tolerance on the accumulated emitted linear
	// RGB radiance.
	ColorTol float64
	// TransTol is the absolute tolerance on the accumulated transmittance.
	TransTol float64
	// MaxDepth bounds adaptive subdivision within a cell.
	MaxDepth int
	// MinSegment is the smallest physical ray-parameter length that may be
	// subdivided; it prevents infinite refinement under floating point noise.
	MinSegment float64
	// Background is the radiance seen after the ray leaves the volume.
	Background RGB
}

// Integrator integrates emission-absorption rays through a Grid. It is safe
// for concurrent use because Integrate mutates only per-call state.
type Integrator struct {
	grid *Grid
	opt  Options
}

// Result is the outcome of integrating one ray.
type Result struct {
	// Color is the front-to-back emitted radiance plus the attenuated
	// background.
	Color RGB
	// Trans is the transmittance through the whole volume, in [0,1].
	Trans float64
	// ColorErr and TransErr are conservative remaining-error estimates for
	// the emitted radiance (excluding background) and transmittance. They are
	// nonzero in particular when the step budget was exhausted.
	ColorErr RGB
	TransErr float64
	// Err is the infinity-norm combination of the per-channel color error
	// and the transmittance error.
	Err float64
	// Steps is the number of coefficient evaluations actually consumed.
	Steps int
	// Exhausted is true when MaxSteps was reached before full convergence.
	Exhausted bool
	// Hit is false when the ray never intersects the grid bounding box.
	Hit bool
}

// NewIntegrator validates options and constructs an Integrator.
func NewIntegrator(grid *Grid, opt Options) (*Integrator, error) {
	if grid == nil {
		return nil, errors.New("grid is nil")
	}
	if opt.MaxSteps == 0 {
		opt.MaxSteps = 4096
	}
	if opt.MaxSteps < 0 {
		return nil, errors.New("MaxSteps must be non-negative")
	}
	if opt.ColorTol == 0 {
		opt.ColorTol = 1e-4
	}
	if opt.TransTol == 0 {
		opt.TransTol = 1e-5
	}
	if opt.ColorTol <= 0 || opt.TransTol <= 0 {
		return nil, errors.New("tolerances must be positive")
	}
	if opt.MaxDepth == 0 {
		opt.MaxDepth = 24
	}
	if opt.MinSegment == 0 {
		opt.MinSegment = 1e-12
	}
	return &Integrator{grid: grid, opt: opt}, nil
}

// Integrate marches origin + t*dir through the grid. dir need not be
// normalized; all distances scale with |dir|. A ray missing the bounding box
// returns Hit=false with zero radiance and unit transmittance.
func (in *Integrator) Integrate(origin, dir Vec3) Result {
	g := in.grid
	if dir == (Vec3{}) {
		return Result{Hit: false, Trans: 1}
	}

	tEnter, tExit, hit := slabBox(origin, dir,
		g.xmin, g.xmax, g.ymin, g.ymax, g.zmin, g.zmax)
	if !hit || tExit <= 0 {
		return Result{Hit: false, Trans: 1}
	}
	if tEnter < 0 {
		tEnter = 0
	}

	entry := Vec3{
		origin[0] + tEnter*dir[0],
		origin[1] + tEnter*dir[1],
		origin[2] + tEnter*dir[2],
	}
	i, _, j, _, k, _ := g.locate(entry)
	eps := 1e-12 * max(1, math.Abs(tExit-tEnter))

	// Per-cell tolerances keep the accumulated worst-case error within the
	// requested global tolerances; a ray crosses at most this many cells.
	maxCells := (g.nx - 1) + (g.ny - 1) + (g.nz - 1)
	ctx := segContext{
		grid:          g,
		origin:        origin,
		dir:           dir,
		dirLen:        vecLen(dir),
		opt:           in.opt,
		budget:        in.opt.MaxSteps,
		localColorTol: in.opt.ColorTol / float64(maxCells),
		localTransTol: in.opt.TransTol / float64(maxCells),
	}

	color := RGB{}
	trans := 1.0
	var colorErr RGB
	transErr := 0.0
	exhausted := false

	t := tEnter
	for t < tExit-eps {
		if i < 0 || i >= g.nx-1 || j < 0 || j >= g.ny-1 || k < 0 || k >= g.nz-1 {
			break
		}
		tb := nextBoundary(t, dir, origin, g, i, j, k)
		if tb > tExit {
			tb = tExit
		}
		if tb <= t {
			// Floating-point stall on a boundary; advance the index instead.
			i, j, k = advanceCell(i, j, k, dir, g)
			continue
		}

		seg, locColorErr, locTransErr, out := ctx.integrateCell(t, tb)

		color = RGB{
			color[0] + trans*seg.C[0],
			color[1] + trans*seg.C[1],
			color[2] + trans*seg.C[2],
		}
		// Local errors reach the camera attenuated by the current transmittance.
		colorErr = RGB{
			colorErr[0] + trans*locColorErr[0],
			colorErr[1] + trans*locColorErr[1],
			colorErr[2] + trans*locColorErr[2],
		}
		// Product trans*seg.T: the new factor's error is scaled by the
		// transmittance accumulated before this cell.
		transErr = transErr*seg.T + trans*locTransErr
		trans *= seg.T

		if out {
			exhausted = true
			// Bound the contribution of every cell still ahead.
			_, cb, tbnd := g.remainder((tExit - tb) * ctx.dirLen)
			colorErr = RGB{
				colorErr[0] + trans*cb[0],
				colorErr[1] + trans*cb[1],
				colorErr[2] + trans*cb[2],
			}
			transErr = transErr + trans*tbnd
			break
		}

		t = tb
		i, j, k = advanceCell(i, j, k, dir, g)
	}

	for c := 0; c < 3; c++ {
		color[c] += trans * in.opt.Background[c]
	}
	trans = clamp01(trans)

	err := transErr
	for c := 0; c < 3; c++ {
		err = max(err, colorErr[c])
	}
	return Result{
		Color:     color,
		Trans:     trans,
		ColorErr:  colorErr,
		TransErr:  transErr,
		Err:       err,
		Steps:     ctx.steps,
		Exhausted: exhausted,
		Hit:       true,
	}
}

// segContext carries the shared per-ray state through recursive subdivision.
type segContext struct {
	grid          *Grid
	origin        Vec3
	dir           Vec3
	dirLen        float64
	opt           Options
	steps         int
	budget        int
	localColorTol float64
	localTransTol float64
}

// pointAt returns the ray position at parameter t.
func (c *segContext) pointAt(t float64) Vec3 {
	return Vec3{
		c.origin[0] + t*c.dir[0],
		c.origin[1] + t*c.dir[1],
		c.origin[2] + t*c.dir[2],
	}
}

// sample evaluates the trilinear coefficients at ray parameter t and charges
// one step against the budget. The boolean reports budget exhaustion.
func (c *segContext) sample(t float64) (Coeff, bool) {
	if c.steps >= c.budget {
		return Coeff{}, true
	}
	c.steps++
	return c.grid.CoeffAt(c.pointAt(t)), false
}

// segment evaluates the constant-coefficient solution for a parameter-space
// span h, converting it to the physical arc length h*|dir| so the result is
// invariant to the magnitude of the supplied direction vector.
func (c *segContext) segment(sigma float64, em RGB, h float64) Segment {
	return analyticSegment(sigma, em, h*c.dirLen)
}

// integrateCell integrates [t0,t1] inside one grid cell using adaptive
// subdivision with analytic constant-coefficient segments.
func (c *segContext) integrateCell(t0, t1 float64) (Segment, RGB, float64, bool) {
	h := t1 - t0
	a, out := c.sample(t0)
	if out {
		seg, cb, tbnd := c.grid.remainder(h * c.dirLen)
		return seg, cb, tbnd, true
	}
	b, out := c.sample(t1)
	if out {
		seg, cb, tbnd := c.grid.remainder(h * c.dirLen)
		return seg, cb, tbnd, true
	}
	m, out := c.sample(t0 + h/2)
	if out {
		best := compose(
			c.segment(a.Sigma, a.Em, h/2),
			c.segment(b.Sigma, b.Em, h/2),
		)
		dColor, dTrans := segmentDiff(best, c.segment(a.Sigma, a.Em, h))
		return best, dColor, dTrans, true
	}
	return c.recurse(t0, t1, a, b, m, 0)
}

// recurse compares the whole-segment estimate (one analytic segment using the
// midpoint coefficients) against the two-half-segment estimate (one analytic
// segment per half, using the quarter-point coefficients) and subdivides
// while their discrepancy exceeds the local tolerance. Coefficients at both
// ends and at the midpoint are already known and reused.
func (c *segContext) recurse(t0, t1 float64, ca, cb, cm Coeff, depth int) (Segment, RGB, float64, bool) {
	h := t1 - t0
	whole := c.segment(cm.Sigma, cm.Em, h)

	canRefine := depth < c.opt.MaxDepth &&
		h > c.opt.MinSegment &&
		c.steps+2 <= c.budget
	if !canRefine {
		// Best available estimate without further samples: two half segments
		// built from the known endpoints; their gap to the midpoint estimate
		// is a measured local error.
		best := compose(
			c.segment(ca.Sigma, ca.Em, h/2),
			c.segment(cb.Sigma, cb.Em, h/2),
		)
		dColor, dTrans := segmentDiff(best, whole)
		return best, dColor, dTrans, c.steps >= c.budget
	}

	qL, outL := c.sample(t0 + h/4)
	qR, outR := c.sample(t0 + 3*h/4)
	if outL || outR {
		best := compose(
			c.segment(ca.Sigma, ca.Em, h/2),
			c.segment(cb.Sigma, cb.Em, h/2),
		)
		dColor, dTrans := segmentDiff(best, whole)
		return best, dColor, dTrans, true
	}
	halves := compose(
		c.segment(qL.Sigma, qL.Em, h/2),
		c.segment(qR.Sigma, qR.Em, h/2),
	)
	dColor, dTrans := segmentDiff(halves, whole)
	if segmentConverged(dColor, dTrans, c.localColorTol, c.localTransTol) {
		return halves, dColor, dTrans, false
	}

	tm := t0 + h/2
	left, lColorErr, lTransErr, exL := c.recurse(t0, tm, ca, cm, qL, depth+1)
	right, rColorErr, rTransErr, exR := c.recurse(tm, t1, cm, cb, qR, depth+1)
	refined := compose(left, right)

	// Propagate child errors through front-to-back composition, then keep the
	// most conservative of the propagated and measured discrepancy.
	propColor := RGB{
		lColorErr[0] + left.T*rColorErr[0],
		lColorErr[1] + left.T*rColorErr[1],
		lColorErr[2] + left.T*rColorErr[2],
	}
	propTrans := lTransErr*right.T + rTransErr
	dColor, dTrans = segmentDiff(refined, whole)
	return refined, maxRGB(propColor, dColor), max(propTrans, dTrans), exL || exR
}

// segmentConverged applies the local tolerances to the whole-versus-halves
// discrepancy.
func segmentConverged(dColor RGB, dTrans, colorTol, transTol float64) bool {
	if dTrans > transTol {
		return false
	}
	for c := 0; c < 3; c++ {
		if dColor[c] > colorTol {
			return false
		}
	}
	return true
}

// segmentDiff returns component-wise absolute discrepancies between two
// segment estimates.
func segmentDiff(a, b Segment) (RGB, float64) {
	return RGB{
			math.Abs(a.C[0] - b.C[0]),
			math.Abs(a.C[1] - b.C[1]),
			math.Abs(a.C[2] - b.C[2]),
		},
		math.Abs(a.T - b.T)
}
