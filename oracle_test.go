package floatconv_test

import (
	"math"
	"math/rand"
	"testing"

	"floatconv"
)

// Independent hardware oracle: binary64 -> binary32 round-to-nearest-even
// must agree with the CPU's own float32(float64) conversion bit-for-bit
// (NaN payloads excluded: their propagation is implementation-defined).
func TestBinary64ToBinary32VsHardware(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	tested := 0
	for tested < 500000 {
		bits := rng.Uint64()
		exp := bits >> 52 & 0x7FF
		if exp == 0x7FF && bits&(1<<52-1) != 0 {
			continue // NaN
		}
		got, _, err := floatconv.Convert(floatconv.Binary32, floatconv.Binary64, bits, floatconv.RoundNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(math.Float32bits(float32(math.Float64frombits(bits))))
		if got != want {
			t.Fatalf("bits %#x: got %#x, hardware %#x", bits, got, want)
		}
		tested++
	}
}
