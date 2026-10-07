package cutsolve

import (
	"math/rand"
	"testing"
)

func randImage(rng *rand.Rand, w, h int) Image {
	pix := make([]byte, w*h)
	for i := range pix {
		pix[i] = byte(rng.Intn(2))
	}
	return NewImage(w, h, pix)
}

func mustParse(t *testing.T, s string) Image {
	t.Helper()
	im, err := ParseImage(s)
	if err != nil {
		t.Fatalf("ParseImage(%q): %v", s, err)
	}
	return im
}

// DP must match exhaustive enumeration on every small random instance:
// same optimal error, same leaf count, same tie-broken encoding.
func TestSingleMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	sizes := [][2]int{{1, 1}, {1, 4}, {2, 2}, {2, 3}, {3, 3}, {4, 3}, {4, 4}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		trials := 12
		if w*h >= 16 {
			trials = 2 // 4x4 has ~2.8M cut trees; keep enumeration affordable
		}
		for trial := 0; trial < trials; trial++ {
			im := randImage(rng, w, h)
			for _, k := range []int{1, 2, 3, 5, w * h} {
				got := SolveSingle(im, k)
				want := BruteSingle(im, k)
				if got.Err != want.Err || got.Leaves != want.Leaves ||
					got.Tree.Encoding() != want.Tree.Encoding() {
					t.Fatalf("%dx%d k=%d: dp=(err %d, leaves %d, %s) brute=(err %d, leaves %d, %s)",
						w, h, k, got.Err, got.Leaves, got.Tree.Encoding(),
						want.Err, want.Leaves, want.Tree.Encoding())
				}
				if got.Leaves > k {
					t.Fatalf("%dx%d k=%d: leaf budget violated (%d)", w, h, k, got.Leaves)
				}
				if e := got.Tree.FrameError(im, Rect{0, 0, w, h}); e != got.Err {
					t.Fatalf("%dx%d k=%d: reported err %d, recomputed %d", w, h, k, got.Err, e)
				}
			}
		}
	}
}

// Shared-template DP must match exhaustive enumeration of all shared trees,
// for feasible and infeasible cap combinations alike.
func TestDualMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	sizes := [][2]int{{1, 1}, {2, 2}, {2, 3}, {3, 3}, {4, 3}, {4, 4}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		trials := 8
		if w*h >= 16 {
			trials = 2
		}
		for trial := 0; trial < trials; trial++ {
			im0 := randImage(rng, w, h)
			im1 := randImage(rng, w, h)
			area := w * h
			ks := []int{1, 2, 4}
			if area <= 9 {
				ks = append(ks, area)
			}
			for _, k := range ks {
				for _, cap := range []int{0, 1, area / 4, area / 2, area} {
					got := SolveDual(im0, im1, k, cap, cap)
					want := BruteDual(im0, im1, k, cap, cap)
					if got.Feasible != want.Feasible {
						t.Fatalf("%dx%d k=%d cap=%d: feasible dp=%v brute=%v",
							w, h, k, cap, got.Feasible, want.Feasible)
					}
					if !got.Feasible {
						if got.Evidence == "" {
							t.Fatalf("%dx%d k=%d cap=%d: infeasible without evidence", w, h, k, cap)
						}
						continue
					}
					if got.Err0 != want.Err0 || got.Err1 != want.Err1 ||
						got.Leaves != want.Leaves || got.Tree.Encoding() != want.Tree.Encoding() {
						t.Fatalf("%dx%d k=%d cap=%d: dp=(%d,%d leaves %d %s) brute=(%d,%d leaves %d %s)",
							w, h, k, cap, got.Err0, got.Err1, got.Leaves, got.Tree.Encoding(),
							want.Err0, want.Err1, want.Leaves, want.Tree.Encoding())
					}
					if got.Err0 > cap || got.Err1 > cap {
						t.Fatalf("%dx%d k=%d cap=%d: cap violated (%d,%d)", w, h, k, cap, got.Err0, got.Err1)
					}
					if got.Leaves > k {
						t.Fatalf("%dx%d k=%d: leaf budget violated (%d)", w, h, k, got.Leaves)
					}
				}
			}
		}
	}
}

func TestSingleTieBreaks(t *testing.T) {
	// Uniform image: error 0 already with one leaf -> fewest leaves wins.
	im := mustParse(t, "00/00")
	res := SolveSingle(im, 4)
	if res.Err != 0 || res.Leaves != 1 || res.Tree.Encoding() != "L" {
		t.Fatalf("uniform: got err=%d leaves=%d enc=%s", res.Err, res.Leaves, res.Tree.Encoding())
	}

	// "01/01": leaf costs 2; V@1 gives two uniform columns (err 0, 2 leaves).
	im = mustParse(t, "01/01")
	res = SolveSingle(im, 2)
	if res.Err != 0 || res.Leaves != 2 || res.Tree.Encoding() != "V1LL" {
		t.Fatalf("columns: got err=%d leaves=%d enc=%s", res.Err, res.Leaves, res.Tree.Encoding())
	}

	// "01/10": every 2-leaf split still errs 2, so the 1-leaf tree wins on
	// fewest leaves despite equal error.
	im = mustParse(t, "01/10")
	res = SolveSingle(im, 2)
	if res.Err != 2 || res.Leaves != 1 || res.Tree.Encoding() != "L" {
		t.Fatalf("checker: got err=%d leaves=%d enc=%s", res.Err, res.Leaves, res.Tree.Encoding())
	}

	// "0101" (1x4): V@1 and V@3 both give err 1 with 2 leaves; the smaller
	// encoding "V1LL" must win.
	im = mustParse(t, "0101")
	res = SolveSingle(im, 2)
	if res.Err != 1 || res.Leaves != 2 || res.Tree.Encoding() != "V1LL" {
		t.Fatalf("encoding tie: got err=%d leaves=%d enc=%s", res.Err, res.Leaves, res.Tree.Encoding())
	}
}

func TestDualSharedTemplate(t *testing.T) {
	// f0 = "00/11" wants a horizontal cut, f1 = "01/01" wants a vertical cut.
	// One shared tree must serve both; the best total is 2 either way, and the
	// encoding tie resolves to "H1LL" < "V1LL".
	im0 := mustParse(t, "00/11")
	im1 := mustParse(t, "01/01")
	res := SolveDual(im0, im1, 2, 4, 4)
	if !res.Feasible || res.Err0+res.Err1 != 2 || res.Leaves != 2 || res.Tree.Encoding() != "H1LL" {
		t.Fatalf("shared template: got %+v enc=%v", res, res.Tree)
	}

	// Tight caps make the shared-template problem infeasible at k=2...
	res = SolveDual(im0, im1, 2, 0, 0)
	if res.Feasible || res.Evidence == "" {
		t.Fatalf("expected infeasible with evidence, got %+v", res)
	}
	// ...while optimizing each frame independently would "succeed" with err 0;
	// that is exactly what the shared template forbids. At k=4 the shared tree
	// splits into single pixels and both frames reach 0.
	res = SolveDual(im0, im1, 4, 0, 0)
	if !res.Feasible || res.Err0 != 0 || res.Err1 != 0 || res.Leaves != 4 {
		t.Fatalf("k=4 caps 0: got %+v", res)
	}
}

func TestDualAsymmetricCaps(t *testing.T) {
	im0 := mustParse(t, "00/11")
	im1 := mustParse(t, "01/01")
	// f0 can reach 0 via H@1 (f1 then errs 2); f1 can reach 0 via V@1
	// (f0 then errs 2). cap f0<=0, f1<=2 forces the horizontal cut.
	res := SolveDual(im0, im1, 2, 0, 2)
	if !res.Feasible || res.Err0 != 0 || res.Err1 != 2 || res.Tree.Encoding() != "H1LL" {
		t.Fatalf("asymmetric caps: got %+v", res)
	}
	res = SolveDual(im0, im1, 2, 2, 0)
	if !res.Feasible || res.Err0 != 2 || res.Err1 != 0 || res.Tree.Encoding() != "V1LL" {
		t.Fatalf("asymmetric caps swapped: got %+v", res)
	}
}

// 8x8 exercises the full-size DP path (no brute force); invariants only.
func TestSingle8x8(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 5; trial++ {
		im := randImage(rng, 8, 8)
		prev := SolveSingle(im, 1)
		for _, k := range []int{2, 4, 8, 16, 64} {
			res := SolveSingle(im, k)
			if res.Leaves > k {
				t.Fatalf("k=%d: %d leaves", k, res.Leaves)
			}
			if res.Err > prev.Err {
				t.Fatalf("k=%d: err %d worse than smaller budget err %d", k, res.Err, prev.Err)
			}
			if e := res.Tree.FrameError(im, Rect{0, 0, 8, 8}); e != res.Err {
				t.Fatalf("k=%d: reported %d, recomputed %d", k, res.Err, e)
			}
			prev = res
		}
	}
}

func TestDual8x8(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	im0 := randImage(rng, 8, 8)
	im1 := randImage(rng, 8, 8)
	res := SolveDual(im0, im1, 12, 20, 20)
	if !res.Feasible {
		t.Fatalf("unexpected infeasible:\n%s", res.Evidence)
	}
	if res.Leaves > 12 || res.Err0 > 20 || res.Err1 > 20 {
		t.Fatalf("constraints violated: %+v", res)
	}
	root := Rect{0, 0, 8, 8}
	if e := res.Tree.FrameError(im0, root); e != res.Err0 {
		t.Fatalf("f0: reported %d, recomputed %d", res.Err0, e)
	}
	if e := res.Tree.FrameError(im1, root); e != res.Err1 {
		t.Fatalf("f1: reported %d, recomputed %d", res.Err1, e)
	}
}
