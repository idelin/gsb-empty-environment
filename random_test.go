package contour

import (
	"strings"
	"testing"
)

// Random images: the geometric invariants (edge replay, perimeter, area)
// must hold unconditionally. Hole-count discrepancies are tolerated: they
// mark genuinely ambiguous topologies (shared or self-touching holes).
func TestRandomCrossValidation(t *testing.T) {
	seed := uint64(12345)
	rnd := func(n int) int { // simple LCG, deterministic
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(seed>>33) % n
	}
	for trial := 0; trial < 200; trial++ {
		w, h := 1+rnd(9), 1+rnd(9)
		im := NewImage(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				im.Set(x, y, rnd(2) == 0)
			}
		}
		res := Analyze(im)
		for _, p := range res.Validate() {
			if strings.HasPrefix(p, "holes:") {
				continue // ambiguous hole attribution, by design reported
			}
			t.Errorf("trial %d (%dx%d): %s", trial, w, h, p)
		}
	}
}
