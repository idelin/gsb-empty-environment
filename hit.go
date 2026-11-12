package taskpkg

import (
	"math"
	"sort"
)

// Hit describes a world-space ray/triangle hit.
//
// T is measured in the caller's world Ray.Direction units. Point is the world
// hit point reconstructed from the local hit and projected back onto the world
// ray. Normal is the unit world normal obtained from the local face normal by
// the inverse-transpose transform. Barycentric contains the weights for
// Triangle vertices A, B, and C, in that order.
type Hit struct {
	InstanceID  uint64
	TriangleID  uint32
	T           float64
	Point       Vec3
	Normal      Vec3
	Barycentric [3]float64
	FrontFace   bool
}

func hitLessExact(a, b Hit) bool {
	if a.T != b.T {
		return a.T < b.T
	}
	if a.InstanceID != b.InstanceID {
		return a.InstanceID < b.InstanceID
	}
	return a.TriangleID < b.TriangleID
}

// hitBetter applies a relative tie tolerance before comparing stable IDs. The
// tolerance exists because two analytically equal world distances can differ
// by a few ulps after independent affine transforms.
func hitBetter(candidate, best Hit, tolerance func(float64) float64) bool {
	scale := math.Max(1, math.Max(math.Abs(candidate.T), math.Abs(best.T)))
	epsilon := tolerance(scale)
	if math.Abs(candidate.T-best.T) <= epsilon {
		if candidate.InstanceID != best.InstanceID {
			return candidate.InstanceID < best.InstanceID
		}
		return candidate.TriangleID < best.TriangleID
	}
	return candidate.T < best.T
}

func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		return hitLessExact(hits[i], hits[j])
	})
}
