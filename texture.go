package taskpkg

import "math"

// AddressMode selects how texture coordinates outside [0,1) are resolved.
type AddressMode int

const (
	// AddressWrap repeats the texture periodically.
	AddressWrap AddressMode = iota
	// AddressClamp clamps coordinates to the texture border.
	AddressClamp
)

// ColorRGBA is a linear, premultiplied-alpha color. All filtering math keeps
// the premultiplied representation end to end.
type ColorRGBA struct {
	R float64
	G float64
	B float64
	A float64
}

// Premultiplied converts a straight (non-premultiplied) linear color into its
// premultiplied representation.
func Premultiplied(r, g, b, a float64) ColorRGBA {
	return ColorRGBA{R: r * a, G: g * a, B: b * a, A: a}
}

// Texture is a mipmapped 2D image whose levels are power-of-two
// downsamplings of the base level. Width and height may differ.
type Texture struct {
	width  int
	height int
	levels int
	data   [][]ColorRGBA
	addrU  AddressMode
	addrV  AddressMode
}

// NewTexture builds a mip chain from a base level. The base image must be at
// least one texel on each axis and contain width*height colors in row-major
// order. Each subsequent level is a 2x2 box average of the previous level
// (premultiplied colors average directly) until both axes reach one texel.
// addrU and addrV independently select wrap or clamp addressing.
func NewTexture(width, height int, base []ColorRGBA, addrU, addrV AddressMode) *Texture {
	if width < 1 || height < 1 || len(base) != width*height {
		panic("taskpkg: invalid texture dimensions or base data length")
	}

	levels := 1
	if width >= height {
		for w := width; w > 1; w >>= 1 {
			levels++
		}
	} else {
		for h := height; h > 1; h >>= 1 {
			levels++
		}
	}

	data := make([][]ColorRGBA, levels)
	data[0] = append([]ColorRGBA(nil), base...)

	w, h := width, height
	for level := 1; level < levels; level++ {
		nextW, nextH := nextDim(w), nextDim(h)
		curr := data[level-1]
		down := make([]ColorRGBA, nextW*nextH)
		for y := 0; y < nextH; y++ {
			for x := 0; x < nextW; x++ {
				sx0, sx1 := 2*x, min(2*x+1, w-1)
				sy0, sy1 := 2*y, min(2*y+1, h-1)
				c00 := curr[sy0*w+sx0]
				c01 := curr[sy0*w+sx1]
				c10 := curr[sy1*w+sx0]
				c11 := curr[sy1*w+sx1]
				down[y*nextW+x] = ColorRGBA{
					R: (c00.R + c01.R + c10.R + c11.R) * 0.25,
					G: (c00.G + c01.G + c10.G + c11.G) * 0.25,
					B: (c00.B + c01.B + c10.B + c11.B) * 0.25,
					A: (c00.A + c01.A + c10.A + c11.A) * 0.25,
				}
			}
		}
		data[level] = down
		w, h = nextW, nextH
	}

	return &Texture{
		width:  width,
		height: height,
		levels: levels,
		data:   data,
		addrU:  addrU,
		addrV:  addrV,
	}
}

// Width returns the base level width.
func (t *Texture) Width() int { return t.width }

// Height returns the base level height.
func (t *Texture) Height() int { return t.height }

// Levels returns the number of mip levels.
func (t *Texture) Levels() int { return t.levels }

func nextDim(dim int) int {
	if dim <= 1 {
		return 1
	}
	return dim / 2
}

func levelDim(baseDim, level int) int {
	dim := baseDim
	for i := 0; i < level; i++ {
		dim = nextDim(dim)
	}
	return dim
}

// wrapIndex maps an integer texel index into [0, dim) for the given mode.
func wrapIndex(index, dim int, mode AddressMode) int {
	if mode == AddressClamp {
		if index < 0 {
			return 0
		}
		if index >= dim {
			return dim - 1
		}
		return index
	}

	idx := index % dim
	if idx < 0 {
		idx += dim
	}
	return idx
}

// texelAt fetches a single texel from a level using per-axis addressing.
func (t *Texture) texelAt(level, x, y int) ColorRGBA {
	w := levelDim(t.width, level)
	h := levelDim(t.height, level)
	x = wrapIndex(x, w, t.addrU)
	y = wrapIndex(y, h, t.addrV)
	return t.data[level][y*w+x]
}

// bilinear performs bilinear interpolation at texel-space position (u, v)
// (texel centers at integer coordinates) on the given level.
func (t *Texture) bilinear(level int, u, v float64) ColorRGBA {
	x0 := int(math.Floor(u))
	y0 := int(math.Floor(v))
	fx := u - float64(x0)
	fy := v - float64(y0)

	c00 := t.texelAt(level, x0, y0)
	c10 := t.texelAt(level, x0+1, y0)
	c01 := t.texelAt(level, x0, y0+1)
	c11 := t.texelAt(level, x0+1, y0+1)

	w00 := (1 - fx) * (1 - fy)
	w10 := fx * (1 - fy)
	w01 := (1 - fx) * fy
	w11 := fx * fy

	return ColorRGBA{
		R: c00.R*w00 + c10.R*w10 + c01.R*w01 + c11.R*w11,
		G: c00.G*w00 + c10.G*w10 + c01.G*w01 + c11.G*w11,
		B: c00.B*w00 + c10.B*w10 + c01.B*w01 + c11.B*w11,
		A: c00.A*w00 + c10.A*w10 + c01.A*w01 + c11.A*w11,
	}
}
