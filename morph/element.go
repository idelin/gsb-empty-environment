// Package morph implements grayscale morphological dilation and erosion
// over 8-bit images with arbitrary finite structuring elements.
//
// Pixels outside the image are treated as fixed zero for BOTH operations.
// In particular, erosion does NOT skip out-of-image samples: any
// structuring-element point that falls outside the image contributes a
// zero to the minimum, so eroded border pixels are pulled toward 0.
//
// Input may be streamed in row blocks (top to bottom); the streaming
// result is identical to a one-shot whole-image computation. Only a
// bounded number of rows (the vertical span of the structuring element)
// is ever kept in memory.
package morph

import "errors"

// ErrEmptyElement is returned when a structuring element has no points.
// The empty structuring element is rejected: dilation/erosion over an
// empty neighborhood is not defined by this library.
var ErrEmptyElement = errors.New("morph: empty structuring element")

// Point is a structuring-element offset (dx, dy) relative to the origin.
// dy > 0 points downward (toward larger row indices).
type Point struct {
	X, Y int
}

// Element is a finite structuring element: a non-empty set of offsets.
type Element struct {
	pts          []Point
	minDY, maxDY int
}

// NewElement builds a structuring element from the given offsets.
// Duplicate offsets are collapsed. An empty element is rejected.
func NewElement(pts ...Point) (Element, error) {
	if len(pts) == 0 {
		return Element{}, ErrEmptyElement
	}
	seen := make(map[Point]bool, len(pts))
	el := Element{pts: make([]Point, 0, len(pts))}
	for _, p := range pts {
		if seen[p] {
			continue
		}
		seen[p] = true
		if len(el.pts) == 0 {
			el.minDY, el.maxDY = p.Y, p.Y
		} else {
			if p.Y < el.minDY {
				el.minDY = p.Y
			}
			if p.Y > el.maxDY {
				el.maxDY = p.Y
			}
		}
		el.pts = append(el.pts, p)
	}
	return el, nil
}

// Points returns the offsets of the element.
func (e Element) Points() []Point {
	out := make([]Point, len(e.pts))
	copy(out, e.pts)
	return out
}

// rowSpan is the number of distinct input rows that must be buffered to
// compute any single output row.
func (e Element) rowSpan() int { return e.maxDY - e.minDY + 1 }
