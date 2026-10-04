package imgcodec

import (
	"math/rand"
	"testing"
)

// bruteForceCost computes the optimum by trying every valid instruction
// length at every position (memoized, but with no pruning of the
// transition set). It is the ground truth for the pruned DP in Encode.
func bruteForceCost(pix []byte, p Params) int {
	n := len(pix)
	w := p.Width
	run := make([]int, n+1)
	ref := make([]int, n+1)
	for i := n - 1; i >= 0; i-- {
		run[i] = 1
		if i+1 < n && pix[i] == pix[i+1] {
			run[i] = run[i+1] + 1
		}
		if i >= w && pix[i] == pix[i-w] {
			ref[i] = ref[i+1] + 1
		}
	}
	memo := make([]int, n+1)
	for i := range memo {
		memo[i] = -1
	}
	memo[n] = 0
	var solve func(i int) int
	solve = func(i int) int {
		if memo[i] >= 0 {
			return memo[i]
		}
		best := int(^uint(0) >> 1)
		for l := 1; l <= n-i && l <= maxWireLen; l++ {
			if c := p.LitHeader + l + solve(i+l); c < best {
				best = c
			}
		}
		rl := run[i]
		if rl > maxWireLen {
			rl = maxWireLen
		}
		for l := 1; l <= rl; l++ {
			if c := p.RepHeader + 1 + solve(i+l); c < best {
				best = c
			}
		}
		if i >= w {
			m := ref[i]
			if m > p.MaxRefLen {
				m = p.MaxRefLen
			}
			if rowLeft := w - i%w; m > rowLeft {
				m = rowLeft
			}
			for l := 1; l <= m; l++ {
				if c := p.RefHeader + solve(i+l); c < best {
					best = c
				}
			}
		}
		memo[i] = best
		return best
	}
	return solve(0)
}

// randomImage generates structured and unstructured small images.
func randomImage(rng *rand.Rand, w, h int) []byte {
	pix := make([]byte, w*h)
	switch rng.Intn(4) {
	case 0: // uniform random, small alphabet -> runs and matches
		for i := range pix {
			pix[i] = byte(rng.Intn(1 + rng.Intn(4)))
		}
	case 1: // solid
		for i := range pix {
			pix[i] = 7
		}
	case 2: // rows copied from row 0 with noise -> long references
		for x := 0; x < w; x++ {
			pix[x] = byte(rng.Intn(3))
		}
		for y := 1; y < h; y++ {
			for x := 0; x < w; x++ {
				v := pix[(y-1)*w+x]
				if rng.Intn(4) == 0 {
					v = byte(rng.Intn(3))
				}
				pix[y*w+x] = v
			}
		}
	default: // full-range noise
		for i := range pix {
			pix[i] = byte(rng.Intn(256))
		}
	}
	return pix
}

func randomParams(rng *rand.Rand) Params {
	return Params{
		Width:     1 + rng.Intn(6),
		Height:    1 + rng.Intn(5),
		LitHeader: rng.Intn(6),
		RepHeader: rng.Intn(6),
		RefHeader: rng.Intn(6),
		MaxRefLen: 1 + rng.Intn(8),
	}
}

func TestEncodeMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 2000; trial++ {
		p := randomParams(rng)
		pix := randomImage(rng, p.Width, p.Height)
		instrs, err := Encode(pix, p)
		if err != nil {
			t.Fatalf("trial %d: Encode: %v", trial, err)
		}
		got := p.Cost(instrs)
		want := bruteForceCost(pix, p)
		if got != want {
			t.Fatalf("trial %d: params %+v pix %v: Encode cost %d, brute force %d, instrs %v",
				trial, p, pix, got, want, instrs)
		}
	}
}

// TestEncodeValidAndRoundTrips checks every emitted instruction stream is
// legal and decodes back to the original pixels.
func TestEncodeValidAndRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 2000; trial++ {
		p := randomParams(rng)
		pix := randomImage(rng, p.Width, p.Height)
		instrs, err := Encode(pix, p)
		if err != nil {
			t.Fatalf("trial %d: Encode: %v", trial, err)
		}
		pos := 0
		for _, in := range instrs {
			if in.Len < 1 {
				t.Fatalf("trial %d: non-positive length in %v", trial, in)
			}
			if in.Op == OpRef {
				if pos < p.Width {
					t.Fatalf("trial %d: ref in first row at %d", trial, pos)
				}
				if in.Len > p.MaxRefLen {
					t.Fatalf("trial %d: ref len %d > MaxRefLen %d", trial, in.Len, p.MaxRefLen)
				}
				if pos%p.Width+in.Len > p.Width {
					t.Fatalf("trial %d: ref crosses row at %d", trial, pos)
				}
			}
			pos += in.Len
		}
		if pos != len(pix) {
			t.Fatalf("trial %d: instructions cover %d of %d pixels", trial, pos, len(pix))
		}
		data, err := Marshal(instrs)
		if err != nil {
			t.Fatalf("trial %d: Marshal: %v", trial, err)
		}
		back, err := Decode(data, p)
		if err != nil {
			t.Fatalf("trial %d: Decode: %v", trial, err)
		}
		for i := range pix {
			if back[i] != pix[i] {
				t.Fatalf("trial %d: round trip mismatch at %d", trial, i)
			}
		}
	}
}
