package quant

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteFrames enumerates every assignment sequence across frames and
// returns the minimum total error satisfying all budgets and caps.
func bruteFrames(frames []FrameSpec, palette []int) (int64, bool) {
	if len(frames) == 0 {
		return 0, true
	}
	p := len(frames[0].Pixels)
	k := len(palette)
	if k == 0 {
		return 0, p == 0
	}
	best := int64(-1)
	var rec func(f int, total int64, prev []int)
	rec = func(f int, total int64, prev []int) {
		if f == len(frames) {
			if best < 0 || total < best {
				best = total
			}
			return
		}
		cur := make([]int, p) // palette index per pixel for this frame
		var assign func(i int, err int64, moved int)
		assign = func(i int, err int64, moved int) {
			if i == p {
				if frames[f].MaxErr >= 0 && err > frames[f].MaxErr {
					return
				}
				if f > 0 && moved > frames[f].Budget {
					return
				}
				rec(f+1, total+err, append([]int(nil), cur...))
				return
			}
			for c := 0; c < k; c++ {
				d := int64(frames[f].Pixels[i] - palette[c])
				mv := moved
				if f > 0 && prev[i] != c {
					mv++
				}
				cur[i] = c
				assign(i+1, err+d*d, mv)
			}
		}
		assign(0, 0, 0)
	}
	rec(0, 0, nil)
	return best, best >= 0
}

func checkFramesAgainstBrute(t *testing.T, frames []FrameSpec, palette []int) {
	t.Helper()
	res := SolveFrames(frames, palette)
	wantTotal, wantOK := bruteFrames(frames, palette)
	if res.OK != wantOK {
		t.Fatalf("SolveFrames(%+v, %v): OK=%v, brute force=%v",
			frames, palette, res.OK, wantOK)
	}
	if !res.OK {
		return
	}
	if res.Total != wantTotal {
		t.Fatalf("SolveFrames(%+v, %v): Total=%d, brute force=%d",
			frames, palette, res.Total, wantTotal)
	}
	// The returned mapping must itself satisfy every constraint.
	pal := append([]int(nil), palette...)
	sort.Ints(pal)
	var sum int64
	for f, fr := range res.Frames {
		var err int64
		for i, g := range frames[f].Pixels {
			d := int64(g - fr.Rep[i])
			err += d * d
		}
		if err != fr.Err {
			t.Fatalf("frame %d: reported Err=%d, recomputed=%d", f, fr.Err, err)
		}
		if frames[f].MaxErr >= 0 && err > frames[f].MaxErr {
			t.Fatalf("frame %d: Err=%d exceeds cap %d", f, err, frames[f].MaxErr)
		}
		inPalette := map[int]bool{}
		for _, c := range pal {
			inPalette[c] = true
		}
		for _, r := range fr.Rep {
			if !inPalette[r] {
				t.Fatalf("frame %d: representative %d not in palette", f, r)
			}
		}
		if f > 0 {
			moved := 0
			for i := range fr.Rep {
				if fr.Rep[i] != res.Frames[f-1].Rep[i] {
					moved++
				}
			}
			if moved != fr.Moved {
				t.Fatalf("frame %d: reported Moved=%d, recomputed=%d", f, fr.Moved, moved)
			}
			if moved > frames[f].Budget {
				t.Fatalf("frame %d: Moved=%d exceeds budget %d", f, moved, frames[f].Budget)
			}
		}
		sum += err
	}
	if sum != res.Total {
		t.Fatalf("Total=%d, sum of frame errors=%d", res.Total, sum)
	}
}

func TestSolveFramesHandcrafted(t *testing.T) {
	// Palette {0, 10}; frame 0 is all zeros, frame 1 is all tens, and
	// only one pixel may migrate. The optimum keeps one pixel on 0.
	frames := []FrameSpec{
		{Pixels: []int{0, 0}, Budget: 0, MaxErr: -1},
		{Pixels: []int{10, 10}, Budget: 1, MaxErr: -1},
	}
	res := SolveFrames(frames, []int{0, 10})
	if !res.OK || res.Total != 100 {
		t.Fatalf("got %+v, want total 100", res)
	}
	if res.Frames[1].Moved != 1 {
		t.Fatalf("frame 1 should migrate exactly one pixel: %+v", res.Frames[1])
	}
	checkFramesAgainstBrute(t, frames, []int{0, 10})
}

func TestFramesCannotBeQuantizedIndependently(t *testing.T) {
	frames := []FrameSpec{
		{Pixels: []int{0, 0}, Budget: 0, MaxErr: -1},
		{Pixels: []int{10, 10}, Budget: 1, MaxErr: -1},
	}
	palette := []int{0, 10}
	joint := SolveFrames(frames, palette)

	// Independent quantization: no migration limit.
	loose := []FrameSpec{frames[0], {Pixels: frames[1].Pixels, Budget: 2, MaxErr: -1}}
	indep := SolveFrames(loose, palette)
	if indep.Total != 0 {
		t.Fatalf("independent optimum should be 0, got %d", indep.Total)
	}
	if indep.Frames[1].Moved > frames[1].Budget {
		// The independent solution violates the migration budget...
		if joint.Total <= indep.Total {
			t.Fatalf("joint total %d should exceed independent total %d",
				joint.Total, indep.Total)
		}
	} else {
		t.Fatalf("independent solution should violate the budget: %+v", indep.Frames[1])
	}
	if joint.Total != 100 {
		t.Fatalf("joint optimum should be 100, got %d", joint.Total)
	}
}

func TestSolveFramesMaxErr(t *testing.T) {
	// The cap on frame 1 forbids leaving both pixels on 0 (error 200),
	// so at least one pixel must migrate even though the budget is tight.
	frames := []FrameSpec{
		{Pixels: []int{0, 0}, Budget: 0, MaxErr: -1},
		{Pixels: []int{10, 10}, Budget: 1, MaxErr: 100},
	}
	res := SolveFrames(frames, []int{0, 10})
	if !res.OK || res.Total != 100 || res.Frames[1].Err != 100 {
		t.Fatalf("got %+v", res)
	}
	// Pin frame 0 to [0,0] and cap frame 1 at 50: with budget 1 the best
	// achievable frame-1 error is 100, so the instance is infeasible.
	frames[0].MaxErr = 0
	frames[1].MaxErr = 50
	if res := SolveFrames(frames, []int{0, 10}); res.OK {
		t.Fatalf("cap 50 should be infeasible, got %+v", res)
	}
	checkFramesAgainstBrute(t, frames, []int{0, 10})
}

func TestSolveFramesRandomVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 200; trial++ {
		tn := 1 + rng.Intn(3)
		p := 1 + rng.Intn(3)
		frames := make([]FrameSpec, tn)
		for f := 0; f < tn; f++ {
			px := make([]int, p)
			for i := range px {
				px[i] = rng.Intn(8)
			}
			frames[f] = FrameSpec{
				Pixels: px,
				Budget: rng.Intn(p + 1),
				MaxErr: -1,
			}
			if rng.Intn(3) == 0 {
				frames[f].MaxErr = int64(rng.Intn(60))
			}
		}
		k := 1 + rng.Intn(3)
		palette := make([]int, k)
		for i := range palette {
			palette[i] = rng.Intn(8)
		}
		checkFramesAgainstBrute(t, frames, palette)
	}
}

// bruteOptimize enumerates every palette of size k from the domain.
func bruteOptimize(frames []FrameSpec, domain []int, k int) (int64, []int, bool) {
	dom := append([]int(nil), domain...)
	sort.Ints(dom)
	best := int64(-1)
	var bestPal []int
	idx := make([]int, k)
	var rec func(pos, lo int)
	rec = func(pos, lo int) {
		if pos == k {
			pal := make([]int, k)
			for i, j := range idx {
				pal[i] = dom[j]
			}
			if total, ok := bruteFrames(frames, pal); ok {
				if best < 0 || total < best {
					best = total
					bestPal = pal
				}
			}
			return
		}
		for j := lo; j <= len(dom)-(k-pos); j++ {
			idx[pos] = j
			rec(pos+1, j+1)
		}
	}
	rec(0, 0)
	return best, bestPal, best >= 0
}

func TestOptimizeFramesVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 60; trial++ {
		tn := 1 + rng.Intn(3)
		p := 1 + rng.Intn(3)
		frames := make([]FrameSpec, tn)
		for f := 0; f < tn; f++ {
			px := make([]int, p)
			for i := range px {
				px[i] = rng.Intn(6)
			}
			frames[f] = FrameSpec{Pixels: px, Budget: rng.Intn(p + 1), MaxErr: -1}
		}
		domain := []int{0, 1, 2, 3, 4, 5}
		k := 1 + rng.Intn(3)
		res := OptimizeFrames(frames, domain, k)
		wantTotal, _, wantOK := bruteOptimize(frames, domain, k)
		if res.OK != wantOK {
			t.Fatalf("OptimizeFrames(%+v): OK=%v, brute=%v", frames, res.OK, wantOK)
		}
		if res.OK && res.Total != wantTotal {
			t.Fatalf("OptimizeFrames(%+v): Total=%d, brute=%d (palette %v)",
				frames, res.Total, wantTotal, res.Palette)
		}
	}
}

func TestOptimizeFramesPaletteTie(t *testing.T) {
	// Frames are all zeros; palettes {0,5} and {0,9} both give total 0.
	// The lexicographically smallest optimal palette wins.
	frames := []FrameSpec{
		{Pixels: []int{0, 0}, Budget: 0, MaxErr: -1},
		{Pixels: []int{0, 0}, Budget: 0, MaxErr: -1},
	}
	res := OptimizeFrames(frames, []int{0, 5, 9}, 2)
	if !res.OK || res.Total != 0 {
		t.Fatalf("got %+v", res)
	}
	if res.Palette[0] != 0 || res.Palette[1] != 5 {
		t.Fatalf("tie should pick lexicographically smallest palette, got %v", res.Palette)
	}
}

func TestOptimizeFramesDefaultDomain(t *testing.T) {
	frames := []FrameSpec{
		{Pixels: []int{1, 2}, Budget: 0, MaxErr: -1},
		{Pixels: []int{2, 3}, Budget: 1, MaxErr: -1},
	}
	res := OptimizeFrames(frames, nil, 2)
	wantTotal, _, _ := bruteOptimize(frames, []int{1, 2, 3}, 2)
	if !res.OK || res.Total != wantTotal {
		t.Fatalf("got %+v, want total %d", res, wantTotal)
	}
}

func TestSolveFramesInfeasible(t *testing.T) {
	// Palette cannot represent anything within the caps.
	frames := []FrameSpec{
		{Pixels: []int{100}, Budget: 0, MaxErr: 1},
	}
	if res := SolveFrames(frames, []int{0}); res.OK {
		t.Fatalf("should be infeasible: %+v", res)
	}
	if res := SolveFrames(frames, nil); res.OK {
		t.Fatalf("empty palette should be infeasible: %+v", res)
	}
}
