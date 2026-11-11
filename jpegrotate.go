// Package jpegrotate performs lossless transforms (rotation by 90/180/270
// degrees and mirroring) on baseline JPEG images by decoding the entropy
// coded data to quantized DCT coefficients, rearranging and transforming
// the coefficients, and re-encoding without re-quantization.
package jpegrotate

import (
	"fmt"
	"io"
)

// Transform is a lossless DCT-domain image transform.
type Transform int

const (
	// Rot90 rotates the image 90 degrees clockwise.
	Rot90 Transform = iota
	// Rot180 rotates the image 180 degrees.
	Rot180
	// Rot270 rotates the image 270 degrees clockwise (90 counter-clockwise).
	Rot270
	// FlipH mirrors the image horizontally.
	FlipH
	// FlipV mirrors the image vertically.
	FlipV
)

func (t Transform) String() string {
	switch t {
	case Rot90:
		return "Rot90"
	case Rot180:
		return "Rot180"
	case Rot270:
		return "Rot270"
	case FlipH:
		return "FlipH"
	case FlipV:
		return "FlipV"
	}
	return "Unknown"
}

// Options controls transform behavior.
type Options struct {
	// Trim controls how images whose dimensions are not a multiple of the
	// MCU size are handled. If Trim is true, the partial MCUs along the
	// right and bottom edges are discarded (the image is cropped down to
	// a whole number of MCUs) before applying the transform. If Trim is
	// false, such images are rejected with an error.
	Trim bool
}

// TransformJPEG applies t to the baseline JPEG read from r and writes the
// transformed JPEG to w. The DCT coefficients are transformed directly, so
// no re-quantization (and thus no generation loss) occurs.
func TransformJPEG(w io.Writer, r io.Reader, t Transform, opts Options) error {
	if t < Rot90 || t > FlipV {
		return fmt.Errorf("jpegrotate: invalid transform %d", int(t))
	}
	j, err := parseJPEG(r)
	if err != nil {
		return err
	}
	if err := j.apply(t, opts); err != nil {
		return err
	}
	return j.encode(w)
}

// block holds the 64 quantized DCT coefficients of one 8x8 block in
// natural (row-major frequency) order. The DC coefficient is stored as an
// absolute (de-predicted) value.
type block [64]int32

// compCoeffs holds the block grid of one component.
type compCoeffs struct {
	bw, bh int // grid size in blocks
	blocks []block
}

// component describes one scan/frame component.
type component struct {
	id     int
	h, v   int // sampling factors
	tq     int // quantization table selector
	td, ta int // Huffman table selectors
	dc, ac *huffTable
}

// jpegImage is a parsed baseline JPEG with decoded coefficients.
type jpegImage struct {
	sofMarker       byte
	width, height   int
	hmax, vmax      int
	comps           []*component
	coefs           []compCoeffs
	qt              [4][64]int32 // natural order
	qtSet           [4]bool
	dhtRaw          [][]byte // raw DHT segments, re-emitted verbatim
	appSegs         [][]byte // raw APPn/COM segments, re-emitted verbatim
	restartInterval int
	entropy         []byte // un-stuffed entropy data with RST markers removed
	rstCount        int
}

// mcuSize returns the MCU dimensions in pixels.
func (j *jpegImage) mcuSize() (int, int) {
	return 8 * j.hmax, 8 * j.vmax
}

// mcuCounts returns the number of MCUs horizontally and vertically,
// counting partial edge MCUs as whole ones.
func (j *jpegImage) mcuCounts() (int, int) {
	mw, mh := j.mcuSize()
	return (j.width + mw - 1) / mw, (j.height + mh - 1) / mh
}
