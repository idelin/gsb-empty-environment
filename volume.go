package taskpkg

import (
	"errors"
	"fmt"
)

// FaceSide reports how a single oriented triangle sits relative to the light.
//
// The sign is taken from the exact rational determinant
//
//	(b-a) × (c-a) · (light-a)
//
// where the triangle vertices are ordered so that the cross product points to
// the exterior of the closed input mesh. A strictly positive value means the
// light is on the outward side and the face is lit; a strictly negative value
// means it is a back-facing face.
//
// Fixed sign perturbation rule: a zero determinant (the light lies in the
// plane of the triangle) is deterministically treated as back-facing, i.e.
// the sign is perturbed to -1. The same rule is applied uniformly to every
// face, so the resulting silhouette still separates lit from non-lit faces
// and the produced shadow volume remains closed.
func FaceSide(a, b, c, light Point3) int {
	d := det3(sub(b, a), sub(c, a), sub(light, a))
	if d.Sign() > 0 {
		return 1
	}
	return -1
}

type edgeKey struct{ a, b int }

// validateOrientedMesh checks index ranges, rejects degenerate triangles and
// verifies that every directed edge occurs exactly once. For a closed
// consistently oriented triangle mesh this implies every undirected edge is
// shared by precisely two oppositely wound faces.
func validateOrientedMesh(mesh *RationalMesh) (map[edgeKey]int, error) {
	if len(mesh.Vertices) < 4 {
		return nil, errors.New("taskpkg: mesh needs at least four vertices")
	}
	if len(mesh.Faces) == 0 {
		return nil, errors.New("taskpkg: mesh has no faces")
	}
	for i, f := range mesh.Faces {
		for _, idx := range f {
			if idx < 0 || idx >= len(mesh.Vertices) {
				return nil, fmt.Errorf("taskpkg: face %d references vertex %d out of range", i, idx)
			}
		}
		if f[0] == f[1] || f[1] == f[2] || f[2] == f[0] {
			return nil, fmt.Errorf("taskpkg: face %d is degenerate", i)
		}
		if area2(mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]]).Sign() == 0 {
			return nil, fmt.Errorf("taskpkg: face %d has zero area", i)
		}
	}

	directed := make(map[edgeKey]int, 3*len(mesh.Faces))
	for fi, f := range mesh.Faces {
		edges := [3]edgeKey{
			{f[0], f[1]},
			{f[1], f[2]},
			{f[2], f[0]},
		}
		for _, e := range edges {
			if other, dup := directed[e]; dup {
				return nil, fmt.Errorf("taskpkg: directed edge (%d,%d) shared by faces %d and %d; mesh is not consistently oriented", e.a, e.b, other, fi)
			}
			directed[e] = fi
		}
	}
	for e, f := range directed {
		opp, ok := directed[edgeKey{e.b, e.a}]
		if !ok {
			return nil, fmt.Errorf("taskpkg: edge (%d,%d) of face %d is a boundary edge; mesh must be closed", e.a, e.b, f)
		}
		if opp == f {
			return nil, fmt.Errorf("taskpkg: face %d contains an edge in both directions", f)
		}
	}
	return directed, nil
}

func area2(a, b, c Point3) *Rat {
	n := cross(sub(b, a), sub(c, a))
	return dot(n, n)
}

// pointOnTriangle reports whether p lies on the closed triangle (a,b,c),
// using exact rational barycentric coordinates.
func pointOnTriangle(p, a, b, c Point3) bool {
	v0 := sub(b, a)
	v1 := sub(c, a)
	v2 := sub(p, a)
	n := cross(v0, v1)
	// Coplanarity: v2 must lie in the span of the two edge vectors, i.e. the
	// scalar triple product with the triangle normal must vanish.
	if dot(n, v2).Sign() != 0 {
		return false
	}
	d00 := dot(v0, v0)
	d01 := dot(v0, v1)
	d11 := dot(v1, v1)
	d20 := dot(v2, v0)
	d21 := dot(v2, v1)
	denom := new(Rat).Sub(new(Rat).Mul(d00, d11), new(Rat).Mul(d01, d01))
	if denom.Sign() == 0 {
		return false
	}
	vNum := new(Rat).Sub(new(Rat).Mul(d11, d20), new(Rat).Mul(d01, d21))
	wNum := new(Rat).Sub(new(Rat).Mul(d00, d21), new(Rat).Mul(d01, d20))
	// v = vNum/denom, w = wNum/denom. Normalize the comparison by the
	// denominator sign so the predicate works for either winding.
	ds := denom.Sign()
	if vNum.Sign()*ds < 0 {
		return false
	}
	if wNum.Sign()*ds < 0 {
		return false
	}
	vwNum := new(Rat).Add(vNum, wNum)
	// v + w <= 1  <=>  (vwNum - denom)/denom <= 0, so numerator and
	// denominator must have opposite signs (or the numerator must be zero).
	if new(Rat).Sub(vwNum, denom).Sign()*ds > 0 {
		return false
	}
	return true
}

func lightOnSurface(light Point3, mesh *RationalMesh) (int, bool) {
	for fi, f := range mesh.Faces {
		if pointOnTriangle(light, mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]]) {
			return fi, true
		}
	}
	return 0, false
}

// rayPlaneParameter returns t such that origin + t*direction lies on plane.
// It fails when the ray is parallel to the plane.
func rayPlaneParameter(origin Point3, direction [3]*Rat, plane Plane) (*Rat, error) {
	denom := dot(plane.Normal, direction)
	if denom.Sign() == 0 {
		return nil, errors.New("taskpkg: light ray is parallel to the far plane")
	}
	num := dot(plane.Normal, sub(plane.Point, origin))
	return new(Rat).Quo(num, denom), nil
}

// BuildShadowVolume constructs the closed shadow volume cast by a closed,
// consistently oriented triangle mesh for a point light that is not on its
// surface.
//
// The returned mesh keeps every original vertex at its original index and
// appends one extruded vertex per silhouette endpoint; all faces sharing an
// extruded endpoint reference the same new vertex ID. Near-cap faces are the
// lit input faces, far-cap faces are their reversed extruded copies, and each
// silhouette edge becomes an outward-wound quad. The output is watertight and
// uniformly oriented outward.
func BuildShadowVolume(mesh *RationalMesh, light Point3, far Plane) (*RationalMesh, error) {
	directed, err := validateOrientedMesh(mesh)
	if err != nil {
		return nil, err
	}
	if fi, on := lightOnSurface(light, mesh); on {
		return nil, fmt.Errorf("taskpkg: light lies on the surface of face %d", fi)
	}

	side := make([]int, len(mesh.Faces))
	litCount, backCount := 0, 0
	for fi, f := range mesh.Faces {
		side[fi] = FaceSide(mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]], light)
		if side[fi] > 0 {
			litCount++
		} else {
			backCount++
		}
	}
	switch {
	case litCount == 0:
		return nil, errors.New("taskpkg: no face is lit; the light appears to be inside the mesh")
	case backCount == 0:
		return nil, errors.New("taskpkg: every face is lit; the light appears to be inside the mesh")
	}

	type silhouette struct{ a, b int }
	var rims []silhouette
	for e, fi := range directed {
		if side[fi] <= 0 {
			continue
		}
		if side[directed[edgeKey{e.b, e.a}]] > 0 {
			continue
		}
		rims = append(rims, silhouette{e.a, e.b})
	}
	if len(rims) == 0 {
		return nil, errors.New("taskpkg: silhouette is empty; cannot build a shadow volume")
	}

	out := &RationalMesh{
		Vertices: make([]Point3, len(mesh.Vertices)),
	}
	for i, v := range mesh.Vertices {
		out.Vertices[i] = pointCopy(v)
	}

	farID := make(map[int]int)
	extrude := func(id int) (int, error) {
		if fid, ok := farID[id]; ok {
			return fid, nil
		}
		direction := sub(mesh.Vertices[id], light)
		t, err := rayPlaneParameter(light, direction, far)
		if err != nil {
			return 0, err
		}
		if t.Cmp(RatFromInt(1)) <= 0 {
			return 0, fmt.Errorf("taskpkg: far plane intersection for vertex %d is not beyond the source point", id)
		}
		fid := len(out.Vertices)
		farID[id] = fid
		out.Vertices = append(out.Vertices, add(light, direction, t))
		return fid, nil
	}

	// Near cap: lit faces copied with their original outward winding.
	for fi, f := range mesh.Faces {
		if side[fi] > 0 {
			out.Faces = append(out.Faces, Triangle{f[0], f[1], f[2]})
		}
	}

	// Far cap: extruded lit faces with reversed winding so their outward
	// normals point away from the shadow volume.
	for fi, f := range mesh.Faces {
		if side[fi] <= 0 {
			continue
		}
		f0, err := extrude(f[0])
		if err != nil {
			return nil, err
		}
		f1, err := extrude(f[1])
		if err != nil {
			return nil, err
		}
		f2, err := extrude(f[2])
		if err != nil {
			return nil, err
		}
		out.Faces = append(out.Faces, Triangle{f2, f1, f0})
	}

	// Side walls: one outward-wound quad per silhouette edge. The edge
	// (a -> b) follows the winding of its lit face.
	for _, rim := range rims {
		fa, err := extrude(rim.a)
		if err != nil {
			return nil, err
		}
		fb, err := extrude(rim.b)
		if err != nil {
			return nil, err
		}
		out.Faces = append(out.Faces,
			Triangle{rim.a, fa, fb},
			Triangle{rim.a, fb, rim.b},
		)
	}

	return out, nil
}
