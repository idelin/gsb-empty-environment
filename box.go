package bboxidx

// Box is an axis-aligned bounding box. A box with Min > Max on any axis is
// empty: it contains no points and intersects nothing.
type Box struct {
	MinX, MinY, MaxX, MaxY float64
}

// IsEmpty reports whether the box has no extent (Min > Max on any axis).
// Point boxes (Min == Max) are not empty.
func (b Box) IsEmpty() bool {
	return b.MinX > b.MaxX || b.MinY > b.MaxY
}

// Intersects reports whether two boxes overlap, including touching edges.
// Empty boxes intersect nothing.
func (b Box) Intersects(o Box) bool {
	if b.IsEmpty() || o.IsEmpty() {
		return false
	}
	return b.MinX <= o.MaxX && o.MinX <= b.MaxX &&
		b.MinY <= o.MaxY && o.MinY <= b.MaxY
}

// Union returns the smallest box containing both b and o. Empty boxes are
// ignored.
func (b Box) Union(o Box) Box {
	if o.IsEmpty() {
		return b
	}
	if b.IsEmpty() {
		return o
	}
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

func (b Box) centerX() float64 { return (b.MinX + b.MaxX) / 2 }
func (b Box) centerY() float64 { return (b.MinY + b.MaxY) / 2 }
