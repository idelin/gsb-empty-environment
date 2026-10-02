package trajsimp

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// testTrace is a synthetic 10k-sample GPS trace with known stay and turn events.
type testTrace struct {
	pts   []Point
	stays [][2]int // intended stay index ranges
	turns []int    // turn apex indices
}

// buildTestTrace generates ~10k samples at 1 Hz:
// straight legs at constant speed (with GPS noise), two stops, and three turns.
func buildTestTrace() testTrace {
	rnd := rand.New(rand.NewSource(7))
	lon0, lat0 := 116.0, 39.0
	t0 := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

	tr := testTrace{}
	x, y := 0.0, 0.0
	sec := 0
	add := func() {
		lon := lon0 + x/metersPerDegLon(lat0)
		lat := lat0 + y/metersPerDegLat
		tr.pts = append(tr.pts, Point{Lon: lon, Lat: lat, T: t0.Add(time.Duration(sec) * time.Second)})
		sec++
	}
	noise := func(amp float64) (float64, float64) {
		return (rnd.Float64()*2 - 1) * amp, (rnd.Float64()*2 - 1) * amp
	}
	leg := func(headingDeg, speed float64, seconds int) {
		dx := math.Cos(headingDeg*math.Pi/180) * speed
		dy := math.Sin(headingDeg*math.Pi/180) * speed
		for s := 0; s < seconds; s++ {
			nx, ny := noise(0.5) // ±0.5 m GPS noise
			x += dx
			y += dy
			sx, sy := x+nx, y+ny
			lon := lon0 + sx/metersPerDegLon(lat0)
			lat := lat0 + sy/metersPerDegLat
			tr.pts = append(tr.pts, Point{Lon: lon, Lat: lat, T: t0.Add(time.Duration(sec) * time.Second)})
			sec++
		}
	}
	stay := func(seconds int) {
		start := len(tr.pts)
		ax, ay := x, y
		for s := 0; s < seconds; s++ {
			nx, ny := noise(0.3) // ±0.3 m jitter while stopped
			x, y = ax+nx, ay+ny
			add()
		}
		x, y = ax, ay
		tr.stays = append(tr.stays, [2]int{start, len(tr.pts) - 1})
	}
	turn := func() { tr.turns = append(tr.turns, len(tr.pts)-1) }

	add()            // origin
	leg(0, 10, 3000) // 30 km east at 10 m/s
	turn()
	stay(600)         // 10 min stop
	leg(90, 12, 2000) // 24 km north at 12 m/s
	turn()
	stay(400)        // 6m40s stop
	leg(45, 8, 2000) // northeast at 8 m/s
	turn()
	leg(-45, 11, 2000) // sharp 90° turn, southeast at 11 m/s
	return tr
}

func keptSet(idx []int) map[int]bool {
	s := make(map[int]bool, len(idx))
	for _, i := range idx {
		s[i] = true
	}
	return s
}

func TestSimplifyLargeTrajectory(t *testing.T) {
	tr := buildTestTrace()
	if got := len(tr.pts); got != 10001 {
		t.Fatalf("generated %d samples, want 10001", got)
	}
	opt := Options{Epsilon: 5, SpeedTolerance: 0.10}

	start := time.Now()
	idx := SimplifyIndices(tr.pts, opt)
	elapsed := time.Since(start)

	removed := 1 - float64(len(idx))/float64(len(tr.pts))
	t.Logf("kept %d of %d samples (%.2f%% removed) in %v",
		len(idx), len(tr.pts), removed*100, elapsed)
	if removed < 0.80 {
		t.Fatalf("only %.2f%% of redundant samples removed, want >= 80%%", removed*100)
	}

	// All stay events must be preserved: detected stay endpoints are kept.
	stays := DetectStays(tr.pts, 10, time.Minute)
	if len(stays) != 2 {
		t.Fatalf("detected %d stays, want 2", len(stays))
	}
	kept := keptSet(idx)
	for _, iv := range stays {
		if !kept[iv[0]] || !kept[iv[1]] {
			t.Errorf("stay interval [%d, %d] not preserved", iv[0], iv[1])
		}
	}

	// All turn apexes must lie within Epsilon of the simplified polyline.
	for _, apex := range tr.turns {
		seg := 0
		for seg+1 < len(idx) && idx[seg+1] < apex {
			seg++
		}
		perp, _, _ := deviation(tr.pts, idx[seg], idx[seg+1], apex)
		if perp > opt.Epsilon {
			t.Errorf("turn apex %d deviates %.2f m from simplified trace (tolerance %.0f m)",
				apex, perp, opt.Epsilon)
		}
	}

	// Temporal and speed fidelity of the whole result.
	maxSync, maxSpeedDev := Validate(tr.pts, idx, opt)
	t.Logf("max time-sync error %.3f m (tolerance %.0f m), max speed error %.2f%% (tolerance %.0f%%)",
		maxSync, opt.Epsilon, maxSpeedDev*100, opt.SpeedTolerance*100)
	if maxSync > opt.Epsilon+1e-6 {
		t.Errorf("max time-sync error %.3f m exceeds tolerance %.0f m", maxSync, opt.Epsilon)
	}
	if maxSpeedDev > opt.SpeedTolerance+1e-9 {
		t.Errorf("max speed error %.3f%% exceeds tolerance %.0f%%", maxSpeedDev*100, opt.SpeedTolerance*100)
	}
}

func TestDetectStays(t *testing.T) {
	tr := buildTestTrace()
	stays := DetectStays(tr.pts, 10, time.Minute)
	if len(stays) != len(tr.stays) {
		t.Fatalf("detected %d stays, want %d", len(stays), len(tr.stays))
	}
	for k, iv := range stays {
		lo, hi := tr.stays[k][0], tr.stays[k][1]
		if iv[0] < lo-2 || iv[0] > hi+2 || iv[1] < lo-2 || iv[1] > hi+2 {
			t.Errorf("stay %d detected as [%d, %d], want near [%d, %d]", k, iv[0], iv[1], lo, hi)
		}
	}
}

func TestSimplifyEdgeCases(t *testing.T) {
	if got := SimplifyIndices(nil, Options{}); got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
	one := []Point{{Lon: 116, Lat: 39, T: time.Now()}}
	if got := SimplifyIndices(one, Options{}); len(got) != 1 {
		t.Errorf("single point: got %v, want 1 index", got)
	}
	two := append(one, Point{Lon: 116.001, Lat: 39, T: time.Now().Add(time.Second)})
	if got := SimplifyIndices(two, Options{}); len(got) != 2 {
		t.Errorf("two points: got %v, want 2 indices", got)
	}
}

func TestStraightLineCollapses(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	pts := make([]Point, 1000)
	for i := range pts {
		pts[i] = Point{
			Lon: 116 + float64(i)*10/metersPerDegLon(39),
			Lat: 39,
			T:   t0.Add(time.Duration(i) * time.Second),
		}
	}
	idx := SimplifyIndices(pts, Options{Epsilon: 5, SpeedTolerance: 0.10})
	if len(idx) != 2 {
		t.Fatalf("uniform straight line kept %d points, want 2", len(idx))
	}
}

func BenchmarkSimplify(b *testing.B) {
	tr := buildTestTrace()
	opt := Options{Epsilon: 5, SpeedTolerance: 0.10}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimplifyIndices(tr.pts, opt)
	}
}
