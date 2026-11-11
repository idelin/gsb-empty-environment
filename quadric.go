package meshsimp

import "math"

// quadric is the symmetric 4x4 fundamental error quadric Q = Kp^T Kp
// accumulated from incident face planes. Only the upper triangle entries are
// stored: xx, xy, xz, xw, yy, yz, yw, zz, zw, ww.
type quadric struct {
	xx, xy, xz, xw float64
	yy, yz, yw     float64
	zz, zw         float64
	ww             float64
}

func (q *quadric) add(o quadric) {
	q.xx += o.xx
	q.xy += o.xy
	q.xz += o.xz
	q.xw += o.xw
	q.yy += o.yy
	q.yz += o.yz
	q.yw += o.yw
	q.zz += o.zz
	q.zw += o.zw
	q.ww += o.ww
}

// planeQuadric builds the outer-product quadric of the plane n^T x + d = 0.
func planeQuadric(n [3]float64, d float64) quadric {
	return quadric{
		xx: n[0] * n[0],
		xy: n[0] * n[1],
		xz: n[0] * n[2],
		xw: n[0] * d,
		yy: n[1] * n[1],
		yz: n[1] * n[2],
		yw: n[1] * d,
		zz: n[2] * n[2],
		zw: n[2] * d,
		ww: d * d,
	}
}

// errorAt evaluates v^T Q v for the homogeneous point v = (x, y, z, 1).
func (q quadric) errorAt(p [3]float64) float64 {
	x, y, z := p[0], p[1], p[2]
	return q.xx*x*x + 2*q.xy*x*y + 2*q.xz*x*z + 2*q.xw*x +
		q.yy*y*y + 2*q.yz*y*z + 2*q.yw*y +
		q.zz*z*z + 2*q.zw*z + q.ww
}

// bestPosition minimizes the QEM of the combined quadric q over points lying
// on the segment between endpoints u and v. When the linear system is
// singular (or its solution escapes the segment too far) it falls back to
// whichever of u, v or their midpoint has the smallest error.
func bestPosition(q quadric, u, v [3]float64) [3]float64 {
	p, ok := optimalPoint(q, u, v)
	if ok {
		return p
	}
	return minErrorPoint(q, u, v)
}

// optimalPoint solves the unconstrained QEM minimum. ok is false when the
// system is singular or the solution runs far away from the u-v segment.
func optimalPoint(q quadric, u, v [3]float64) (p [3]float64, ok bool) {
	a := [3][3]float64{
		{q.xx, q.xy, q.xz},
		{q.xy, q.yy, q.yz},
		{q.xz, q.yz, q.zz},
	}
	b := [3]float64{-q.xw, -q.yw, -q.zw}
	p, solved := solve3(a, b)
	if !solved {
		return p, false
	}
	mid := [3]float64{(u[0] + v[0]) / 2, (u[1] + v[1]) / 2, (u[2] + v[2]) / 2}
	halfLen := norm(sub(mid, u))
	if halfLen > 0 && norm(sub(p, mid)) > 4*halfLen {
		return p, false
	}
	return p, true
}

func minErrorPoint(q quadric, u, v [3]float64) [3]float64 {
	mid := [3]float64{(u[0] + v[0]) / 2, (u[1] + v[1]) / 2, (u[2] + v[2]) / 2}
	best := u
	bestErr := q.errorAt(u)
	if e := q.errorAt(v); e < bestErr {
		best, bestErr = v, e
	}
	if e := q.errorAt(mid); e < bestErr {
		best = mid
	}
	return best
}

// solve3 solves a symmetric 3x3 linear system using Cramer's rule. ok is
// false when the determinant is too small relative to the matrix scale.
func solve3(a [3][3]float64, b [3]float64) (x [3]float64, ok bool) {
	det := a[0][0]*(a[1][1]*a[2][2]-a[1][2]*a[2][1]) -
		a[0][1]*(a[1][0]*a[2][2]-a[1][2]*a[2][0]) +
		a[0][2]*(a[1][0]*a[2][1]-a[1][1]*a[2][0])

	scale := 1.0
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if s := math.Abs(a[i][j]); s > scale {
				scale = s
			}
		}
	}
	if math.Abs(det) < 1e-10*scale*scale*scale {
		return x, false
	}

	replace := func(col int) [3][3]float64 {
		m := a
		for i := 0; i < 3; i++ {
			m[i][col] = b[i]
		}
		return m
	}
	det3 := func(m [3][3]float64) float64 {
		return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
			m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
			m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	}
	x[0] = det3(replace(0)) / det
	x[1] = det3(replace(1)) / det
	x[2] = det3(replace(2)) / det
	return x, true
}
