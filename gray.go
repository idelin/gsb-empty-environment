package tmplmatch

import "image"

// GrayImage is a row-major grayscale image with 0..1 values.
type GrayImage struct {
	Pix    []float64
	Width  int
	Height int
	// Mask reports which pixels participate in matching. A nil mask means
	// every pixel is valid. It is used for templates with transparency.
	Mask       []bool
	ValidCount int
}

func newGray(w, h int) *GrayImage {
	return &GrayImage{
		Pix:        make([]float64, w*h),
		Width:      w,
		Height:     h,
		ValidCount: w * h,
	}
}

func (g *GrayImage) at(x, y int) float64 {
	return g.Pix[y*g.Width+x]
}

func (g *GrayImage) hasMask() bool { return g.Mask != nil }

// ToGray converts any image into a 0..1 grayscale image. When keepAlpha is
// true, pixels with alpha below 128 are recorded in the mask (used for
// templates); otherwise the mask is nil and all pixels are valid.
func ToGray(src image.Image, keepAlpha bool) *GrayImage {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	g := newGray(w, h)
	if keepAlpha {
		g.Mask = make([]bool, w*h)
		g.ValidCount = 0
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, gg, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			idx := y*w + x
			// Rec. 601 luma on the 16-bit channel values.
			g.Pix[idx] = (0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bl)) / 65535.0
			if keepAlpha {
				if a >= 128*257 {
					g.Mask[idx] = true
					g.ValidCount++
				}
			}
		}
	}
	return g
}

// ResizeBilinear scales src to width x height. The mask, when present, is
// resized with nearest-neighbour sampling so it stays a hard alpha cut.
func ResizeBilinear(src *GrayImage, width, height int) *GrayImage {
	if width <= 0 || height <= 0 {
		return nil
	}
	dst := newGray(width, height)
	if src.Mask != nil {
		dst.Mask = make([]bool, width*height)
		dst.ValidCount = 0
	}
	sx := float64(src.Width) / float64(width)
	sy := float64(src.Height) / float64(height)
	for y := 0; y < height; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		y0 := int(fy)
		wy := fy - float64(y0)
		y1 := y0 + 1
		cy0 := clampInt(y0, 0, src.Height-1)
		cy1 := clampInt(y1, 0, src.Height-1)
		for x := 0; x < width; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			x0 := int(fx)
			wx := fx - float64(x0)
			x1 := x0 + 1
			cx0 := clampInt(x0, 0, src.Width-1)
			cx1 := clampInt(x1, 0, src.Width-1)

			v00 := src.at(cx0, cy0)
			v01 := src.at(cx1, cy0)
			v10 := src.at(cx0, cy1)
			v11 := src.at(cx1, cy1)
			top := v00*(1-wx) + v01*wx
			bot := v10*(1-wx) + v11*wx
			dst.Pix[y*width+x] = top*(1-wy) + bot*wy

			if dst.Mask != nil {
				// Nearest source pixel decides mask membership.
				nx := clampInt(int((float64(x)+0.5)*sx), 0, src.Width-1)
				ny := clampInt(int((float64(y)+0.5)*sy), 0, src.Height-1)
				if src.Mask[ny*src.Width+nx] {
					dst.Mask[y*width+x] = true
					dst.ValidCount++
				}
			}
		}
	}
	return dst
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

// IntegralImage is the summed-area table of a grayscale image and its square.
// Integral[y+1][x+1] holds the sum of all pixels in [0,x) x [0,y).
type IntegralImage struct {
	Width  int
	Height int
	S1     []float64 // pixel sum
	S2     []float64 // squared pixel sum
}

// NewIntegral builds summed-area tables for value and value^2.
func NewIntegral(g *GrayImage) *IntegralImage {
	w, h := g.Width, g.Height
	ii := &IntegralImage{
		Width:  w,
		Height: h,
		S1:     make([]float64, (w+1)*(h+1)),
		S2:     make([]float64, (w+1)*(h+1)),
	}
	stride := w + 1
	for y := 0; y < h; y++ {
		rowSum1, rowSum2 := 0.0, 0.0
		srcRow := y * w
		dstRow := (y + 1) * stride
		prevRow := y * stride
		for x := 0; x < w; x++ {
			v := g.Pix[srcRow+x]
			rowSum1 += v
			rowSum2 += v * v
			ii.S1[dstRow+x+1] = ii.S1[prevRow+x+1] + rowSum1
			ii.S2[dstRow+x+1] = ii.S2[prevRow+x+1] + rowSum2
		}
	}
	return ii
}

// rectSum returns the sum over the rectangle [x, x+w) x [y, y+h).
func (ii *IntegralImage) rectSum(s []float64, x, y, w, h int) float64 {
	stride := ii.Width + 1
	x1, y1 := x+w, y+h
	return s[y1*stride+x1] - s[y*stride+x1] - s[y1*stride+x] + s[y*stride+x]
}
