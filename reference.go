package taskpkg

import "errors"

// PointClass is the exact topological classification of a test point.
type PointClass int

const (
	// PointExterior means the point is outside the closed mesh.
	PointExterior PointClass = iota
	// PointInterior means the point is inside the closed mesh.
	PointInterior
	// PointBoundary means the point lies exactly on a triangle of the mesh.
	PointBoundary
)

// rayTriangle returns the parameter t of the unique intersection between the
// ray origin + t*direction and the triangle (a,b,c). The boolean is false
// when the ray is parallel to the triangle plane or misses the triangle.
// The intersection must lie strictly inside the triangle: an edge or vertex
// hit is reported as a miss, which keeps the parity test well defined.
func rayTriangle(origin Point3, direction [3]*Rat, a, b, c Point3) (*Rat, bool) {
	e1 := sub(b, a)
	e2 := sub(c, a)
	p := cross(direction, e2)
	det := dot(e1, p)
	if det.Sign() == 0 {
		return nil, false
	}
	s := sub(origin, a)
	u := new(Rat).Quo(dot(s, p), det)
	if u.Sign() <= 0 {
		return nil, false
	}
	q := cross(s, e1)
	v := new(Rat).Quo(dot(direction, q), det)
	if v.Sign() <= 0 || new(Rat).Add(u, v).Cmp(RatFromInt(1)) >= 0 {
		return nil, false
	}
	t := new(Rat).Quo(dot(e2, q), det)
	return t, true
}

// OccludedByRay is the reference shadow test: the point is in shadow when the
// segment from the light to the point crosses the occluder strictly between
// its endpoints. All arithmetic is exact, and an exact boundary hit (the
// point itself lying on the occluder) is not counted as an occlusion.
func OccludedByRay(mesh *RationalMesh, light, point Point3) (bool, error) {
	if fi, on := lightOnSurface(light, mesh); on {
		return false, errors.New("reference: light lies on the surface of face " + itoa(fi))
	}
	for _, f := range mesh.Faces {
		if pointOnTriangle(point, mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]]) {
			return false, errors.New("reference: point lies on the occluder surface")
		}
	}
	direction := sub(point, light)
	one := RatFromInt(1)
	for _, f := range mesh.Faces {
		t, hit := rayTriangle(light, direction,
			mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]])
		if hit && t.Sign() > 0 && t.Cmp(one) < 0 {
			return true, nil
		}
	}
	return false, nil
}

// genericDirections are pairwise non-collinear irrational-looking integer
// directions used for parity ray tests. Their large, prime-weighted components
// make accidental alignment with triangle edges and vertices effectively
// impossible for rationally constructed test data.
var genericDirections = [][3]*Rat{
	{RatFromInt(3), RatFromInt(-7), RatFromInt(11)},
	{RatFromInt(-5), RatFromInt(2), RatFromInt(13)},
	{RatFromInt(7), RatFromInt(11), RatFromInt(-3)},
	{RatFromInt(-13), RatFromInt(5), RatFromInt(-2)},
	{RatFromInt(2), RatFromInt(13), RatFromInt(7)},
	{RatFromInt(17), RatFromInt(-19), RatFromInt(23)},
	{RatFromInt(-29), RatFromInt(31), RatFromInt(37)},
	{RatFromInt(41), RatFromInt(43), RatFromInt(-47)},
}

// parityCount counts strict triangle intersections of the ray origin+t*d with
// t > 0. Edge and vertex hits are skipped.
func parityCount(mesh *RationalMesh, origin Point3, direction [3]*Rat) int {
	count := 0
	for _, f := range mesh.Faces {
		t, hit := rayTriangle(origin, direction,
			mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]])
		if hit && t.Sign() > 0 {
			count++
		}
	}
	return count
}

// ClassifyPoint determines whether a point is strictly inside, strictly
// outside or exactly on the boundary of a closed oriented triangle mesh.
// Boundary points are detected first with exact barycentric tests; every other
// point is tested with several parity rays. A strict majority of the ray
// parities decides, so the occasional ray that grazes an edge or vertex
// (skipped as a non-strict triangle hit) cannot flip the classification.
func ClassifyPoint(mesh *RationalMesh, point Point3) (PointClass, error) {
	for _, f := range mesh.Faces {
		if pointOnTriangle(point, mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]]) {
			return PointBoundary, nil
		}
	}
	insideVotes, outsideVotes := 0, 0
	for _, d := range genericDirections {
		parity := parityCount(mesh, point, d) & 1
		if parity == 1 {
			insideVotes++
		} else {
			outsideVotes++
		}
	}
	if insideVotes == outsideVotes {
		return 0, errors.New("reference: parity rays tied; the point may be degenerate relative to the mesh")
	}
	if insideVotes > outsideVotes {
		return PointInterior, nil
	}
	return PointExterior, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
