// Package trajsimp implements a timestamp-aware Ramer-Douglas-Peucker
// simplifier for geospatial trajectories.
//
// The classic RDP algorithm recursively simplifies a polyline by
// perpendicular spatial distance. This implementation adds two temporal
// criteria on top of it:
//
//   - temporal uniformity: the synchronized Euclidean distance (SED)
//     between a sample and the time-interpolated position on the
//     simplified segment must stay within the spatial tolerance;
//   - speed smoothness: the relative error between the smoothed speed
//     of a dropped sample and the implied speed of the simplified
//     segment must stay within the speed tolerance.
//
// Stop episodes and sharp turns are detected up front and anchored, so
// waiting periods and cornering events are never distorted.
package trajsimp

import (
	"math"
	"sort"
	"time"
)

// Point is a single timestamped GPS sample.
type Point struct {
	Lat  float64
	Lon  float64
	Time time.Time
}

const earthRadiusM = 6371000.0

// Haversine returns the great-circle distance between two points in meters.
func Haversine(a, b Point) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// Simplifier holds the tolerance configuration for trajectory
// simplification. The zero value is not useful; use Default or set the
// fields explicitly.
type Simplifier struct {
	SpatialTol float64       // max spatial deviation (perpendicular & synchronized), meters
	SpeedTol   float64       // max relative speed error, e.g. 0.10 for 10%; <= 0 disables the check
	StopSpeed  float64       // speed (m/s) below which a sample counts as stopped
	TurnAngle  float64       // heading change (degrees) that anchors a turn
	Window     int           // smoothing half-window, in samples, for speed/heading
	MinStop    time.Duration // minimum stop-episode duration worth anchoring
}

// Default returns a Simplifier matching the acceptance spec:
// 5 m spatial tolerance and 10% speed tolerance.
func Default() Simplifier {
	return Simplifier{
		SpatialTol: 5,
		SpeedTol:   0.10,
		StopSpeed:  0.5,
		TurnAngle:  25,
		Window:     3,
		MinStop:    10 * time.Second,
	}
}

// Simplify reduces pts using the default configuration.
func Simplify(pts []Point) []Point {
	return Default().Simplify(pts)
}

// Simplify reduces the trajectory while honouring the configured spatial
// and speed tolerances. The input must be ordered by time. The returned
// slice is a subset of the input points (same values, new slice).
func (s Simplifier) Simplify(pts []Point) []Point {
	n := len(pts)
	if n <= 2 {
		out := make([]Point, n)
		copy(out, pts)
		return out
	}
	w := newWorker(s, pts)
	keep := make([]bool, n)
	anchors := w.anchors()
	anchors = append(anchors, 0, n-1)
	sort.Ints(anchors)
	prev := -1
	for _, a := range anchors {
		if a == prev {
			continue
		}
		prev = a
		keep[a] = true
	}
	for k := 0; k+1 < len(anchors); k++ {
		lo, hi := anchors[k], anchors[k+1]
		if hi-lo >= 2 {
			w.simplifyRange(keep, lo, hi)
		}
	}
	out := make([]Point, 0, n/4)
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// Validate measures how faithfully simp represents orig. It returns the
// maximum synchronized Euclidean distance (meters) and the maximum
// relative speed error over all removed samples; samples kept in simp
// are exact by construction and are skipped.
func (s Simplifier) Validate(orig, simp []Point) (maxSED, maxSpeedErr float64) {
	if len(orig) == 0 || len(simp) < 2 {
		return 0, 0
	}
	w := newWorker(s, orig)
	j := 0
	for i, p := range orig {
		for j < len(simp)-2 && !p.Time.Before(simp[j+1].Time) {
			j++
		}
		a, b := simp[j], simp[j+1]
		if p.Time.Equal(a.Time) || p.Time.Equal(b.Time) {
			continue
		}
		dt := b.Time.Sub(a.Time).Seconds()
		bXY := toXY(b, a)
		pXY := toXY(p, a)
		sed := perpDist(pXY, xy{}, bXY)
		if dt > 0 {
			frac := p.Time.Sub(a.Time).Seconds() / dt
			sed = math.Hypot(pXY.x-frac*bXY.x, pXY.y-frac*bXY.y)
		}
		if sed > maxSED {
			maxSED = sed
		}
		vSeg := 0.0
		if dt > 0 {
			vSeg = math.Hypot(bXY.x, bXY.y) / dt
		}
		v := w.speed[i]
		if !(vSeg < s.StopSpeed && v < s.StopSpeed) {
			if e := math.Abs(v-vSeg) / math.Max(vSeg, 0.1); e > maxSpeedErr {
				maxSpeedErr = e
			}
		}
	}
	return maxSED, maxSpeedErr
}

// xy is a position in a local equirectangular projection, in meters.
type xy struct{ x, y float64 }

func toXY(p, ref Point) xy {
	latR := ref.Lat * math.Pi / 180
	return xy{
		x: (p.Lon - ref.Lon) * (math.Pi / 180) * earthRadiusM * math.Cos(latR),
		y: (p.Lat - ref.Lat) * (math.Pi / 180) * earthRadiusM,
	}
}

// perpDist returns the perpendicular distance from p to the line a-b.
func perpDist(p, a, b xy) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(p.x-a.x, p.y-a.y)
	}
	return math.Abs(dy*p.x-dx*p.y+b.x*a.y-b.y*a.x) / math.Sqrt(l2)
}

func angleDiff(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > math.Pi {
		d = 2*math.Pi - d
	}
	return d
}

// worker carries the trajectory and its derived signals through the
// simplification pipeline.
type worker struct {
	cfg     Simplifier
	pts     []Point
	speed   []float64 // smoothed speed per sample, m/s
	heading []float64 // smoothed course per sample, radians
}

func newWorker(cfg Simplifier, pts []Point) *worker {
	if cfg.Window < 1 {
		cfg.Window = 1
	}
	n := len(pts)
	w := &worker{
		cfg:     cfg,
		pts:     pts,
		speed:   make([]float64, n),
		heading: make([]float64, n),
	}
	win := cfg.Window
	for i := 0; i < n; i++ {
		lo, hi := i-win, i+win
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		if dt := pts[hi].Time.Sub(pts[lo].Time).Seconds(); dt > 0 {
			w.speed[i] = Haversine(pts[lo], pts[hi]) / dt
		}
		v := toXY(pts[hi], pts[lo])
		if v.x != 0 || v.y != 0 {
			w.heading[i] = math.Atan2(v.y, v.x)
		} else if i > 0 {
			w.heading[i] = w.heading[i-1]
		}
	}
	return w
}

// anchors returns the indices that must survive simplification:
// boundaries of stop episodes and sharp turns.
func (w *worker) anchors() []int {
	n := len(w.pts)
	var out []int
	// Stop episodes: boundaries of every sufficiently long stretch with
	// smoothed speed below StopSpeed.
	for i := 1; i < n-1; {
		if w.speed[i] >= w.cfg.StopSpeed {
			i++
			continue
		}
		start := i
		for i < n-1 && w.speed[i] < w.cfg.StopSpeed {
			i++
		}
		end := i - 1
		if w.pts[end].Time.Sub(w.pts[start].Time) >= w.cfg.MinStop {
			out = append(out, start, end)
		}
	}
	// Turns: significant heading change while moving.
	turnRad := w.cfg.TurnAngle * math.Pi / 180
	for i := 1; i < n-1; i++ {
		if w.speed[i] < w.cfg.StopSpeed {
			continue
		}
		if angleDiff(w.heading[i-1], w.heading[i+1]) > turnRad {
			out = append(out, i)
		}
	}
	return out
}

// simplifyRange runs iterative RDP with the combined spatial/temporal
// criterion over pts[lo:hi+1], marking survivors in keep.
func (w *worker) simplifyRange(keep []bool, lo, hi int) {
	type seg struct{ lo, hi int }
	stack := []seg{{lo, hi}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.hi-cur.lo < 2 {
			continue
		}
		idx, score := w.worstPoint(cur.lo, cur.hi)
		if score > 1 {
			keep[idx] = true
			stack = append(stack, seg{cur.lo, idx}, seg{idx, cur.hi})
		}
	}
}

// worstPoint finds the sample in (lo, hi) with the largest normalized
// deviation from the segment lo-hi. A score above 1 means at least one
// tolerance (spatial or speed) is violated.
func (w *worker) worstPoint(lo, hi int) (int, float64) {
	a, b := w.pts[lo], w.pts[hi]
	dt := b.Time.Sub(a.Time).Seconds()
	bXY := toXY(b, a)
	vSeg := 0.0
	if dt > 0 {
		vSeg = math.Hypot(bXY.x, bXY.y) / dt
	}
	spatialTol := math.Max(w.cfg.SpatialTol, 1e-9)
	bestIdx, bestScore := -1, -1.0
	for k := lo + 1; k < hi; k++ {
		pXY := toXY(w.pts[k], a)
		perp := perpDist(pXY, xy{}, bXY)
		sed := perp
		if dt > 0 {
			frac := w.pts[k].Time.Sub(a.Time).Seconds() / dt
			sed = math.Hypot(pXY.x-frac*bXY.x, pXY.y-frac*bXY.y)
		}
		score := math.Max(perp, sed) / spatialTol
		if w.cfg.SpeedTol > 0 {
			v := w.speed[k]
			spErr := 0.0
			// Both stationary: speed error is meaningless, skip it so
			// stop interiors collapse to their boundary anchors.
			if !(vSeg < w.cfg.StopSpeed && v < w.cfg.StopSpeed) {
				spErr = math.Abs(v-vSeg) / math.Max(vSeg, 0.1)
			}
			if s := spErr / w.cfg.SpeedTol; s > score {
				score = s
			}
		}
		if score > bestScore {
			bestScore, bestIdx = score, k
		}
	}
	return bestIdx, bestScore
}
