// Package taskpkg provides tetrahedral interpolation for regular RGB 3D
// lookup tables (LUTs). It is intended to be called by a Go video rendering
// host that works with linear floating-point color.
//
// A LUT is a regular grid of RGB values. The table's domain defaults to the
// unit cube [0,1]^3, although callers may configure another regular domain.
// Colors are sampled with tetrahedral interpolation, not trilinear
// interpolation. Within a cell, the three fractional coordinates are ordered
// from largest to smallest; equal coordinates are ordered by channel (R, then
// G, then B). That ordering selects exactly one of the six tetrahedra that
// share the cell's black-to-white diagonal.
//
// Boundary handling is configurable. BoundaryClamp maps coordinates outside
// the domain onto the nearest domain face. BoundaryReject reports the first
// offending pixel and leaves a frame unchanged.
package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// Float is the set of frame element types handled by the frame helpers.
type Float interface {
	~float32 | ~float64
}

// RGB is a linear-light RGB color. Alpha is deliberately not represented:
// LUT processing never grades or validates alpha.
type RGB struct {
	R float64
	G float64
	B float64
}

// Color is an alias for RGB.
type Color = RGB

// BoundaryMode selects what happens when a color component is outside the
// LUT domain.
type BoundaryMode uint8

const (
	// BoundaryClamp clamps an out-of-domain component to the nearest domain
	// boundary. It is the default.
	BoundaryClamp BoundaryMode = iota
	// BoundaryReject rejects an out-of-domain component.
	BoundaryReject
)

const (
	// Clamp is an alias for BoundaryClamp.
	Clamp = BoundaryClamp
	// Reject is an alias for BoundaryReject.
	Reject = BoundaryReject
	// ClampBoundary and RejectBoundary are descriptive aliases.
	ClampBoundary  = BoundaryClamp
	RejectBoundary = BoundaryReject
)

func (m BoundaryMode) String() string {
	switch m {
	case BoundaryClamp:
		return "clamp"
	case BoundaryReject:
		return "reject"
	default:
		return fmt.Sprintf("BoundaryMode(%d)", uint8(m))
	}
}

// Layout describes the memory order of the RGB triples in the table.
type Layout uint8

const (
	// RedFastest stores triples with red varying fastest, then green, then
	// blue. For a table of size (nr, ng, nb), the node index is
	// ((b*ng)+g)*nr+r. This is the order used by common .cube files.
	RedFastest Layout = iota
	// BlueFastest stores triples with blue varying fastest, then green, then
	// red. For a table of size (nr, ng, nb), the node index is
	// ((r*ng)+g)*nb+b.
	BlueFastest
)

const (
	// LayoutRedFastest and LayoutBlueFastest are descriptive aliases.
	LayoutRedFastest  = RedFastest
	LayoutBlueFastest = BlueFastest
)

func (l Layout) String() string {
	switch l {
	case RedFastest:
		return "red-fastest"
	case BlueFastest:
		return "blue-fastest"
	default:
		return fmt.Sprintf("Layout(%d)", uint8(l))
	}
}

// Errors returned by this package. Errors that identify a pixel or sample
// unwrap to one of these sentinels.
var (
	ErrInvalidLUT   = errors.New("lut: invalid lookup table")
	ErrInvalidFrame = errors.New("lut: invalid frame")
	ErrInvalidColor = errors.New("lut: invalid color")
	ErrNonFinite    = errors.New("lut: non-finite color component")
	ErrOutOfRange   = errors.New("lut: color component outside LUT domain")
)

// ColorError describes a bad color component. Pixel is -1 for a direct
// sample, or the zero-based pixel index for a frame.
type ColorError struct {
	Pixel   int
	Channel int
	Value   float64
	Err     error
}

func (e *ColorError) Error() string {
	return fmt.Sprintf("%v: pixel %d channel %d value %v", e.Err, e.Pixel, e.Channel, e.Value)
}

// Unwrap makes errors.Is(err, ErrOutOfRange) and
// errors.Is(err, ErrNonFinite) work for ColorError values.
func (e *ColorError) Unwrap() error { return e.Err }

// Option configures a LUT. BoundaryMode, Layout, and the With* helpers are
// valid options.
type Option interface {
	apply(*lutConfig) error
}

type optionFunc func(*lutConfig) error

func (f optionFunc) apply(c *lutConfig) error { return f(c) }

func (m BoundaryMode) apply(c *lutConfig) error {
	if m != BoundaryClamp && m != BoundaryReject {
		return fmt.Errorf("%w: unknown boundary mode %d", ErrInvalidLUT, uint8(m))
	}
	c.boundary = m
	return nil
}

func (l Layout) apply(c *lutConfig) error {
	if l != RedFastest && l != BlueFastest {
		return fmt.Errorf("%w: unknown table layout %d", ErrInvalidLUT, uint8(l))
	}
	c.layout = l
	return nil
}

// WithBoundary selects clamp or reject boundary behavior.
func WithBoundary(mode BoundaryMode) Option {
	return optionFunc(func(c *lutConfig) error { return mode.apply(c) })
}

// WithLayout selects the table's memory order.
func WithLayout(layout Layout) Option {
	return optionFunc(func(c *lutConfig) error { return layout.apply(c) })
}

// WithDomain configures the inclusive RGB domain of the LUT grid. Each lower
// bound must be finite and strictly less than its upper bound.
func WithDomain(lower, upper [3]float64) Option {
	return optionFunc(func(c *lutConfig) error {
		for i := 0; i < 3; i++ {
			if math.IsNaN(lower[i]) || math.IsInf(lower[i], 0) ||
				math.IsNaN(upper[i]) || math.IsInf(upper[i], 0) {
				return fmt.Errorf("%w: domain bounds must be finite", ErrInvalidLUT)
			}
			if !(lower[i] < upper[i]) {
				return fmt.Errorf("%w: domain lower bound %v is not less than upper bound %v",
					ErrInvalidLUT, lower[i], upper[i])
			}
		}
		c.lower = lower
		c.upper = upper
		return nil
	})
}

type lutConfig struct {
	boundary BoundaryMode
	layout   Layout
	lower    [3]float64
	upper    [3]float64
}

func defaultConfig() lutConfig {
	return lutConfig{
		boundary: BoundaryClamp,
		layout:   RedFastest,
		lower:    [3]float64{0, 0, 0},
		upper:    [3]float64{1, 1, 1},
	}
}

// LUT is an immutable regular RGB lookup table. It is safe for concurrent
// reads after construction.
type LUT struct {
	sizeR int
	sizeG int
	sizeB int

	table  []float64
	lower  [3]float64
	upper  [3]float64
	scale  [3]float64
	layout Layout
	mode   BoundaryMode
}

// NewLUT builds a LUT from a regular RGB grid. sr, sg, and sb are the numbers
// of samples along the red, green, and blue axes and must each be at least 1.
//
// table may be one of:
//
//	[]float32, []float64          // 3 consecutive values per node
//	[][3]float32, [][3]float64    // one triple per node
//	[]RGB
//
// The number of nodes or values must match the dimensions exactly. Node
// values must be finite. The table is copied, so later changes to the caller's
// slice do not affect the LUT.
//
// Options may be supplied as BoundaryMode and Layout values, or as With*
// options. The defaults are BoundaryClamp, RedFastest, and the [0,1]^3
// domain.
func NewLUT(sizeR, sizeG, sizeB int, table any, opts ...Option) (*LUT, error) {
	if sizeR < 1 || sizeG < 1 || sizeB < 1 {
		return nil, fmt.Errorf("%w: dimensions must be at least 1, got (%d,%d,%d)",
			ErrInvalidLUT, sizeR, sizeG, sizeB)
	}

	nodes, err := checkedNodeCount(sizeR, sizeG, sizeB)
	if err != nil {
		return nil, err
	}
	const maxInt = int(^uint(0) >> 1)
	if nodes > maxInt/3 {
		return nil, fmt.Errorf("%w: grid has too many samples", ErrInvalidLUT)
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt.apply(&cfg); err != nil {
			return nil, err
		}
	}

	values, err := tableToFloat64(table, nodes)
	if err != nil {
		return nil, err
	}
	for i, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%w: node %d component %d is not finite", ErrInvalidLUT, i/3, i%3)
		}
	}

	l := &LUT{
		sizeR:  sizeR,
		sizeG:  sizeG,
		sizeB:  sizeB,
		table:  values,
		lower:  cfg.lower,
		upper:  cfg.upper,
		layout: cfg.layout,
		mode:   cfg.boundary,
	}
	for i := 0; i < 3; i++ {
		l.scale[i] = 1 / (l.upper[i] - l.lower[i])
	}
	return l, nil
}

// NewLUT32 is a typed convenience constructor for float32 tables.
func NewLUT32(sizeR, sizeG, sizeB int, table []float32, opts ...Option) (*LUT, error) {
	return NewLUT(sizeR, sizeG, sizeB, table, opts...)
}

// NewLUT64 is a typed convenience constructor for float64 tables.
func NewLUT64(sizeR, sizeG, sizeB int, table []float64, opts ...Option) (*LUT, error) {
	return NewLUT(sizeR, sizeG, sizeB, table, opts...)
}

// New is an alias for NewLUT.
func New(sizeR, sizeG, sizeB int, table any, opts ...Option) (*LUT, error) {
	return NewLUT(sizeR, sizeG, sizeB, table, opts...)
}

// NewLUT3D is NewLUT with the dimensions supplied as an array.
func NewLUT3D(size [3]int, table any, opts ...Option) (*LUT, error) {
	return NewLUT(size[0], size[1], size[2], table, opts...)
}

// NewGrid is an alias for NewLUT3D.
func NewGrid(size [3]int, table any, opts ...Option) (*LUT, error) {
	return NewLUT3D(size, table, opts...)
}

func checkedNodeCount(a, b, c int) (int, error) {
	// Go's int is wide enough for all realistic LUTs, but multiplication can
	// still wrap for malicious dimensions.
	const maxInt = int(^uint(0) >> 1)
	if a > maxInt/b {
		return 0, fmt.Errorf("%w: grid dimensions overflow", ErrInvalidLUT)
	}
	ab := a * b
	if c > maxInt/ab {
		return 0, fmt.Errorf("%w: grid dimensions overflow", ErrInvalidLUT)
	}
	return ab * c, nil
}

func tableToFloat64(table any, nodeCount int) ([]float64, error) {
	wantValues := nodeCount * 3
	switch t := table.(type) {
	case []float32:
		if len(t) != wantValues {
			return nil, tableLengthError(len(t), wantValues)
		}
		out := make([]float64, len(t))
		for i, v := range t {
			out[i] = float64(v)
		}
		return out, nil
	case []float64:
		if len(t) != wantValues {
			return nil, tableLengthError(len(t), wantValues)
		}
		out := make([]float64, len(t))
		copy(out, t)
		return out, nil
	case [][3]float32:
		if len(t) != nodeCount {
			return nil, tableNodeLengthError(len(t), nodeCount)
		}
		out := make([]float64, wantValues)
		for i, v := range t {
			out[i*3+0] = float64(v[0])
			out[i*3+1] = float64(v[1])
			out[i*3+2] = float64(v[2])
		}
		return out, nil
	case [][3]float64:
		if len(t) != nodeCount {
			return nil, tableNodeLengthError(len(t), nodeCount)
		}
		out := make([]float64, wantValues)
		for i, v := range t {
			out[i*3+0] = v[0]
			out[i*3+1] = v[1]
			out[i*3+2] = v[2]
		}
		return out, nil
	case []RGB:
		if len(t) != nodeCount {
			return nil, tableNodeLengthError(len(t), nodeCount)
		}
		out := make([]float64, wantValues)
		for i, v := range t {
			out[i*3+0] = v.R
			out[i*3+1] = v.G
			out[i*3+2] = v.B
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("%w: table is nil", ErrInvalidLUT)
	default:
		return nil, fmt.Errorf("%w: unsupported table type %T", ErrInvalidLUT, table)
	}
}

func tableLengthError(got, want int) error {
	return fmt.Errorf("%w: table has %d values, want %d", ErrInvalidLUT, got, want)
}

func tableNodeLengthError(got, want int) error {
	return fmt.Errorf("%w: table has %d nodes, want %d", ErrInvalidLUT, got, want)
}

// Dimensions returns the number of samples on each RGB axis.
func (l *LUT) Dimensions() [3]int {
	return [3]int{l.sizeR, l.sizeG, l.sizeB}
}

// Size returns the number of samples on each RGB axis.
func (l *LUT) Size() (red, green, blue int) {
	return l.sizeR, l.sizeG, l.sizeB
}

// Boundary returns the configured boundary mode.
func (l *LUT) Boundary() BoundaryMode { return l.mode }

// Layout returns the table's memory order.
func (l *LUT) Layout() Layout { return l.layout }

// Domain returns the inclusive lower and upper bounds of the LUT grid.
func (l *LUT) Domain() (lower, upper [3]float64) {
	return l.lower, l.upper
}

// Sample applies the LUT to one linear RGB color. It returns the interpolated
// linear RGB value. The boundary mode determines whether an out-of-domain
// component is clamped or rejected. NaN components are always rejected.
func (l *LUT) Sample(red, green, blue float64) (RGB, error) {
	if err := l.validateSample(red, green, blue); err != nil {
		return RGB{}, err
	}
	return l.sampleChecked(red, green, blue), nil
}

// Sample32 is Sample with float32 input components.
func (l *LUT) Sample32(red, green, blue float32) (RGB, error) {
	return l.Sample(float64(red), float64(green), float64(blue))
}

// Lookup is an alias for Sample.
func (l *LUT) Lookup(red, green, blue float64) (RGB, error) {
	return l.Sample(red, green, blue)
}

// Interpolate is an alias for Sample.
func (l *LUT) Interpolate(red, green, blue float64) (RGB, error) {
	return l.Sample(red, green, blue)
}

// ApplyColor is an alias for Sample.
func (l *LUT) ApplyColor(red, green, blue float64) (RGB, error) {
	return l.Sample(red, green, blue)
}

func (l *LUT) validateSample(red, green, blue float64) error {
	values := [3]float64{red, green, blue}
	for i, v := range values {
		if math.IsNaN(v) {
			return &ColorError{Pixel: -1, Channel: i, Value: v, Err: ErrNonFinite}
		}
		if l.mode == BoundaryReject {
			if math.IsInf(v, 0) || v < l.lower[i] || v > l.upper[i] {
				return &ColorError{Pixel: -1, Channel: i, Value: v, Err: ErrOutOfRange}
			}
		}
	}
	return nil
}

func (l *LUT) sampleChecked(red, green, blue float64) RGB {
	var u [3]float64
	for i, v := range [3]float64{red, green, blue} {
		if v <= l.lower[i] {
			u[i] = 0
		} else if v >= l.upper[i] {
			u[i] = 1
		} else {
			u[i] = (v - l.lower[i]) * l.scale[i]
		}
	}
	return l.sampleNormalized(u[0], u[1], u[2])
}

// SampleCell samples one cell directly. cellR, cellG, cellB identify the
// lower corner of the cell. fr, fg, fb are local cell coordinates in [0,1].
// This is useful when a caller already knows the cell, and it makes face
// continuity testable across the two cells sharing a face.
//
// The cell indices must be valid cell indices for the grid, and each fraction
// must be in [0,1]. A dimension of size 1 has a single cell index 0 and
// accepts any fraction, but its output is constant.
func (l *LUT) SampleCell(cellR, cellG, cellB int, fr, fg, fb float64) (RGB, error) {
	badR := cellR < 0 || (l.sizeR == 1 && cellR != 0) || (l.sizeR > 1 && cellR >= l.sizeR-1)
	badG := cellG < 0 || (l.sizeG == 1 && cellG != 0) || (l.sizeG > 1 && cellG >= l.sizeG-1)
	badB := cellB < 0 || (l.sizeB == 1 && cellB != 0) || (l.sizeB > 1 && cellB >= l.sizeB-1)
	if badR || badG || badB {
		return RGB{}, fmt.Errorf("%w: invalid cell (%d,%d,%d) for grid (%d,%d,%d)",
			ErrInvalidColor, cellR, cellG, cellB, l.sizeR, l.sizeG, l.sizeB)
	}
	for i, f := range [3]float64{fr, fg, fb} {
		if math.IsNaN(f) || f < 0 || f > 1 {
			return RGB{}, &ColorError{Pixel: -1, Channel: i, Value: f, Err: ErrInvalidColor}
		}
	}
	return l.sampleCell(cellR, cellG, cellB, fr, fg, fb), nil
}

func (l *LUT) cellIndex(component float64, size int) (cell int, fraction float64) {
	if size <= 1 {
		return 0, 0
	}
	scaled := component * float64(size-1)
	if scaled <= 0 {
		return 0, 0
	}
	// Grid nodes should recover table entries exactly even when the caller's
	// absolute color was converted to a normalized coordinate with a
	// round-off error of a few ulps.
	scaled = snapGridCoordinate(scaled)
	if scaled >= float64(size-1) {
		return size - 2, 1
	}
	cell = int(math.Floor(scaled))
	if cell >= size-1 {
		return size - 2, 1
	}
	fraction = scaled - float64(cell)
	return cell, fraction
}

func snapGridCoordinate(v float64) float64 {
	nearest := math.Round(v)
	tol := 8 * (math.Nextafter(1, 2) - 1) * math.Max(1, math.Abs(v))
	if math.Abs(v-nearest) <= tol {
		return nearest
	}
	return v
}

func (l *LUT) sampleNormalized(ur, ug, ub float64) RGB {
	cr, fr := l.cellIndex(ur, l.sizeR)
	cg, fg := l.cellIndex(ug, l.sizeG)
	cb, fb := l.cellIndex(ub, l.sizeB)
	return l.sampleCell(cr, cg, cb, fr, fg, fb)
}

func (l *LUT) sampleCell(cellR, cellG, cellB int, fr, fg, fb float64) RGB {
	f := [3]float64{fr, fg, fb}

	// Corners are returned from the table verbatim. This also avoids any
	// round-off in an incremental accumulation at a grid node.
	if isZeroOrOne(fr) && isZeroOrOne(fg) && isZeroOrOne(fb) {
		r := cellR
		g := cellG
		b := cellB
		if fr == 1 && l.sizeR > 1 {
			r++
		}
		if fg == 1 && l.sizeG > 1 {
			g++
		}
		if fb == 1 && l.sizeB > 1 {
			b++
		}
		return l.node(r, g, b)
	}

	order := componentOrder(f)
	var step [3][3]int
	if l.sizeR > 1 {
		step[0][0] = 1
	}
	if l.sizeG > 1 {
		step[1][1] = 1
	}
	if l.sizeB > 1 {
		step[2][2] = 1
	}

	axis0 := order[0]
	axis1 := order[1]
	axis2 := order[2]

	r0, g0, b0 := cellR, cellG, cellB
	p0 := l.node(r0, g0, b0)

	r1 := r0 + step[axis0][0]
	g1 := g0 + step[axis0][1]
	b1 := b0 + step[axis0][2]
	p1 := l.node(r1, g1, b1)

	r2 := r1 + step[axis1][0]
	g2 := g1 + step[axis1][1]
	b2 := b1 + step[axis1][2]
	p2 := l.node(r2, g2, b2)

	r3 := r2 + step[axis2][0]
	g3 := g2 + step[axis2][1]
	b3 := b2 + step[axis2][2]
	p3 := l.node(r3, g3, b3)

	w0 := f[axis0]
	w1 := f[axis1]
	w2 := f[axis2]

	return RGB{
		R: p0.R + w0*(p1.R-p0.R) + w1*(p2.R-p1.R) + w2*(p3.R-p2.R),
		G: p0.G + w0*(p1.G-p0.G) + w1*(p2.G-p1.G) + w2*(p3.G-p2.G),
		B: p0.B + w0*(p1.B-p0.B) + w1*(p2.B-p1.B) + w2*(p3.B-p2.B),
	}
}

func isZeroOrOne(v float64) bool { return v == 0 || v == 1 }

// componentOrder returns the descending order of the three cell
// coordinates. Equal values are ordered by RGB channel priority: R, then G,
// then B. This is a total order, so exactly one tetrahedron is selected on
// cell faces and along cell diagonals as well as in cell interiors.
func componentOrder(v [3]float64) [3]int {
	order := [3]int{0, 1, 2}
	for i := 1; i < 3; i++ {
		for j := i; j > 0; j-- {
			a, b := order[j-1], order[j]
			if v[a] > v[b] || (v[a] == v[b] && a < b) {
				break
			}
			order[j-1], order[j] = b, a
		}
	}
	return order
}

func (l *LUT) node(r, g, b int) RGB {
	var idx int
	switch l.layout {
	case BlueFastest:
		idx = ((r*l.sizeG)+g)*l.sizeB + b
	default:
		idx = ((b*l.sizeG)+g)*l.sizeR + r
	}
	off := idx * 3
	return RGB{R: l.table[off], G: l.table[off+1], B: l.table[off+2]}
}
