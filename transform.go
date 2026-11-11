package jpegrotate

import "fmt"

// The DCT is separable, so geometric transforms of an 8x8 sample block map
// to exact transforms of its (quantized) DCT coefficients:
//
//   transpose:        C'[u][v] = C[v][u]
//   flip columns:     C'[u][v] = C[u][v] * (-1)^v
//   flip rows:        C'[u][v] = C[u][v] * (-1)^u
//
// Rotations and mirrors decompose into these primitives. Because the
// quantization step is per-frequency, a transpose also requires
// transposing the quantization table.

func rot90Block(b block) (o block) {
	// Rotate 90 clockwise = transpose, then flip columns.
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			c := b[v*8+u]
			if v&1 != 0 {
				c = -c
			}
			o[u*8+v] = c
		}
	}
	return o
}

func rot270Block(b block) (o block) {
	// Rotate 270 clockwise = transpose, then flip rows.
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			c := b[v*8+u]
			if u&1 != 0 {
				c = -c
			}
			o[u*8+v] = c
		}
	}
	return o
}

func rot180Block(b block) (o block) {
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			c := b[u*8+v]
			if (u+v)&1 != 0 {
				c = -c
			}
			o[u*8+v] = c
		}
	}
	return o
}

func flipHBlock(b block) (o block) {
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			c := b[u*8+v]
			if v&1 != 0 {
				c = -c
			}
			o[u*8+v] = c
		}
	}
	return o
}

func flipVBlock(b block) (o block) {
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			c := b[u*8+v]
			if u&1 != 0 {
				c = -c
			}
			o[u*8+v] = c
		}
	}
	return o
}

// rot90 rotates the block grid 90 degrees clockwise. The grid dimensions
// swap, and each block's coefficients are rotated as well.
func (c *compCoeffs) rot90() {
	nbw, nbh := c.bh, c.bw
	out := make([]block, len(c.blocks))
	for by := 0; by < c.bh; by++ {
		for bx := 0; bx < c.bw; bx++ {
			out[bx*nbw+(c.bh-1-by)] = rot90Block(c.blocks[by*c.bw+bx])
		}
	}
	c.bw, c.bh, c.blocks = nbw, nbh, out
}

func (c *compCoeffs) rot270() {
	nbw, nbh := c.bh, c.bw
	out := make([]block, len(c.blocks))
	for by := 0; by < c.bh; by++ {
		for bx := 0; bx < c.bw; bx++ {
			out[(c.bw-1-bx)*nbw+by] = rot270Block(c.blocks[by*c.bw+bx])
		}
	}
	c.bw, c.bh, c.blocks = nbw, nbh, out
}

func (c *compCoeffs) rot180() {
	out := make([]block, len(c.blocks))
	for by := 0; by < c.bh; by++ {
		for bx := 0; bx < c.bw; bx++ {
			out[(c.bh-1-by)*c.bw+(c.bw-1-bx)] = rot180Block(c.blocks[by*c.bw+bx])
		}
	}
	c.blocks = out
}

func (c *compCoeffs) flipH() {
	out := make([]block, len(c.blocks))
	for by := 0; by < c.bh; by++ {
		for bx := 0; bx < c.bw; bx++ {
			out[by*c.bw+(c.bw-1-bx)] = flipHBlock(c.blocks[by*c.bw+bx])
		}
	}
	c.blocks = out
}

func (c *compCoeffs) flipV() {
	out := make([]block, len(c.blocks))
	for by := 0; by < c.bh; by++ {
		for bx := 0; bx < c.bw; bx++ {
			out[(c.bh-1-by)*c.bw+bx] = flipVBlock(c.blocks[by*c.bw+bx])
		}
	}
	c.blocks = out
}

// crop shrinks the block grid to bw x bh blocks, dropping the rightmost
// columns and bottom rows.
func (c *compCoeffs) crop(bw, bh int) {
	out := make([]block, bw*bh)
	for by := 0; by < bh; by++ {
		copy(out[by*bw:(by+1)*bw], c.blocks[by*c.bw:by*c.bw+bw])
	}
	c.bw, c.bh, c.blocks = bw, bh, out
}

// transposeQuant transposes all quantization tables in place. Required for
// transpose-type transforms (Rot90, Rot270).
func (j *jpegImage) transposeQuant() {
	for tq := 0; tq < 4; tq++ {
		if !j.qtSet[tq] {
			continue
		}
		var o [64]int32
		for u := 0; u < 8; u++ {
			for v := 0; v < 8; v++ {
				o[u*8+v] = j.qt[tq][v*8+u]
			}
		}
		j.qt[tq] = o
	}
}

// swapSampling swaps the horizontal and vertical sampling factors of every
// component. Required for transpose-type transforms so that, e.g., a
// 2x1-sampled component becomes 1x2-sampled.
func (j *jpegImage) swapSampling() {
	j.hmax, j.vmax = 0, 0
	for _, c := range j.comps {
		c.h, c.v = c.v, c.h
		if c.h > j.hmax {
			j.hmax = c.h
		}
		if c.v > j.vmax {
			j.vmax = c.v
		}
	}
}

// trimPartialMCUs drops the partial MCUs along the right and bottom edges
// and rounds the image dimensions down to a whole number of MCUs.
func (j *jpegImage) trimPartialMCUs() error {
	mw, mh := j.mcuSize()
	mcusX, mcusY := j.mcuCounts()
	if j.width%mw != 0 {
		mcusX--
	}
	if j.height%mh != 0 {
		mcusY--
	}
	if mcusX < 1 || mcusY < 1 {
		return fmt.Errorf("jpegrotate: image %dx%d is smaller than one MCU (%dx%d)", j.width, j.height, mw, mh)
	}
	j.width, j.height = mcusX*mw, mcusY*mh
	for i, c := range j.comps {
		j.coefs[i].crop(mcusX*c.h, mcusY*c.v)
	}
	return nil
}

// apply performs the transform on the decoded coefficients.
func (j *jpegImage) apply(t Transform, opts Options) error {
	mw, mh := j.mcuSize()
	if j.width%mw != 0 || j.height%mh != 0 {
		if !opts.Trim {
			return fmt.Errorf("jpegrotate: image size %dx%d is not a multiple of the MCU size %dx%d; set Options.Trim to crop the partial edge MCUs", j.width, j.height, mw, mh)
		}
		if err := j.trimPartialMCUs(); err != nil {
			return err
		}
	}
	switch t {
	case Rot90:
		for i := range j.coefs {
			j.coefs[i].rot90()
		}
		j.transposeQuant()
		j.swapSampling()
		j.width, j.height = j.height, j.width
	case Rot270:
		for i := range j.coefs {
			j.coefs[i].rot270()
		}
		j.transposeQuant()
		j.swapSampling()
		j.width, j.height = j.height, j.width
	case Rot180:
		for i := range j.coefs {
			j.coefs[i].rot180()
		}
	case FlipH:
		for i := range j.coefs {
			j.coefs[i].flipH()
		}
	case FlipV:
		for i := range j.coefs {
			j.coefs[i].flipV()
		}
	}
	return nil
}
