package meshsimp

import "math"

// DistanceReport summarizes the geometric distance from a sampled source
// surface to the triangles of a target mesh.
type DistanceReport struct {
	// Max is the largest sampled point-to-surface distance.
	Max float64
	// RMS is the root mean square sampled distance.
	RMS float64
	// Samples is the number of sampled points.
	Samples int
}

// SampleMesh returns points covering the mesh surface: every vertex, every
// edge midpoint and a subdivisions-per-side barycentric grid inside each
// triangle. subdivisions < 2 yields vertices and edge midpoints only.
func SampleMesh(m *Mesh, subdivisions int) [][3]float64 {
	seen := make(map[[3]float64]struct{}, len(m.Positions))
	var points [][3]float64
	add := func(p [3]float64) {
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		points = append(points, p)
	}

	for _, p := range m.Positions {
		add(p)
	}
	for _, f := range m.Faces {
		for k := 0; k < 3; k++ {
			a := m.Positions[f[k]]
			b := m.Positions[f[(k+1)%3]]
			add([3]float64{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2, (a[2] + b[2]) / 2})
		}
		if subdivisions < 2 {
			continue
		}
		n := subdivisions
		for i := 0; i <= n; i++ {
			for j := 0; j <= n-i; j++ {
				l1 := float64(i) / float64(n)
				l2 := float64(j) / float64(n)
				l3 := 1 - l1 - l2
				p0, p1, p2 := m.Positions[f[0]], m.Positions[f[1]], m.Positions[f[2]]
				add([3]float64{
					l1*p0[0] + l2*p1[0] + l3*p2[0],
					l1*p0[1] + l2*p1[1] + l3*p2[1],
					l1*p0[2] + l2*p1[2] + l3*p2[2],
				})
			}
		}
	}
	return points
}

// DistanceToMesh measures the distance of each sampled source point to the
// closest triangle of target. The samples are taken with SampleMesh using
// subdivisions for both meshes.
func DistanceToMesh(source, target *Mesh, subdivisions int) DistanceReport {
	samples := SampleMesh(source, subdivisions)
	var maxD, sumSq float64
	for _, p := range samples {
		d := pointToMeshDistance(p, target)
		if d > maxD {
			maxD = d
		}
		sumSq += d * d
	}
	rms := 0.0
	if len(samples) > 0 {
		rms = math.Sqrt(sumSq / float64(len(samples)))
	}
	return DistanceReport{Max: maxD, RMS: rms, Samples: len(samples)}
}

// SymmetricHausdorff returns the larger one-sided Max distance between the
// two meshes, a conservative bound on the two-sided Hausdorff distance at
// the chosen sampling density.
func SymmetricHausdorff(a, b *Mesh, subdivisions int) float64 {
	d1 := DistanceToMesh(a, b, subdivisions).Max
	d2 := DistanceToMesh(b, a, subdivisions).Max
	if d1 > d2 {
		return d1
	}
	return d2
}

func pointToMeshDistance(p [3]float64, m *Mesh) float64 {
	best := math.Inf(1)
	for _, f := range m.Faces {
		d := pointToTriangleDistance(
			p, m.Positions[f[0]], m.Positions[f[1]], m.Positions[f[2]])
		if d < best {
			best = d
		}
	}
	return best
}

// pointToTriangleDistance is the standard closest-point distance: it tests
// the triangle interior plane projection and the three edge segments.
func pointToTriangleDistance(p, a, b, c [3]float64) float64 {
	ab := sub(b, a)
	ac := sub(c, a)
	ap := sub(p, a)
	d1 := dot(ab, ap)
	d2 := dot(ac, ap)
	if d1 <= 0 && d2 <= 0 {
		return norm(ap)
	}

	bp := sub(p, b)
	d3 := dot(ab, bp)
	d4 := dot(ac, bp)
	if d3 >= 0 && d4 <= d3 {
		return norm(bp)
	}

	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		v := d1 / (d1 - d3)
		return norm(sub(p, [3]float64{
			a[0] + v*ab[0], a[1] + v*ab[1], a[2] + v*ab[2],
		}))
	}

	cp := sub(p, c)
	d5 := dot(ab, cp)
	d6 := dot(ac, cp)
	if d6 >= 0 && d5 <= d6 {
		return norm(cp)
	}

	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		w := d2 / (d2 - d6)
		return norm(sub(p, [3]float64{
			a[0] + w*ac[0], a[1] + w*ac[1], a[2] + w*ac[2],
		}))
	}

	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		bc := sub(c, b)
		return norm(sub(p, [3]float64{
			b[0] + w*bc[0], b[1] + w*bc[1], b[2] + w*bc[2],
		}))
	}

	// Closest point is inside the triangle: project onto its plane.
	n := cross(ab, ac)
	area2 := dot(n, n)
	planeDist := dot(ap, n)
	return math.Abs(planeDist) / math.Sqrt(area2)
}
