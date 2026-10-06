package bboxindex

// Box is an axis-aligned bounding box. A box is empty when Min > Max on
// any axis; empty boxes never intersect anything, including themselves.
type Box struct {
	MinX, MinY, MaxX, MaxY float64
}

// Empty reports whether the box is inverted (Min > Max on some axis).
// Zero-area boxes (points, slivers) are not empty.
func (b Box) Empty() bool {
	return b.MinX > b.MaxX || b.MinY > b.MaxY
}

// Intersects reports whether two boxes overlap. Empty boxes intersect
// nothing.
func (b Box) Intersects(o Box) bool {
	if b.Empty() || o.Empty() {
		return false
	}
	return b.MinX <= o.MaxX && o.MinX <= b.MaxX &&
		b.MinY <= o.MaxY && o.MinY <= b.MaxY
}

// union returns the smallest box containing both b and o.
func union(b, o Box) Box {
	if o.MinX < b.MinX {
		b.MinX = o.MinX
	}
	if o.MinY < b.MinY {
		b.MinY = o.MinY
	}
	if o.MaxX > b.MaxX {
		b.MaxX = o.MaxX
	}
	if o.MaxY > b.MaxY {
		b.MaxY = o.MaxY
	}
	return b
}

func centerX(b Box) float64 { return (b.MinX + b.MaxX) / 2 }
