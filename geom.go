package delaunay

// orient2d returns a positive value when p lies to the left of the directed
// edge a->b (i.e. a, b, p are counter-clockwise), negative when to the right,
// and zero when collinear.
func orient2d(a, b, p Point) float64 {
	return (b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X)
}

// inCircle evaluates the incircle determinant for the CCW triangle (a, b, c).
// It returns a positive value when p lies strictly inside the circumcircle
// of (a, b, c), zero when cocircular, and negative when outside.
func inCircle(a, b, c, p Point) float64 {
	ax := a.X - p.X
	ay := a.Y - p.Y
	bx := b.X - p.X
	by := b.Y - p.Y
	cx := c.X - p.X
	cy := c.Y - p.Y
	return (ax*ax+ay*ay)*(bx*cy-cx*by) -
		(bx*bx+by*by)*(ax*cy-cx*ay) +
		(cx*cx+cy*cy)*(ax*by-bx*ay)
}

// Circumcircle returns the center and squared radius of the circle passing
// through a, b and c.
func Circumcircle(a, b, c Point) (center Point, r2 float64) {
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	a2 := a.X*a.X + a.Y*a.Y
	b2 := b.X*b.X + b.Y*b.Y
	c2 := c.X*c.X + c.Y*c.Y
	center = Point{
		X: (a2*(b.Y-c.Y) + b2*(c.Y-a.Y) + c2*(a.Y-b.Y)) / d,
		Y: (a2*(c.X-b.X) + b2*(a.X-c.X) + c2*(b.X-a.X)) / d,
	}
	r2 = (a.X-center.X)*(a.X-center.X) + (a.Y-center.Y)*(a.Y-center.Y)
	return center, r2
}

// onEdge reports whether p lies (numerically) on the segment a-b.
func onEdge(a, b, p Point) bool {
	dx := b.X - a.X
	dy := b.Y - a.Y
	len2 := dx*dx + dy*dy
	tol := 1e-12 * (len2 + 1)
	cross := orient2d(a, b, p)
	if cross < -tol || cross > tol {
		return false
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / len2
	return t >= -1e-9 && t <= 1+1e-9
}

// rotate3 cyclically shifts a triple so that index k becomes index 0.
// Cyclic shifts preserve the counter-clockwise orientation.
func rotate3(v [3]int, k int) [3]int {
	return [3]int{v[k%3], v[(k+1)%3], v[(k+2)%3]}
}
