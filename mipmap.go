package texsample

// NewTexture builds a full mipmap pyramid from interleaved RGBA texels.
//
// If srgb is true the source channels (alpha is treated as linear) are
// decoded to linear light before mip generation, so every level lives in
// linear space and interpolation is performed correctly. If srgb is false
// the source values are assumed to already be linear.
//
// Width and height may be arbitrary positive integers, including non power of
// two and odd sizes. Each level is produced by a weighted 2x2 box filter that
// averages the texels actually covered; border texels contribute with half
// (or quarter) weight instead of being duplicated or dropped.
func NewTexture(rgba []float32, w, h int, srgb bool) *Texture {
	if w <= 0 || h <= 0 {
		panic("texsample: texture dimensions must be positive")
	}
	if len(rgba) != w*h*4 {
		panic("texsample: rgba data length does not match dimensions")
	}

	base := Level{Data: make([]float32, w*h*4), W: w, H: h}
	if srgb {
		for i := 0; i < w*h; i++ {
			p := i * 4
			base.Data[p] = DecodeSRGB(rgba[p])
			base.Data[p+1] = DecodeSRGB(rgba[p+1])
			base.Data[p+2] = DecodeSRGB(rgba[p+2])
			base.Data[p+3] = rgba[p+3]
		}
	} else {
		copy(base.Data, rgba)
	}

	t := &Texture{SRGB: srgb, Levels: make([]Level, 0, MipLevelCount(w, h))}
	t.Levels = append(t.Levels, base)
	for t.Levels[len(t.Levels)-1].W > 1 || t.Levels[len(t.Levels)-1].H > 1 {
		t.Levels = append(t.Levels, downsampleBox(t.Levels[len(t.Levels)-1]))
	}
	return t
}

// NewLinearTexture builds a texture whose level 0 is already in linear space
// (no sRGB decoding) and generates the remaining mip levels. It is mainly
// useful for tests and for callers that manage color conversion themselves.
func NewLinearTexture(levels []Level) *Texture {
	if len(levels) == 0 {
		panic("texsample: at least one mip level is required")
	}
	t := &Texture{SRGB: false, Levels: make([]Level, len(levels))}
	for i, l := range levels {
		if l.W <= 0 || l.H <= 0 || len(l.Data) != l.W*l.H*4 {
			panic("texsample: invalid mip level dimensions")
		}
		t.Levels[i] = Level{Data: append([]float32(nil), l.Data...), W: l.W, H: l.H}
	}
	return t
}

// downsampleBox produces the next mip level with dimensions ceil(w/2) x
// ceil(h/2). Destination texel (dx, dy) covers source texels
// [2dx, min(2dx+2, w)) x [2dy, min(2dy+2, h)); every covered source texel has
// equal weight, which automatically gives border texels their reduced weight
// for odd source dimensions.
func downsampleBox(src Level) Level {
	dw, dh := (src.W+1)/2, (src.H+1)/2
	dst := Level{Data: make([]float32, dw*dh*4), W: dw, H: dh}

	for dy := 0; dy < dh; dy++ {
		y0 := dy * 2
		y1 := y0 + 2
		if y1 > src.H {
			y1 = src.H
		}
		for dx := 0; dx < dw; dx++ {
			x0 := dx * 2
			x1 := x0 + 2
			if x1 > src.W {
				x1 = src.W
			}

			var r, g, b, a float32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					p := (sy*src.W + sx) * 4
					r += src.Data[p]
					g += src.Data[p+1]
					b += src.Data[p+2]
					a += src.Data[p+3]
				}
			}
			count := float32((x1 - x0) * (y1 - y0))
			q := (dy*dw + dx) * 4
			dst.Data[q] = r / count
			dst.Data[q+1] = g / count
			dst.Data[q+2] = b / count
			dst.Data[q+3] = a / count
		}
	}
	return dst
}
