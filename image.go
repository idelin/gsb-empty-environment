// Package contour extracts 4-connected boundary loops from binary images.
//
// Model conventions:
//   - Foreground connectivity is 4-neighbourhood, so regions that touch only
//     at a corner (diagonally) are treated as separate regions.
//   - Background connectivity is the Jordan-dual 8-neighbourhood: a diagonal
//     slit of background stays connected to the outside and is not a hole,
//     while a fully surrounded single background pixel is a hole.
//   - Pixel (x, y) covers the unit square [x, x+1] x [y, y+1] with y growing
//     downward. Boundaries are directed unit edges ("cracks") on the integer
//     grid, oriented so the foreground pixel lies on the left of the walk
//     direction. Outer loops therefore wind clockwise in image coordinates
//     and hole loops counter-clockwise.
package contour

import "strings"

// Image is a binary bitmap. Pix is row-major: Pix[y*W+x].
type Image struct {
	W, H int
	Pix  []bool
}

// NewImage returns an all-background image of the given size.
func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, Pix: make([]bool, w*h)}
}

// At reports the pixel value; out-of-bounds reads are background.
func (im *Image) At(x, y int) bool {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return false
	}
	return im.Pix[y*im.W+x]
}

// Set writes a pixel, ignoring out-of-bounds coordinates.
func (im *Image) Set(x, y int, v bool) {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return
	}
	im.Pix[y*im.W+x] = v
}

// ImageFromASCII builds an image from text rows where '#' is foreground.
func ImageFromASCII(rows ...string) *Image {
	h := len(rows)
	w := 0
	for _, r := range rows {
		if len(r) > w {
			w = len(r)
		}
	}
	im := NewImage(w, h)
	for y, r := range rows {
		for x, c := range r {
			if c == '#' {
				im.Set(x, y, true)
			}
		}
	}
	return im
}

// String renders the image with '#' for foreground and '.' for background.
func (im *Image) String() string {
	var b strings.Builder
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			if im.At(x, y) {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// equal reports whether two images have identical size and pixels.
func (im *Image) equal(other *Image) bool {
	if im.W != other.W || im.H != other.H {
		return false
	}
	for i := range im.Pix {
		if im.Pix[i] != other.Pix[i] {
			return false
		}
	}
	return true
}
