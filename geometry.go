package taskpkg

import "math"

// slabBox intersects the ray origin+t*dir with an axis-aligned box using the
// slab method. It returns the entry and exit parameters; hit is false for a
// miss or a degenerate direction contained in a slab plane.
func slabBox(origin, dir Vec3, xmin, xmax, ymin, ymax, zmin, zmax float64) (t0, t1 float64, hit bool) {
	t0 = math.Inf(-1)
	t1 = math.Inf(1)

	update := func(o, d, lo, hi float64) bool {
		if d == 0 {
			if o < lo || o > hi {
				return false
			}
			return true
		}
		ta := (lo - o) / d
		tb := (hi - o) / d
		if ta > tb {
			ta, tb = tb, ta
		}
		t0 = max(t0, ta)
		t1 = min(t1, tb)
		return t0 <= t1
	}

	if !update(origin[0], dir[0], xmin, xmax) {
		return 0, 0, false
	}
	if !update(origin[1], dir[1], ymin, ymax) {
		return 0, 0, false
	}
	if !update(origin[2], dir[2], zmin, zmax) {
		return 0, 0, false
	}
	return t0, t1, true
}

// nextBoundary returns the ray parameter of the nearest cell boundary ahead
// of t. Non-uniform node coordinates are honored exactly: each axis computes
// the parameter at its next node rather than stepping by a fixed size.
func nextBoundary(t float64, dir, origin Vec3, g *Grid, i, j, k int) float64 {
	tb := math.Inf(1)
	if dir[0] > 0 {
		tb = min(tb, (g.x[i+1]-origin[0])/dir[0])
	} else if dir[0] < 0 {
		tb = min(tb, (g.x[i]-origin[0])/dir[0])
	}
	if dir[1] > 0 {
		tb = min(tb, (g.y[j+1]-origin[1])/dir[1])
	} else if dir[1] < 0 {
		tb = min(tb, (g.y[j]-origin[1])/dir[1])
	}
	if dir[2] > 0 {
		tb = min(tb, (g.z[k+1]-origin[2])/dir[2])
	} else if dir[2] < 0 {
		tb = min(tb, (g.z[k]-origin[2])/dir[2])
	}
	// Never report a boundary behind the current position, which can otherwise
	// happen through rounding at repeated crossings.
	if tb < t {
		tb = t
	}
	return tb
}

// advanceCell moves the cell index across the boundary the ray crosses.
func advanceCell(i, j, k int, dir Vec3, g *Grid) (int, int, int) {
	if dir[0] > 0 && i < g.nx-2 {
		i++
	} else if dir[0] < 0 && i > 0 {
		i--
	}
	if dir[1] > 0 && j < g.ny-2 {
		j++
	} else if dir[1] < 0 && j > 0 {
		j--
	}
	if dir[2] > 0 && k < g.nz-2 {
		k++
	} else if dir[2] < 0 && k > 0 {
		k--
	}
	return i, j, k
}

// remainder reports a best-effort segment plus conservative error bounds for
// a path of length s that could not be sampled because the step budget was
// exhausted. The un-sampled tail is treated as vacuum in the best estimate;
// bounds use the largest node coefficients in the grid: transmittance can
// deviate by at most 1-exp(-sigmaMax*s), emitted radiance per channel by
// |emMax|*s.
func (g *Grid) remainder(s float64) (Segment, RGB, float64) {
	if s <= 0 {
		return Segment{T: 1}, RGB{}, 0
	}
	transBound := -math.Expm1(-g.maxSigma * s)
	if transBound < 0 {
		transBound = 0
	}
	color := RGB{
		g.maxEm[0] * s,
		g.maxEm[1] * s,
		g.maxEm[2] * s,
	}
	return Segment{T: 1}, color, transBound
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// isFinite reports whether v is neither NaN nor infinity.
func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// vecLen returns the Euclidean length of v.
func vecLen(v Vec3) float64 {
	return math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
}
