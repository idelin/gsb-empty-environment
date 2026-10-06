// Package contour extracts 4-connected boundary loops from binary images.
//
// Coordinates: image cells are addressed with x growing right and y growing
// down, cell (x,y) covering the unit square [x,x+1]x[y,y+1]. Boundary loops
// are polygons on the integer lattice, emitted in image coordinates; outer
// loops have negative signed area (clockwise in math convention) and hole
// loops positive.
package contour

// Point is a vertex on the integer lattice (a corner of image cells).
type Point struct {
	X, Y int
}

// Image is a binary grid. Cells outside the bounds count as background.
type Image struct {
	W, H int
	fg   []bool
}

// NewImage returns an all-background image of the given size.
func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, fg: make([]bool, w*h)}
}

// Set marks cell (x,y) as foreground (v=true) or background (v=false).
func (im *Image) Set(x, y int, v bool) {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return
	}
	im.fg[y*im.W+x] = v
}

// At reports whether cell (x,y) is foreground. Out-of-bounds is background.
func (im *Image) At(x, y int) bool {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return false
	}
	return im.fg[y*im.W+x]
}

// FromRows builds an Image from text rows; '#', '1', 'x' and 'X' are
// foreground, anything else is background. Rows may have different lengths.
func FromRows(rows ...string) *Image {
	w := 0
	for _, r := range rows {
		if len(r) > w {
			w = len(r)
		}
	}
	im := NewImage(w, len(rows))
	for y, r := range rows {
		for x := 0; x < len(r); x++ {
			switch r[x] {
			case '#', '1', 'x', 'X':
				im.Set(x, y, true)
			}
		}
	}
	return im
}
