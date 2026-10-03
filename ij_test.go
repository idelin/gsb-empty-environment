package hexgrid

import (
	"math/rand"
	"testing"
)

func TestDigitOffsetsResiduesBijective(t *testing.T) {
	seen := map[int]int{}
	for d, off := range digitOffsets {
		r := mod7(3*off.I + off.J)
		if prev, dup := seen[r]; dup {
			t.Fatalf("digits %d and %d share residue %d", prev, d, r)
		}
		seen[r] = d
		if residueToDigit[r] != uint8(d) {
			t.Fatalf("residueToDigit[%d] = %d, want %d", r, residueToDigit[r], d)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("only %d distinct residues", len(seen))
	}
}

func TestMulDivMInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100000; i++ {
		p := IJ{rng.Intn(2000) - 1000, rng.Intn(2000) - 1000}
		if q := divM(mulM(p)); q != p {
			t.Fatalf("divM(mulM(%v)) = %v", p, q)
		}
	}
}

func TestDigitsIJRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2000; trial++ {
		res := 1 + rng.Intn(MaxRes)
		digits := make([]uint8, res)
		for i := range digits {
			digits[i] = uint8(rng.Intn(7))
		}
		ij := digitsToIJ(digits)
		back, ok := ijToDigits(ij, res)
		if !ok {
			t.Fatalf("ijToDigits failed for %v (ij=%v)", digits, ij)
		}
		if len(back) != len(digits) {
			t.Fatalf("len %d, want %d", len(back), len(digits))
		}
		for i := range digits {
			if back[i] != digits[i] {
				t.Fatalf("digits %v -> ij %v -> %v", digits, ij, back)
			}
		}
	}
}

// TestParentChainConverges proves the lattice has no foreign cycles near
// the origin: every IJ within radius 60 reaches the base-cell origin,
// so every such point is representable at fine enough resolution.
func TestParentChainConverges(t *testing.T) {
	for i := -60; i <= 60; i++ {
		for j := -60; j <= 60; j++ {
			if ijDistance(IJ{}, IJ{i, j}) > 60 {
				continue
			}
			p := IJ{i, j}
			reached := false
			for step := 0; step < 64; step++ {
				if p == (IJ{0, 0}) {
					reached = true
					break
				}
				d := residueToDigit[mod7(3*p.I+p.J)]
				off := digitOffsets[d]
				p = divM(IJ{p.I - off.I, p.J - off.J})
			}
			if !reached {
				t.Fatalf("parent chain from (%d,%d) never reaches origin", i, j)
			}
		}
	}
}

func TestIJDistance(t *testing.T) {
	if d := ijDistance(IJ{0, 0}, IJ{0, 0}); d != 0 {
		t.Fatalf("self distance %d", d)
	}
	for _, dir := range ijDirections {
		if d := ijDistance(IJ{0, 0}, dir); d != 1 {
			t.Fatalf("unit direction %v at distance %d", dir, d)
		}
	}
	if d := ijDistance(IJ{3, -1}, IJ{-2, 4}); d != ijDistance(IJ{-2, 4}, IJ{3, -1}) {
		t.Fatal("distance not symmetric")
	}
}
