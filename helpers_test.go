package linscale

import (
	"image"
)

func resizeSerial(img image.Image, w, h int) (*image.NRGBA, error) {
	return resize(img, w, h, false)
}

func resizeForcedParallel(img image.Image, w, h int) (*image.NRGBA, error) {
	return resize(img, w, h, true)
}

// naiveSRGBDownscaleX applies the same Lanczos weights as linscale but
// directly on encoded sRGB bytes (no linearization, no premultiplication),
// reproducing the dark-thin-line behavior of a naive scaler. Height is kept.
func naiveSRGBDownscaleX(src *image.NRGBA, dstW int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	hx := buildAxis(w, dstW)
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, h))

	for y := 0; y < h; y++ {
		for x := 0; x < dstW; x++ {
			var acc [4]float64
			w0 := hx.dstStart[x]
			w1 := hx.dstStart[x+1]
			first := hx.srcStart[x]
			for k, wf := range hx.weights[w0:w1] {
				sx := clampIndex(first+k, w)
				so := (y*w + sx) * 4
				for c := 0; c < 4; c++ {
					acc[c] += float64(src.Pix[so+c]) * float64(wf)
				}
			}
			do := (y*dstW + x) * 4
			for c := 0; c < 4; c++ {
				v := int(acc[c] + 0.5)
				if v < 0 {
					v = 0
				} else if v > 255 {
					v = 255
				}
				dst.Pix[do+c] = uint8(v)
			}
		}
	}
	return dst
}
