// Package linscale resizes images with a separable Lanczos-3 filter in
// linear-light premultiplied-alpha space. Filtering raw sRGB values darkens
// thin features and filtering non-premultiplied colors leaves black fringes
// on transparent edges; linscale decodes sRGB to linear light and
// premultiplies alpha before both filter passes, then encodes back to sRGB.
package linscale

import (
	"errors"
	"image"
	"image/color"
	"runtime"
	"sync"
)

// parallelPixels is the minimum source pixel count at which the vertical pass
// is split into worker-processed stripes. Smaller images stay on one goroutine
// to avoid scheduling overhead; both paths execute identical arithmetic.
const parallelPixels = 1 << 20

// stripeHeight is the output-row height of one parallel work unit.
const stripeHeight = 16

// ErrInvalidSize is returned when the requested dimensions or the source
// image are empty.
var ErrInvalidSize = errors.New("linscale: dimensions must be positive")

// Resize returns img resized to dstWidth x dstHeight pixels. The output is
// always an 8-bit *image.NRGBA (straight alpha, sRGB-encoded color).
//
// Large images are processed in horizontal stripes by a worker pool; the
// per-pixel arithmetic is identical to the single-goroutine path, so results
// are bit-for-bit reproducible regardless of worker count.
func Resize(img image.Image, dstWidth, dstHeight int) (*image.NRGBA, error) {
	return resize(img, dstWidth, dstHeight, false)
}

// resize is the shared implementation. forceParallel bypasses the size
// threshold so tests can exercise the stripe path on small images and
// compare it byte-for-byte with the serial path.
func resize(img image.Image, dstWidth, dstHeight int, forceParallel bool) (*image.NRGBA, error) {
	if dstWidth <= 0 || dstHeight <= 0 {
		return nil, ErrInvalidSize
	}
	srcBounds := img.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return nil, ErrInvalidSize
	}

	src := toNRGBA(img)
	// Premultiplied linear-light source: r,g,b already multiplied by a.
	srcPL := decodePremultiplied(src, srcWidth, srcHeight)

	hx := buildAxis(srcWidth, dstWidth)
	vy := buildAxis(srcHeight, dstHeight)

	// Horizontal pass: full-width float32 premultiplied linear buffer.
	midW, midH := dstWidth, srcHeight
	mid := make([]float32, midW*midH*4)
	for y := 0; y < midH; y++ {
		filterRow(srcPL, srcWidth, y, hx, mid, midW)
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	verticalPass(mid, midW, midH, vy, dst, forceParallel)
	return dst, nil
}

// toNRGBA normalizes any input image into a tightly packed *image.NRGBA.
// Fast paths cover the standard straight-alpha and premultiplied-alpha
// layouts; anything else goes through the generic color model.
func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	switch src := img.(type) {
	case *image.NRGBA:
		if src.Rect == b && src.Stride == w*4 {
			out := image.NewNRGBA(image.Rect(0, 0, w, h))
			copy(out.Pix, src.Pix)
			return out
		}
	case *image.RGBA:
		// image.RGBA holds premultiplied 8-bit values; recover the straight
		// (non-premultiplied) color with the same integer formula as
		// color.NRGBAModel: straight = premul*255/alpha.
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := src.PixOffset(b.Min.X+x, b.Min.Y+y)
				o := (y*w + x) * 4
				a := src.Pix[i+3]
				out.Pix[o+3] = a
				if a == 0 {
					continue
				}
				for c := 0; c < 3; c++ {
					p := uint32(src.Pix[i+c])
					out.Pix[o+c] = uint8((p*255 + uint32(a)/2) / uint32(a))
				}
			}
		}
		return out
	}

	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nrgba := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			o := (y*w + x) * 4
			out.Pix[o+0] = nrgba.R
			out.Pix[o+1] = nrgba.G
			out.Pix[o+2] = nrgba.B
			out.Pix[o+3] = nrgba.A
		}
	}
	return out
}

// decodePremultiplied turns an 8-bit straight-alpha sRGB image into packed
// premultiplied linear-light RGBA floats.
func decodePremultiplied(src *image.NRGBA, w, h int) []float32 {
	buf := make([]float32, w*h*4)
	for i := 0; i < w*h; i++ {
		a := float64(src.Pix[i*4+3]) / 255.0
		buf[i*4+0] = float32(decodeTable[src.Pix[i*4+0]] * a)
		buf[i*4+1] = float32(decodeTable[src.Pix[i*4+1]] * a)
		buf[i*4+2] = float32(decodeTable[src.Pix[i*4+2]] * a)
		// Alpha is coverage, not an sRGB-encoded color: it stays linear.
		buf[i*4+3] = float32(a)
	}
	return buf
}

// filterRow applies the horizontal 1-D filter to source row y, writing
// premultiplied linear RGBA into dst at row y. All accumulation runs in
// float64; only the stored taps are float32.
func filterRow(src []float32, srcW, y int, hx contrib, dst []float32, dstW int) {
	srcOff := y * srcW * 4
	dstOff := y * dstW * 4
	for x := 0; x < dstW; x++ {
		w0 := hx.dstStart[x]
		w1 := hx.dstStart[x+1]
		first := hx.srcStart[x]
		var rr, gg, bb, aa float64
		for k, w := range hx.weights[w0:w1] {
			sx := clampIndex(first+k, srcW)
			so := srcOff + sx*4
			wf := float64(w)
			rr += float64(src[so+0]) * wf
			gg += float64(src[so+1]) * wf
			bb += float64(src[so+2]) * wf
			aa += float64(src[so+3]) * wf
		}
		do := dstOff + x*4
		dst[do+0] = float32(rr)
		dst[do+1] = float32(gg)
		dst[do+2] = float32(bb)
		dst[do+3] = float32(aa)
	}
}

// verticalPass applies the vertical 1-D filter stripe by stripe and encodes
// the result as straight-alpha sRGB. Stripes are independent because the
// vertical kernel only reads source rows, so they may run on a worker pool
// without changing any computed value.
func verticalPass(mid []float32, midW, midH int, vy contrib, dst *image.NRGBA, forceParallel bool) {
	dstW, dstH := dst.Rect.Dx(), dst.Rect.Dy()

	process := func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < midW; x++ {
				w0 := vy.dstStart[y]
				w1 := vy.dstStart[y+1]
				first := vy.srcStart[y]
				var rr, gg, bb, aa float64
				for k, w := range vy.weights[w0:w1] {
					sy := clampIndex(first+k, midH)
					mo := (sy*midW + x) * 4
					wf := float64(w)
					rr += float64(mid[mo+0]) * wf
					gg += float64(mid[mo+1]) * wf
					bb += float64(mid[mo+2]) * wf
					aa += float64(mid[mo+3]) * wf
				}
				encodeOutput(dst.Pix, (y*dstW+x)*4, rr, gg, bb, aa)
			}
		}
	}

	if !forceParallel && midW*midH < parallelPixels {
		process(0, dstH)
		return
	}

	workers := runtime.GOMAXPROCS(0)
	if !forceParallel && workers < 2 {
		process(0, dstH)
		return
	}
	stripes := (dstH + stripeHeight - 1) / stripeHeight
	if workers > stripes {
		workers = stripes
	}

	jobs := make(chan int, stripes)
	for s := 0; s < stripes; s++ {
		jobs <- s
	}
	close(jobs)

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				y0 := s * stripeHeight
				y1 := y0 + stripeHeight
				if y1 > dstH {
					y1 = dstH
				}
				process(y0, y1)
			}
		}()
	}
	wg.Wait()
}

// encodeOutput unpremultiplies one filtered pixel and encodes it as sRGB.
// Fully transparent pixels collapse to (0,0,0,0) so unused color channels
// never carry round-off noise.
func encodeOutput(pix []uint8, off int, rr, gg, bb, aa float64) {
	if aa <= 0 {
		return
	}
	if aa > 1 {
		aa = 1
	}
	inv := 1.0 / aa
	r := rr * inv
	g := gg * inv
	b := bb * inv
	if r > 1 {
		r = 1
	}
	if g > 1 {
		g = 1
	}
	if b > 1 {
		b = 1
	}
	pix[off+0] = encodeLinearLUT(r)
	pix[off+1] = encodeLinearLUT(g)
	pix[off+2] = encodeLinearLUT(b)
	// Alpha is linear coverage; quantize directly without sRGB encoding.
	av := int(aa*255.0 + 0.5)
	if av > 255 {
		av = 255
	}
	pix[off+3] = uint8(av)
}
