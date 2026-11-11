package yuvconv

import (
	"image"
	"math"
)

// smpteColor is one bar of the 100% SMPTE color bars with its sRGB code
// values and the canonical BT.601 limited-range YUV code values
// (e.g. ITU-R BT.1885 / FFmpeg colorbars source values).
type smpteColor struct {
	name    string
	r, g, b uint8
	y, u, v uint8
}

var smpteBars = []smpteColor{
	{"white", 255, 255, 255, 235, 128, 128},
	{"yellow", 255, 255, 0, 210, 16, 146},
	{"cyan", 0, 255, 255, 170, 166, 16},
	{"green", 0, 255, 0, 145, 54, 34},
	{"magenta", 255, 0, 255, 106, 202, 222},
	{"red", 255, 0, 0, 81, 90, 240},
	{"blue", 0, 0, 255, 41, 240, 110},
	{"black", 0, 0, 0, 16, 128, 128},
}

// buildI420 fills an I420 frame from a solid-color 2x2-block bar pattern.
func buildI420(w, h int, bars []smpteColor) I420Frame {
	cw, ch := (w+1)/2, (h+1)/2
	f := I420Frame{
		Y: make([]byte, w*h),
		U: make([]byte, cw*ch),
		V: make([]byte, cw*ch),
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bar := bars[(x/2)%len(bars)]
			f.Y[y*w+x] = bar.y
			if x%2 == 0 && y%2 == 0 {
				f.U[(y/2)*cw+x/2] = bar.u
				f.V[(y/2)*cw+x/2] = bar.v
			}
		}
	}
	return f
}

func i420ToNV12(src I420Frame, cw, ch int) NV12Frame {
	nv := NV12Frame{
		Y: append([]byte(nil), src.Y...),
		C: make([]byte, 2*cw*ch),
	}
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			nv.C[cy*2*cw+2*cx] = src.U[cy*cw+cx]
			nv.C[cy*2*cw+2*cx+1] = src.V[cy*cw+cx]
		}
	}
	return nv
}

// to10bit maps an 8-bit limited-range code value onto the 10-bit scale and
// places it in the high 10 bits of a P010 uint16.
func to10bit(v uint8) uint16 {
	code := int(math.Round(float64(v) * 1023 / 255))
	return uint16(code) << 6
}

func i420ToP010(src I420Frame, w, h, cw, ch int) P010Frame {
	p := P010Frame{
		Y: make([]uint16, w*h),
		C: make([]uint16, 2*cw*ch),
	}
	for i, v := range src.Y {
		p.Y[i] = to10bit(v)
	}
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			p.C[cy*2*cw+2*cx] = to10bit(src.U[cy*cw+cx])
			p.C[cy*2*cw+2*cx+1] = to10bit(src.V[cy*cw+cx])
		}
	}
	return p
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxAbsDiff(img *image.RGBA, w, h int, want func(x, y int) (r, g, b uint8)) int {
	worst := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			wr, wg, wb := want(x, y)
			i := img.PixOffset(x, y)
			d := maxInt(absInt(int(img.Pix[i])-int(wr)), absInt(int(img.Pix[i+1])-int(wg)))
			d = maxInt(d, absInt(int(img.Pix[i+2])-int(wb)))
			if d > worst {
				worst = d
			}
			if img.Pix[i+3] != 255 {
				worst = 256
			}
		}
	}
	return worst
}

func barRGB(x, _ int) (r, g, b uint8) {
	bar := smpteBars[(x/2)%len(smpteBars)]
	return bar.r, bar.g, bar.b
}
