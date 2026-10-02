package trajsimp

import (
	"math"
	"time"
)

const (
	// speedWindow is the number of samples on each side used to estimate
	// the local speed, smoothing out GPS jitter.
	speedWindow = 3
	// minSpeed floors the denominator of the relative speed error so that
	// near-stationary segments do not blow up the ratio.
	minSpeed = 0.5 // m/s
)

// Options configures the simplifier.
type Options struct {
	// Epsilon is the spatial tolerance in meters. It bounds both the
	// perpendicular distance and the time-synchronized distance (SED).
	// Defaults to 5.
	Epsilon float64
	// SpeedTolerance is the maximum allowed relative speed error between
	// a kept segment's implied speed and the local measured speed,
	// e.g. 0.10 for 10%. Defaults to 0.10.
	SpeedTolerance float64
	// StayRadius is the radius in meters used for stay-point detection.
	// Defaults to 10.
	StayRadius float64
	// MinStay is the minimum duration of a stay interval. Stay interval
	// endpoints are always kept. Defaults to 1 minute.
	MinStay time.Duration
}

func (o Options) withDefaults() Options {
	if o.Epsilon <= 0 {
		o.Epsilon = 5
	}
	if o.SpeedTolerance <= 0 {
		o.SpeedTolerance = 0.10
	}
	if o.StayRadius <= 0 {
		o.StayRadius = 10
	}
	if o.MinStay <= 0 {
		o.MinStay = time.Minute
	}
	return o
}

// Simplify returns the simplified trajectory.
func Simplify(pts []Point, opt Options) []Point {
	idx := SimplifyIndices(pts, opt)
	out := make([]Point, len(idx))
	for i, k := range idx {
		out[i] = pts[k]
	}
	return out
}

// SimplifyIndices returns the ascending indices of the kept samples.
//
// It runs the classic Douglas-Peucker recursion, but a segment is split
// not only on perpendicular distance: the split cost of each interior
// sample is the maximum of
//
//	perpendicular distance / Epsilon            (spatial shape)
//	time-synchronized distance / Epsilon        (temporal uniformity)
//	relative local-speed error / SpeedTolerance (speed smoothness)
//
// and the segment is split at the worst sample whenever the cost exceeds 1.
// Endpoints of detected stay intervals are forced to be kept, so stop-and-wait
// events are never smoothed away.
func SimplifyIndices(pts []Point, opt Options) []int {
	opt = opt.withDefaults()
	n := len(pts)
	if n == 0 {
		return nil
	}
	if n <= 2 {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}

	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	for _, iv := range DetectStays(pts, opt.StayRadius, opt.MinStay) {
		keep[iv[0]] = true
		keep[iv[1]] = true
	}

	type segment struct{ i, j int }
	stack := []segment{{0, n - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.j-s.i < 2 {
			continue
		}
		if k, cost := worstPoint(pts, s.i, s.j, opt); cost > 1 {
			keep[k] = true
			stack = append(stack, segment{s.i, k}, segment{k, s.j})
		}
	}

	var out []int
	for i, k := range keep {
		if k {
			out = append(out, i)
		}
	}
	return out
}

// worstPoint returns the interior sample of pts[i..j] with the highest
// normalized split cost, together with that cost.
func worstPoint(pts []Point, i, j int, opt Options) (int, float64) {
	best, bestCost := -1, 1.0
	for m := i + 1; m < j; m++ {
		perp, sync, speedDev := deviation(pts, i, j, m)
		cost := perp / opt.Epsilon
		if c := sync / opt.Epsilon; c > cost {
			cost = c
		}
		if c := speedDev / opt.SpeedTolerance; c > cost {
			cost = c
		}
		if cost > bestCost {
			best, bestCost = m, cost
		}
	}
	return best, bestCost
}

// deviation computes, for sample m relative to the candidate segment
// pts[i]..pts[j]: the perpendicular distance to the segment (meters), the
// time-synchronized (SED) distance (meters), and the relative local-speed
// error against the segment's implied speed.
func deviation(pts []Point, i, j, m int) (perp, sync, speedDev float64) {
	a, b := pts[i], pts[j]
	bx, by := offsetMeters(b, a)
	px, py := offsetMeters(pts[m], a)

	segLen2 := bx*bx + by*by
	t := 0.0
	if segLen2 > 0 {
		t = clamp((px*bx + py*by) / segLen2)
	}
	perp = math.Hypot(px-t*bx, py-t*by)

	dt := b.T.Sub(a.T).Seconds()
	if dt <= 0 {
		return perp, 0, 0
	}
	rt := clamp(pts[m].T.Sub(a.T).Seconds() / dt)
	sync = math.Hypot(px-rt*bx, py-rt*by)

	segSpeed := math.Sqrt(segLen2) / dt
	lo, hi := m-speedWindow, m+speedWindow
	if lo < i {
		lo = i
	}
	if hi > j {
		hi = j
	}
	if dtw := pts[hi].T.Sub(pts[lo].T).Seconds(); dtw > 0 {
		vw := DistanceMeters(pts[lo], pts[hi]) / dtw
		if segSpeed >= minSpeed || vw >= minSpeed {
			// A relative error is meaningless when both speeds are
			// near zero (a stop is a stop), so only compare otherwise.
			speedDev = math.Abs(vw-segSpeed) / math.Max(segSpeed, minSpeed)
		}
	}
	return perp, sync, speedDev
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
