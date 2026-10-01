package arithmetic

import (
	"math"
	"math/rand"
	"testing"
)

// skewedStream returns n symbols, each 0 with probability p0, using a fixed
// seed so tests are deterministic.
func skewedStream(n int, p0 float64, seed int64) []int {
	rng := rand.New(rand.NewSource(seed))
	symbols := make([]int, n)
	for i := range symbols {
		if rng.Float64() < p0 {
			symbols[i] = 0
		} else {
			symbols[i] = 1
		}
	}
	return symbols
}

func roundTrip(t *testing.T, symbols []int) []byte {
	t.Helper()
	encoded := Encode(symbols, NewBinaryModel())
	decoded := Decode(encoded, NewBinaryModel())
	if len(decoded) != len(symbols) {
		t.Fatalf("decoded %d symbols, want %d", len(decoded), len(symbols))
	}
	for i := range symbols {
		if decoded[i] != symbols[i] {
			t.Fatalf("symbol %d: got %d, want %d", i, decoded[i], symbols[i])
		}
	}
	return encoded
}

func TestRoundTripPatterns(t *testing.T) {
	cases := map[string][]int{
		"empty":       {},
		"single zero": {0},
		"single one":  {1},
		"all zeros":   {0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"all ones":    {1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		"alternating": {0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1},
		"mixed":       {0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0},
	}
	for name, symbols := range cases {
		t.Run(name, func(t *testing.T) {
			roundTrip(t, symbols)
		})
	}
}

func TestRoundTripRandom(t *testing.T) {
	for _, p0 := range []float64{0.5, 0.9, 0.99, 0.1} {
		symbols := skewedStream(20000, p0, 42)
		roundTrip(t, symbols)
	}
}

// TestCompressionBeatsHuffman verifies the acceptance boundary: on a binary
// stream with 90% zeros, the arithmetic coder output must be far below the
// Huffman theoretical lower bound (1 bit/symbol for a binary alphabet) and
// approach the Shannon entropy limit.
func TestCompressionBeatsHuffman(t *testing.T) {
	const n = 100000
	const p0 = 0.9
	symbols := skewedStream(n, p0, 7)

	encoded := roundTrip(t, symbols)

	// Shannon entropy of the ideal Bernoulli(0.9) source, in bits/symbol.
	entropy := -(p0*math.Log2(p0) + (1-p0)*math.Log2(1-p0))
	entropyBytes := entropy * n / 8

	// Huffman on a binary alphabet can never do better than 1 bit/symbol.
	huffmanBytes := float64(n) / 8

	got := float64(len(encoded))
	t.Logf("symbols:            %d", n)
	t.Logf("arithmetic coded:   %.0f bytes (%.4f bits/symbol)", got, got*8/n)
	t.Logf("shannon entropy:    %.0f bytes (%.4f bits/symbol)", entropyBytes, entropy)
	t.Logf("huffman lower bound: %.0f bytes (1.0000 bits/symbol)", huffmanBytes)

	// Must approach the entropy limit (small overhead for the adaptive
	// model warm-up, the 8 byte header and stream termination).
	if got > entropyBytes*1.05+16 {
		t.Errorf("encoded size %.0f bytes exceeds entropy bound %.0f by more than 5%%", got, entropyBytes)
	}
	// Must be significantly below the Huffman lower bound.
	if got > huffmanBytes*0.6 {
		t.Errorf("encoded size %.0f bytes is not significantly below Huffman bound %.0f", got, huffmanBytes)
	}
}
