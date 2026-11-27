package writeproof

import "testing"

func TestIntervalDecompositionCoversRange(t *testing.T) {
	cubes := decomposeInterval(181, 240, 8)
	covered := make(map[uint64]bool)
	for _, cube := range cubes {
		size := cube.FreeMask + 1
		for offset := uint64(0); offset < size; offset++ {
			address := cube.Base | offset
			if address < 181 || address >= 240 || covered[address] {
				t.Fatalf("bad cube %+v at address %d", cube, address)
			}
			covered[address] = true
		}
	}
	if len(covered) != 59 {
		t.Fatalf("covered=%d cubes=%+v", len(covered), cubes)
	}
}
