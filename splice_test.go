package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

// feasiblePlacement checks a full boundary vector against the head/tail and
// inter-cut minimum-length and crossfade non-overlap constraints.
func feasiblePlacement(bs []int, cuts []Cut, frames, minLength int) bool {
	prevBoundary, prevFade := 0, 0
	for i, b := range bs {
		if !feasibleGap(prevBoundary, prevFade, b, cuts[i].Fade, minLength) {
			return false
		}
		prevBoundary, prevFade = b, cuts[i].Fade
	}
	return feasibleGap(prevBoundary, prevFade, frames, 0, minLength)
}

// bruteForce re-implements the global lexicographic optimum by exhaustive
// enumeration of every boundary combination inside the search windows.
func bruteForce(pcm [][]float64, cuts []Cut, minLength, frames int) ([]int, []stepCost, bool) {
	if len(cuts) == 0 {
		if minLength <= frames {
			return []int{}, []stepCost{}, true
		}
		return nil, nil, false
	}
	ranges := make([][]int, len(cuts))
	for i, cut := range cuts {
		lo := max(1, cut.At-cut.Window)
		hi := min(frames-1, cut.At+cut.Window)
		for b := lo; b <= hi; b++ {
			ranges[i] = append(ranges[i], b)
		}
	}

	var bestBS []int
	var bestCost []stepCost

	var recurse func(i int, bs []int, costs []stepCost)
	recurse = func(i int, bs []int, costs []stepCost) {
		if i == len(cuts) {
			if !feasiblePlacement(bs, cuts, frames, minLength) {
				return
			}
			if bestBS == nil {
				bestBS = append([]int(nil), bs...)
				bestCost = append([]stepCost(nil), costs...)
				return
			}
			for k := range costs {
				if lessCost(costs[k], bestCost[k]) {
					bestBS = append([]int(nil), bs...)
					bestCost = append([]stepCost(nil), costs...)
					return
				}
				if lessCost(bestCost[k], costs[k]) {
					return
				}
			}
		}
		for _, b := range ranges[i] {
			recurse(i+1, append(bs, b), append(costs, stepCost{
				jump: maxJump(pcm, b),
				dist: abs(b - cuts[i].At),
				off:  b - cuts[i].At,
			}))
		}
	}
	recurse(0, nil, nil)

	if bestBS == nil {
		return nil, nil, false
	}
	return bestBS, bestCost, true
}

// randomCuts builds up to 3 distinct, ordered cuts over frames.
func randomCuts(rng *rand.Rand, frames int) []Cut {
	nCuts := rng.Intn(3)
	perm := rng.Perm(frames - 1)
	ats := make([]int, nCuts)
	for i := range ats {
		ats[i] = perm[i] + 1
	}
	for a := 1; a < len(ats); a++ {
		for b := a; b > 0 && ats[b] < ats[b-1]; b-- {
			ats[b], ats[b-1] = ats[b-1], ats[b]
		}
	}
	cuts := make([]Cut, nCuts)
	for i, at := range ats {
		cuts[i] = Cut{
			At:     at,
			Fade:   rng.Intn(5),
			Window: rng.Intn(4),
		}
	}
	return cuts
}

// TestExhaustiveGlobalMatch runs short random cases and checks that the DP
// solver reproduces the globally enumerated optimum, and that every output
// frame obeys its source-sample map.
func TestExhaustiveGlobalMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		frames := 6 + rng.Intn(12)
		channels := 1 + rng.Intn(3)
		pcm := make([][]float64, channels)
		for c := range pcm {
			pcm[c] = make([]float64, frames)
			for f := range pcm[c] {
				pcm[c][f] = rng.Float64()*2 - 1
			}
		}

		cuts := randomCuts(rng, frames)
		minLength := rng.Intn(3)

		res, err := Splice(pcm, cuts, Options{MinSegmentLength: minLength})
		wantBS, _, ok := bruteForce(pcm, cuts, minLength, frames)
		if !ok {
			if err != ErrInfeasible {
				t.Fatalf("trial %d: want ErrInfeasible, got %v", trial, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: unexpected error %v", trial, err)
		}

		if len(res.Boundaries) != len(cuts) {
			t.Fatalf("trial %d: got %d boundaries, want %d", trial, len(res.Boundaries), len(cuts))
		}
		for i := range cuts {
			if got := res.Boundaries[i].Boundary; got != wantBS[i] {
				t.Fatalf("trial %d cut %d: boundary %d, brute force %d", trial, i, got, wantBS[i])
			}
			if res.Boundaries[i].MaxJump != maxJump(pcm, wantBS[i]) {
				t.Fatalf("trial %d cut %d: MaxJump mismatch", trial, i)
			}
		}

		expectedLen := frames
		for _, cut := range cuts {
			expectedLen -= fadeHalf(cut.Fade)
		}
		if len(res.Mapping) != expectedLen || len(res.PCM[0]) != expectedLen {
			t.Fatalf("trial %d: output length %d/%d, want %d", trial, len(res.Mapping), len(res.PCM[0]), expectedLen)
		}

		for f, m := range res.Mapping {
			if m.OutputFrame != f {
				t.Fatalf("trial %d: mapping frame %d labeled %d", trial, f, m.OutputFrame)
			}
			for c := 0; c < channels; c++ {
				var rebuilt float64
				for _, ref := range m.Sources[c] {
					if ref.Channel != c {
						t.Fatalf("trial %d: wrong channel ref", trial)
					}
					rebuilt += ref.Gain * pcm[c][ref.Frame]
				}
				if math.Abs(rebuilt-res.PCM[c][f]) > 1e-12 {
					t.Fatalf("trial %d frame %d ch %d: mapping reconstructs %v, output %v",
						trial, f, c, rebuilt, res.PCM[c][f])
				}
			}
		}
	}
}

// TestAdjacentFadeConflict builds two cuts whose long crossfade regions must
// not overlap, and verifies a feasible pair is chosen while impossible
// minimum-length requests are rejected.
func TestAdjacentFadeConflict(t *testing.T) {
	pcm := make([][]float64, 1)
	pcm[0] = make([]float64, 14)
	cuts := []Cut{{At: 6, Fade: 6, Window: 2}, {At: 8, Fade: 6, Window: 2}}

	res, err := Splice(pcm, cuts, Options{MinSegmentLength: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b0, b1 := res.Boundaries[0].Boundary, res.Boundaries[1].Boundary
	if b1-b0 < fadeHalf(cuts[0].Fade)+fadeHalf(cuts[1].Fade) {
		t.Fatalf("crossfades overlap: boundaries %d, %d", b0, b1)
	}
	if b0 < 1 || b1 > 13 {
		t.Fatalf("boundaries out of range: %d, %d", b0, b1)
	}

	if _, err := Splice(pcm, cuts, Options{MinSegmentLength: 4}); err != ErrInfeasible {
		t.Fatalf("want ErrInfeasible, got %v", err)
	}

	tight := []Cut{{At: 2, Fade: 4, Window: 1}}
	if _, err := Splice(pcm, tight, Options{MinSegmentLength: 2}); err != ErrInfeasible {
		t.Fatalf("want ErrInfeasible for head, got %v", err)
	}
}

// TestStereoSync verifies that one common boundary is selected for every
// channel and that the same input frames are mapped across channels.
func TestStereoSync(t *testing.T) {
	const frames = 12
	pcm := make([][]float64, 2)
	// Both channels step into 0.75 at frame 4 and back to 0 at frame 6. The
	// steps therefore sit at boundaries 4 and 6; boundary 5 is the only
	// step-free point in the search window.
	for c := range pcm {
		pcm[c] = make([]float64, frames)
		for f := 4; f < 6; f++ {
			pcm[c][f] = 0.75
		}
	}
	// An extra, larger step on the right channel proves the jump metric is
	// taken as the maximum across channels.
	pcm[1][9] = -1.5

	// Nominal point 4 lies directly on a step; moving one frame to boundary 5
	// removes every channel jump.
	cuts := []Cut{{At: 4, Fade: 4, Window: 1}}
	res, err := Splice(pcm, cuts, Options{MinSegmentLength: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Boundaries[0].Boundary; got != 5 {
		t.Fatalf("want shared jump-free boundary 5, got %d", got)
	}

	for f, m := range res.Mapping {
		left := m.Sources[0]
		right := m.Sources[1]
		if len(left) != len(right) {
			t.Fatalf("frame %d: channel ref count diverges", f)
		}
		for k := range left {
			if left[k].Frame != right[k].Frame || left[k].Gain != right[k].Gain {
				t.Fatalf("frame %d: channels desynchronised: %+v vs %+v", f, left, right)
			}
		}
	}
}

// TestNoFadeLength checks hard cuts: no output frames are lost, gains are
// exactly one, and the splice preserves the input samples in order.
func TestNoFadeLength(t *testing.T) {
	pcm := [][]float64{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	}
	cuts := []Cut{{At: 3, Fade: 0, Window: 2}, {At: 7, Fade: 0, Window: 1}}

	res, err := Splice(pcm, cuts, Options{MinSegmentLength: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.PCM[0]) != len(pcm[0]) {
		t.Fatalf("hard cut changed length: got %d, want %d", len(res.PCM[0]), len(pcm[0]))
	}

	for f, m := range res.Mapping {
		refs := m.Sources[0]
		if len(refs) != 1 || refs[0].Gain != 1 || refs[0].Frame != f {
			t.Fatalf("frame %d: want identity map, got %+v", f, refs)
		}
		if res.PCM[0][f] != pcm[0][f] {
			t.Fatalf("frame %d: hard cut altered sample", f)
		}
	}

	if res.Boundaries[0].Boundary != 3 || res.Boundaries[1].Boundary != 7 {
		t.Fatalf("zero-jump ties should stay at nominal points, got %+v", res.Boundaries)
	}
}

// TestEqualPowerWindow checks the sine/cosine identity and endpoint behaviour.
func TestEqualPowerWindow(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 8, 16} {
		for k := 0; k < n; k++ {
			gL, gR := equalPowerGains(n, k)
			if sum := gL*gL + gR*gR; math.Abs(sum-1) > 1e-15 {
				t.Fatalf("n=%d k=%d: gL^2+gR^2 = %v", n, k, sum)
			}
		}
		if n > 1 {
			gL0, gR0 := equalPowerGains(n, 0)
			if gL0 != 1 || gR0 != 0 {
				t.Fatalf("n=%d: window must start at (1,0), got (%v,%v)", n, gL0, gR0)
			}
			gLe, gRe := equalPowerGains(n, n-1)
			if gLe != 0 || gRe != 1 {
				t.Fatalf("n=%d: window must end at (0,1), got (%v,%v)", n, gLe, gRe)
			}
		}
	}

	gL, gR := equalPowerGains(1, 0)
	if math.Abs(gL-math.Sqrt2/2) > 1e-15 || math.Abs(gR-math.Sqrt2/2) > 1e-15 {
		t.Fatalf("single-frame window = (%v,%v)", gL, gR)
	}
}

// TestJumpBeforeDistance crafts a case where a farther boundary removes a
// bigger DC step: jump minimisation must win over movement distance.
func TestJumpBeforeDistance(t *testing.T) {
	// Constant 1.0 except frame 6 which dips to -1: boundaries 6 and 7 carry
	// a step of size 2; every boundary up to 5 is step-free.
	signal := make([]float64, 12)
	for f := range signal {
		signal[f] = 1
	}
	signal[6] = -1
	pcm := [][]float64{signal}

	// Nominal point 6 sits on the size-2 step. The window reaches boundary 5
	// with jump 0 and distance 1, but also boundary 8 at distance 2: the
	// solver must leave the step for the farther click-free point.
	cuts := []Cut{{At: 6, Fade: 2, Window: 2}}
	res, err := Splice(pcm, cuts, Options{MinSegmentLength: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Boundaries[0].Boundary != 5 {
		t.Fatalf("want jump-free boundary 5, got %d", res.Boundaries[0].Boundary)
	}
	if res.Boundaries[0].MaxJump != 0 {
		t.Fatalf("want zero jump, got %v", res.Boundaries[0].MaxJump)
	}
}

// TestValidationAndNoCuts covers input validation and the no-cut pass-through.
func TestValidationAndNoCuts(t *testing.T) {
	if _, err := Splice(nil, nil, Options{}); err != ErrNoPCM {
		t.Fatalf("want ErrNoPCM, got %v", err)
	}

	ragged := [][]float64{{0, 1, 2}, {0, 1}}
	if _, err := Splice(ragged, nil, Options{}); err != ErrRaggedPCM {
		t.Fatalf("want ErrRaggedPCM, got %v", err)
	}

	pcm := [][]float64{{0, 1, 2, 3, 4}}
	badCases := [][]Cut{
		{{At: 0, Fade: 0, Window: 0}},
		{{At: 5, Fade: 0, Window: 0}},
		{{At: 2, Fade: -1, Window: 0}},
		{{At: 2, Fade: 0, Window: -1}},
		{{At: 2, Fade: 0, Window: 0}, {At: 2, Fade: 0, Window: 0}},
	}
	for _, bad := range badCases {
		if _, err := Splice(pcm, bad, Options{}); err != ErrInvalidCut {
			t.Fatalf("cuts %+v: want ErrInvalidCut, got %v", bad, err)
		}
	}
	if _, err := Splice(pcm, nil, Options{MinSegmentLength: -1}); err != ErrInvalidCut {
		t.Fatalf("want ErrInvalidCut for negative MinSegmentLength, got %v", err)
	}

	res, err := Splice(pcm, nil, Options{MinSegmentLength: 1})
	if err != nil {
		t.Fatalf("no-cut splice failed: %v", err)
	}
	if len(res.PCM[0]) != len(pcm[0]) || len(res.Boundaries) != 0 || len(res.Mapping) != len(pcm[0]) {
		t.Fatalf("no-cut splice altered the stream: %+v", res)
	}
	for f := range pcm[0] {
		if res.PCM[0][f] != pcm[0][f] {
			t.Fatalf("no-cut splice altered sample %d", f)
		}
	}

	if _, err := Splice(pcm, nil, Options{MinSegmentLength: 6}); err != ErrInfeasible {
		t.Fatalf("want ErrInfeasible for oversized head segment, got %v", err)
	}
}
