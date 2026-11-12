package eqcube

import (
	"image"
	"image/color"
	"math"
)

// Equirect is an equirectangular panorama.
//
// Horizontal pixel coordinate encodes longitude phi in [-pi, pi), sampled
// periodically: phi and phi+2*pi denote the same direction, so imagery is
// continuous across the left/right image border. Vertical coordinate
// encodes latitude theta in [-pi/2, pi/2]; sampling past either pole is
// clamped to the pole row instead of mirroring or reading out of bounds.
type Equirect struct {
	src image.Image
	w   int
	h   int
}

// NewEquirect wraps any image.Image as an equirectangular panorama.
func NewEquirect(src image.Image) *Equirect {
	b := src.Bounds()
	return &Equirect{src: src, w: b.Dx(), h: b.Dy()}
}

// Size returns the panorama width and height in pixels.
func (e *Equirect) Size() (width, height int) {
	return e.w, e.h
}

// AtDirection bilinearly samples the panorama along unit direction d.
//
// Longitude wraps with period 2*pi (seam continuity) and latitude is
// clamped to [-pi/2, pi/2] (pole clamping).
func (e *Equirect) AtDirection(d Vec3) color.NRGBA {
	phi := math.Atan2(d.Z, d.X)
	theta := math.Asin(clamp(d.Y, -1, 1))

	// Center the half-open longitude range on [0, 2*pi).
	if phi < 0 {
		phi += 2 * math.Pi
	}

	fx := phi / (2 * math.Pi) * float64(e.w)
	fy := (theta + math.Pi/2) / math.Pi * float64(e.h)

	x0 := int(math.Floor(fx))
	y0 := int(math.Floor(fy))
	tx := fx - float64(x0)
	ty := fy - float64(y0)

	// x indices wrap around the longitude seam; y indices clamp at poles.
	x1 := x0 + 1
	if x1 >= e.w {
		x1 -= e.w
	}
	x0 = mod(x0, e.w)
	yl := clampInt(y0, 0, e.h-1)
	yh := clampInt(y0+1, 0, e.h-1)

	c00 := e.pixel(x0, yl)
	c10 := e.pixel(x1, yl)
	c01 := e.pixel(x0, yh)
	c11 := e.pixel(x1, yh)

	return color.NRGBA{
		R: mix(mix(c00.R, c10.R, tx), mix(c01.R, c11.R, tx), ty),
		G: mix(mix(c00.G, c10.G, tx), mix(c01.G, c11.G, tx), ty),
		B: mix(mix(c00.B, c10.B, tx), mix(c01.B, c11.B, tx), ty),
		A: mix(mix(c00.A, c10.A, tx), mix(c01.A, c11.A, tx), ty),
	}
}

// pixel reads one source pixel in normalized NRGBA space.
func (e *Equirect) pixel(x, y int) color.NRGBA {
	b := e.src.Bounds()
	return color.NRGBAModel.Convert(
		e.src.At(b.Min.X+x, b.Min.Y+y),
	).(color.NRGBA)
}

func mix(a, b uint8, t float64) uint8 {
	v := float64(a) + (float64(b)-float64(a))*t
	return uint8(math.Round(clamp(v, 0, 255)))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mod(a, n int) int {
	r := a % n
	if r < 0 {
		r += n
	}
	return r
}
