// Package morph implements grayscale morphological dilation and erosion
// with arbitrary finite structuring elements over uint8 images.
//
// Pixels outside the image are fixed to zero for both operations. Input
// rows may be streamed in blocks; streaming results are identical to a
// single whole-image computation.
package morph

import "errors"

// ErrEmptyElement is returned when a structuring element has no offsets.
var ErrEmptyElement = errors.New("morph: structuring element must contain at least one offset")

// Point is a structuring-element offset. X is the column delta, Y the row
// delta relative to the output pixel.
type Point struct {
	X, Y int
}

// Element is a finite structuring element: a set of pixel offsets. It may
// be asymmetric and need not contain the origin.
type Element struct {
	offsets      []Point
	minDX, maxDX int
	minDY, maxDY int
}

// NewElement builds a structuring element from offsets. An empty offset
// list is rejected with ErrEmptyElement.
func NewElement(offsets []Point) (Element, error) {
	if len(offsets) == 0 {
		return Element{}, ErrEmptyElement
	}
	el := Element{offsets: make([]Point, len(offsets))}
	copy(el.offsets, offsets)
	el.minDX, el.maxDX = offsets[0].X, offsets[0].X
	el.minDY, el.maxDY = offsets[0].Y, offsets[0].Y
	for _, p := range offsets[1:] {
		if p.X < el.minDX {
			el.minDX = p.X
		}
		if p.X > el.maxDX {
			el.maxDX = p.X
		}
		if p.Y < el.minDY {
			el.minDY = p.Y
		}
		if p.Y > el.maxDY {
			el.maxDY = p.Y
		}
	}
	return el, nil
}

// Offsets returns a copy of the element's offsets.
func (el Element) Offsets() []Point {
	out := make([]Point, len(el.offsets))
	copy(out, el.offsets)
	return out
}

// RowSpan is the number of distinct input rows that can contribute to one
// output row; it bounds the streaming row cache.
func (el Element) RowSpan() int { return el.maxDY - el.minDY + 1 }
