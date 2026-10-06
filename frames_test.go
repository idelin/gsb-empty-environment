package quant

import (
	"math/rand"
	"testing"
)

// bruteForceFrames enumerates every sequence of per-frame assignment
// vectors and returns the minimal total cost satisfying the migration
// budget and the frame error limit. It is an independent reference for
// QuantizeFrames.
func bruteForceFrames(frames [][]int, palette []int, budget []int, maxFrameErr int64) (int64, bool) {
	F := len(frames)
	N := len(frames[0])
	K := len(palette)
	S := 1
	for i := 0; i < N; i++ {
		S *= K
	}
	digits := make([][]int, S)
	for s := 0; s < S; s++ {
		d := make([]int, N)
		x := s
		for i := N - 1; i >= 0; i-- {
			d[i] = x % K
			x /= K
		}
		digits[s] = d
	}
	frameCost := func(f, s int) int64 {
		var c int64
		for i := 0; i < N; i++ {
			d := int64(frames[f][i] - palette[digits[s][i]])
			c += d * d
		}
		return c
	}
	limit := maxFrameErr
	if limit < 0 {
		limit = int64(1<<62 - 1)
	}
	best := int64(1<<62 - 1)
	found := false
	path := make([]int, F)
	var rec func(f, prevState int, acc int64)
	rec = func(f, prevState int, acc int64) {
		if f == F {
			if acc < best {
				best = acc
				found = true
			}
			return
		}
		for s := 0; s < S; s++ {
			c := frameCost(f, s)
			if c > limit || acc+c >= best {
				continue
			}
			if f > 0 {
				moved := 0
				for i := 0; i < N; i++ {
					if digits[prevState][i] != digits[s][i] {
						moved++
					}
				}
				if moved > budget[f-1] {
					continue
				}
			}
			path[f] = s
			rec(f+1, s, acc+c)
		}
	}
	rec(0, -1, 0)
	return best, found
}

// independentAssign maps every pixel of every frame to its nearest
// palette entry (ties to the smaller value), ignoring the migration
// budget entirely.
func independentAssign(frames [][]int, palette []int) (assign [][]int, changed []int, total int64) {
	F := len(frames)
	N := len(frames[0])
	assign = make([][]int, F)
	changed = make([]int, F)
	for f := 0; f < F; f++ {
		assign[f] = make([]int, N)
		for i := 0; i < N; i++ {
			best, bi := int64(1<<62-1), 0
			for j, p := range palette {
				d := int64(frames[f][i] - p)
				if c := d * d; c < best {
					best, bi = c, j
				}
			}
			assign[f][i] = bi
			total += best
		}
		if f > 0 {
			for i := 0; i < N; i++ {
				if assign[f][i] != assign[f-1][i] {
					changed[f]++
				}
			}
		}
	}
	return assign, changed, total
}

func TestFramesCannotQuantizeIndependently(t *testing.T) {
	// Palette {0, 10}; the two pixels swap their preferred entry in
	// frame 1, but the budget forbids any migration. The joint optimum
	// keeps one fixed assignment for both frames at total cost 164,
	// while independent per-frame quantization costs only 4 but moves
	// both pixels and violates the budget.
	frames := [][]int{{1, 9}, {9, 1}}
	palette := []int{0, 10}
	res, err := QuantizeFrames(frames, palette, []int{0}, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 164 {
		t.Fatalf("total = %d, want 164", res.Total)
	}
	if res.Changed[1] != 0 {
		t.Fatalf("changed = %v, want no migration", res.Changed)
	}
	for i := 0; i < 2; i++ {
		if res.Assign[0][i] != res.Assign[1][i] {
			t.Fatalf("pixel %d changed assignment: %v -> %v", i, res.Assign[0], res.Assign[1])
		}
	}
	_, indepChanged, indepTotal := independentAssign(frames, palette)
	if indepChanged[1] != 2 {
		t.Fatalf("independent quantization moved %d pixels, want 2", indepChanged[1])
	}
	if indepTotal >= res.Total {
		t.Fatalf("independent total %d should beat (infeasible) joint %d", indepTotal, res.Total)
	}
}

func TestFramesBudgetAllowsOneMigration(t *testing.T) {
	// Same setup but one pixel may move: the optimum moves the pixel
	// with the larger gain.
	frames := [][]int{{1, 9}, {9, 1}}
	palette := []int{0, 10}
	res, err := QuantizeFrames(frames, palette, []int{1}, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed[1] != 1 {
		t.Fatalf("changed = %v, want exactly 1 migration", res.Changed)
	}
	// Frame 0 keeps (0,10) with cost 2; frame 1 flips one pixel:
	// (0,10)->cost 82 or (10,0)... best is flipping one pixel: cost
	// 1+81 = 82 either way, total 2+82 = 84.
	if res.Total != 84 {
		t.Fatalf("total = %d, want 84", res.Total)
	}
	want, ok := bruteForceFrames(frames, palette, []int{1}, -1)
	if !ok || want != res.Total {
		t.Fatalf("brute force = %d (%v), solver = %d", want, ok, res.Total)
	}
}

func TestFramesErrorLimitInfeasible(t *testing.T) {
	frames := [][]int{{1, 9}, {9, 1}}
	palette := []int{0, 10}
	// Every frame needs at least error 2 (pixels 1 and 9 are distance 1
	// from the nearest entry), so a limit of 1 is infeasible.
	if _, err := QuantizeFrames(frames, palette, []int{2}, 1); err == nil {
		t.Fatal("expected infeasibility error")
	}
	res, err := QuantizeFrames(frames, palette, []int{2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 {
		t.Fatalf("total = %d, want 4", res.Total)
	}
	for f, e := range res.FrameErr {
		if e > 2 {
			t.Fatalf("frame %d error %d exceeds limit", f, e)
		}
	}
}

func TestFramesValidation(t *testing.T) {
	frames := [][]int{{1, 2}, {3, 4}}
	palette := []int{0, 10}
	if _, err := QuantizeFrames(nil, palette, nil, -1); err == nil {
		t.Fatal("expected error for no frames")
	}
	if _, err := QuantizeFrames(frames, nil, []int{0}, -1); err == nil {
		t.Fatal("expected error for empty palette")
	}
	if _, err := QuantizeFrames(frames, palette, nil, -1); err == nil {
		t.Fatal("expected error for missing budget")
	}
	if _, err := QuantizeFrames(frames, palette, []int{-1}, -1); err == nil {
		t.Fatal("expected error for negative budget")
	}
	if _, err := QuantizeFrames(frames, []int{5, 5}, []int{0}, -1); err == nil {
		t.Fatal("expected error for duplicate palette entries")
	}
	if _, err := QuantizeFrames([][]int{{1}, {2, 3}}, palette, []int{0}, -1); err == nil {
		t.Fatal("expected error for ragged frames")
	}
	if _, err := QuantizeFrames([][]int{make([]int, 20)}, palette, nil, -1); err == nil {
		t.Fatal("expected error for oversized state space")
	}
}

func TestFramesMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		F := 1 + rng.Intn(3) // 1..3 frames
		N := 1 + rng.Intn(4) // 1..4 pixels
		K := 2 + rng.Intn(2) // palette of 2..3 entries
		palette := []int{0, 5, 10}[:K]
		frames := make([][]int, F)
		for f := range frames {
			frames[f] = make([]int, N)
			for i := range frames[f] {
				frames[f][i] = rng.Intn(11)
			}
		}
		budget := make([]int, F-1)
		for i := range budget {
			budget[i] = rng.Intn(N + 1)
		}
		limit := int64(-1)
		if rng.Intn(2) == 0 {
			limit = int64(rng.Intn(60)) // sometimes tight, often infeasible
		}
		res, err := QuantizeFrames(frames, palette, budget, limit)
		want, ok := bruteForceFrames(frames, palette, budget, limit)
		if !ok {
			if err == nil {
				t.Fatalf("trial %d: solver found %d but brute force says infeasible", trial, res.Total)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: solver failed but brute force found %d", trial, want)
		}
		if res.Total != want {
			t.Fatalf("trial %d: frames=%v palette=%v budget=%v limit=%d solver=%d brute=%d",
				trial, frames, palette, budget, limit, res.Total, want)
		}
		// Verify reported migration counts and per-frame errors.
		for f := 0; f < F; f++ {
			var e int64
			for i := 0; i < N; i++ {
				d := int64(frames[f][i] - palette[res.Assign[f][i]])
				e += d * d
			}
			if e != res.FrameErr[f] {
				t.Fatalf("trial %d: frame %d error %d, reported %d", trial, f, e, res.FrameErr[f])
			}
			if limit >= 0 && e > limit {
				t.Fatalf("trial %d: frame %d error %d exceeds limit %d", trial, f, e, limit)
			}
			if f > 0 {
				moved := 0
				for i := 0; i < N; i++ {
					if res.Assign[f][i] != res.Assign[f-1][i] {
						moved++
					}
				}
				if moved != res.Changed[f] {
					t.Fatalf("trial %d: frame %d moved %d, reported %d", trial, f, moved, res.Changed[f])
				}
				if moved > budget[f-1] {
					t.Fatalf("trial %d: frame %d moved %d over budget %d", trial, f, moved, budget[f-1])
				}
			}
		}
	}
}

func TestFramesWithDerivedPalette(t *testing.T) {
	// End-to-end: derive a shared palette from the combined histogram
	// via Quantize, then solve the multi-frame problem with it.
	frames := [][]int{
		{10, 12, 40, 41},
		{11, 11, 39, 42},
		{12, 10, 41, 40},
	}
	pal, err := Quantize(CombinedHistogram(frames), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pal.Palette) != 2 {
		t.Fatalf("palette = %v", pal.Palette)
	}
	res, err := QuantizeFrames(frames, pal.Palette, []int{1, 1}, -1)
	if err != nil {
		t.Fatal(err)
	}
	want, ok := bruteForceFrames(frames, pal.Palette, []int{1, 1}, -1)
	if !ok || res.Total != want {
		t.Fatalf("solver = %d, brute force = %d (%v)", res.Total, want, ok)
	}
}
