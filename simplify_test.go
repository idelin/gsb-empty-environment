package trajsimp

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// gen builds deterministic synthetic GPS trajectories at 1 Hz with
// realistic GPS jitter.
type gen struct {
	pts     []Point
	x, y    float64 // meters, local frame
	speed   float64 // m/s
	heading float64 // radians, 0 = east
	t       time.Time
	rnd     *rand.Rand
	turns   []time.Time
	stops   [][2]time.Time
}

func newGen() *gen {
	return &gen{
		t:   time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
		rnd: rand.New(rand.NewSource(42)),
	}
}

func (g *gen) emit() {
	jx := g.x + g.rnd.NormFloat64()*0.4
	jy := g.y + g.rnd.NormFloat64()*0.4
	const lat0, lon0 = 31.2304, 121.4737
	lat := lat0 + jy/earthRadiusM*180/math.Pi
	lon := lon0 + jx/(earthRadiusM*math.Cos(lat0*math.Pi/180))*180/math.Pi
	g.pts = append(g.pts, Point{Lat: lat, Lon: lon, Time: g.t})
	g.t = g.t.Add(time.Second)
}

func (g *gen) drive(seconds int) {
	for i := 0; i < seconds; i++ {
		g.x += g.speed * math.Cos(g.heading)
		g.y += g.speed * math.Sin(g.heading)
		g.emit()
	}
}

func (g *gen) stop(seconds int) {
	start := g.t
	for i := 0; i < seconds; i++ {
		g.emit()
	}
	g.stops = append(g.stops, [2]time.Time{start, g.t})
}

func (g *gen) turn(deg float64, seconds int) {
	g.turns = append(g.turns, g.t)
	step := deg * math.Pi / 180 / float64(seconds)
	for i := 0; i < seconds; i++ {
		g.heading += step
		g.x += g.speed * math.Cos(g.heading)
		g.y += g.speed * math.Sin(g.heading)
		g.emit()
	}
}

// acceptanceTrajectory builds the 10k-point acceptance trajectory:
// long straight cruise, two dwell stops and three 90-degree turns.
func acceptanceTrajectory() *gen {
	g := newGen()
	g.emit()
	g.speed, g.heading = 10, 0
	g.drive(3500)
	g.speed = 0
	g.stop(600)
	g.speed = 10
	g.turn(90, 10)
	g.speed = 8
	g.drive(1500)
	g.speed = 10
	g.turn(-90, 8)
	g.speed = 12
	g.drive(2000)
	g.speed = 0
	g.stop(800)
	g.speed = 10
	g.turn(-90, 8)
	g.drive(1573)
	return g
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// countStops counts dwell events in a (simplified) trajectory: consecutive
// kept samples spanning at least minDur with less than 5 m displacement.
func countStops(pts []Point, minDur time.Duration) int {
	n := 0
	for i := 0; i+1 < len(pts); i++ {
		if pts[i+1].Time.Sub(pts[i].Time) >= minDur && Haversine(pts[i], pts[i+1]) < 5 {
			n++
		}
	}
	return n
}

func TestStraightLineReduction(t *testing.T) {
	g := newGen()
	g.emit()
	g.speed = 12
	g.drive(2000)
	out := Default().Simplify(g.pts)
	if len(out) != 2 {
		t.Fatalf("straight line: want 2 points, got %d", len(out))
	}
}

func TestTurnPreserved(t *testing.T) {
	g := newGen()
	g.emit()
	g.speed = 10
	g.drive(200)
	g.turn(90, 4)
	g.drive(200)
	out := Default().Simplify(g.pts)
	if len(out) < 3 {
		t.Fatalf("turn lost: only %d points kept", len(out))
	}
	// A kept point must sit within 5 s of the turn apex.
	apex := g.turns[0].Add(2 * time.Second)
	ok := false
	for _, p := range out {
		if absDur(p.Time.Sub(apex)) <= 5*time.Second {
			ok = true
		}
	}
	if !ok {
		t.Fatal("no kept point near the turn apex")
	}
}

func TestStopPreserved(t *testing.T) {
	g := newGen()
	g.emit()
	g.speed = 10
	g.drive(200)
	g.speed = 0
	g.stop(300)
	g.speed = 10
	g.drive(200)
	out := Default().Simplify(g.pts)
	if got := countStops(out, 250*time.Second); got != 1 {
		t.Fatalf("want 1 stop event, got %d", got)
	}
	if len(out) > 30 {
		t.Fatalf("stop episode not collapsed: %d points kept", len(out))
	}
}

func TestSpeedChangePreserved(t *testing.T) {
	g := newGen()
	g.emit()
	g.speed = 10
	g.drive(500)
	change := g.t
	g.speed = 20
	g.drive(500)
	out := Default().Simplify(g.pts)
	if len(out) <= 2 {
		t.Fatal("speed change smoothed away on a straight line")
	}
	ok := false
	for _, p := range out {
		if absDur(p.Time.Sub(change)) <= 3*time.Second {
			ok = true
		}
	}
	if !ok {
		t.Fatal("no kept point near the speed change")
	}
}

func TestAcceptance10k(t *testing.T) {
	g := acceptanceTrajectory()
	if len(g.pts) != 10000 {
		t.Fatalf("generator produced %d points, want 10000", len(g.pts))
	}
	s := Default() // 5 m spatial, 10% speed
	start := time.Now()
	out := s.Simplify(g.pts)
	elapsed := time.Since(start)

	removed := 1 - float64(len(out))/float64(len(g.pts))
	if removed < 0.80 {
		t.Fatalf("redundancy removal %.1f%% < 80%%", removed*100)
	}
	maxSED, maxSpeedErr := s.Validate(g.pts, out)
	if maxSED > s.SpatialTol+1e-9 {
		t.Fatalf("max synchronized deviation %.3f m exceeds %.1f m", maxSED, s.SpatialTol)
	}
	if maxSpeedErr > s.SpeedTol+1e-9 {
		t.Fatalf("max speed error %.3f exceeds %.2f", maxSpeedErr, s.SpeedTol)
	}
	if got := countStops(out, 400*time.Second); got != len(g.stops) {
		t.Fatalf("stop events: want %d, got %d", len(g.stops), got)
	}
	for i, tt := range g.turns {
		ok := false
		for _, p := range out {
			if absDur(p.Time.Sub(tt)) <= 10*time.Second {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("turn %d at %s not preserved", i, tt.Format("15:04:05"))
		}
	}
	t.Logf("10k points -> %d kept (%.1f%% removed) in %v", len(out), removed*100, elapsed)
	t.Logf("max SED %.3f m, max speed error %.2f%%, stops %d/%d, turns %d/%d",
		maxSED, maxSpeedErr*100, countStops(out, 400*time.Second), len(g.stops), len(g.turns), len(g.turns))
}

func TestEdgeCases(t *testing.T) {
	if got := Default().Simplify(nil); len(got) != 0 {
		t.Fatalf("nil input: want 0 points, got %d", len(got))
	}
	now := time.Now()
	one := []Point{{Lat: 31, Lon: 121, Time: now}}
	if got := Default().Simplify(one); len(got) != 1 {
		t.Fatalf("single point: want 1, got %d", len(got))
	}
	two := []Point{{Lat: 31, Lon: 121, Time: now}, {Lat: 31.001, Lon: 121.001, Time: now.Add(time.Second)}}
	if got := Default().Simplify(two); len(got) != 2 {
		t.Fatalf("two points: want 2, got %d", len(got))
	}
	// Duplicate timestamps must not panic or loop forever.
	dup := make([]Point, 100)
	for i := range dup {
		dup[i] = Point{Lat: 31 + float64(i)*1e-6, Lon: 121, Time: now}
	}
	if got := Default().Simplify(dup); len(got) < 2 {
		t.Fatalf("duplicate timestamps: want >= 2 points, got %d", len(got))
	}
}

func ExampleSimplify() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []Point
	for i := 0; i <= 10; i++ {
		pts = append(pts, Point{
			Lat:  31.0,
			Lon:  121.0 + float64(i)*0.001,
			Time: base.Add(time.Duration(i) * 10 * time.Second),
		})
	}
	out := Simplify(pts)
	fmt.Println(len(out))
	// Output: 2
}
