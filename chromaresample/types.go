package chromaresample

import "errors"

// ErrInvalidGeometry is returned when a grid, plane or geometry description is
// inconsistent or uses an invalid sampling center.
var ErrInvalidGeometry = errors.New("chromaresample: invalid geometry")

// Rational is an exact rational number used to express a sampling-center
// offset in reference-grid pixel units. Den must be positive.
type Rational struct {
	Num int64
	Den int64
}

// Float returns the center as a high precision floating point offset.
func (r Rational) Float() float64 {
	return float64(r.Num) / float64(r.Den)
}

func (r Rational) valid() bool {
	return r.Den > 0
}

// Grid describes one output sampling grid of a planar component.
type Grid struct {
	// Width and Height are the output pixel dimensions.
	Width, Height int
	// CenterX and CenterY are the output sampling centers relative to the
	// common reference grid. They must always be supplied explicitly.
	CenterX, CenterY Rational
}

func (g Grid) validate() error {
	if g.Width <= 0 || g.Height <= 0 || !g.CenterX.valid() || !g.CenterY.valid() {
		return ErrInvalidGeometry
	}
	return nil
}

// Geometry fully describes a resampling problem between two sampling grids
// of the same component, both expressed against one reference grid (normally
// the luma sampling grid).
type Geometry struct {
	// RefWidth and RefHeight are the dimensions, in reference pixels, of the
	// common reference grid shared by source and destination.
	RefWidth, RefHeight int
	// Src and Dst describe the source and destination sampling grids.
	Src, Dst Grid
}

func (g Geometry) validate() error {
	if g.RefWidth <= 0 || g.RefHeight <= 0 {
		return ErrInvalidGeometry
	}
	if err := g.Src.validate(); err != nil {
		return err
	}
	return g.Dst.validate()
}

// Plane is a planar 8-bit component (Y, Cb or Cr) together with its sampling
// grid. Samples are stored row by row; Stride is the number of bytes between
// the first sample of two consecutive rows and may be larger than Width.
type Plane struct {
	Grid
	// Pix holds the component samples, at least Height*Stride bytes.
	Pix []uint8
	// Stride is the row stride in bytes.
	Stride int
}

func (p Plane) validate() error {
	if err := p.Grid.validate(); err != nil {
		return err
	}
	if p.Stride < p.Width || len(p.Pix) < p.Stride*(p.Height-1)+p.Width {
		return ErrInvalidGeometry
	}
	return nil
}
