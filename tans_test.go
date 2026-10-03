package tans

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

// skewed returns n bytes drawn from a highly skewed distribution:
// p(symbol i) is proportional to 0.8^i over 32 symbols.
func skewed(rng *rand.Rand, n int) []byte {
	var weights [32]float64
	sum := 0.0
	for i := range weights {
		weights[i] = math.Pow(0.8, float64(i))
		sum += weights[i]
	}
	out := make([]byte, n)
	for i := range out {
		x := rng.Float64() * sum
		acc := 0.0
		s := 0
		for ; s < len(weights)-1; s++ {
			acc += weights[s]
			if x < acc {
				break
			}
		}
		out[i] = byte(s)
	}
	return out
}

func freqOf(data []byte) (freq [256]uint64) {
	for _, b := range data {
		freq[b]++
	}
	return freq
}

func roundTrip(t *testing.T, data []byte, tableLog int) {
	t.Helper()
	compressed := Compress(data, tableLog)
	decoded, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if len(data) == 0 {
		if len(decoded) != 0 {
			t.Fatalf("expected empty output, got %d bytes", len(decoded))
		}
		return
	}
	if !bytes.Equal(decoded, data) {
		t.Fatalf("round trip mismatch (input %d bytes, tableLog %d)", len(data), tableLog)
	}
}

func TestRoundTripBasic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte{42}},
		{"single symbol", bytes.Repeat([]byte{7}, 10000)},
		{"two symbols alternating", func() []byte {
			b := make([]byte, 10000)
			for i := range b {
				b[i] = byte(i % 2)
			}
			return b
		}()},
		{"text", bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 500)},
		{"skewed 100k", skewed(rng, 100000)},
		{"random 100k", func() []byte {
			b := make([]byte, 100000)
			rng.Read(b)
			return b
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, tableLog := range []int{MinTableLog, 8, DefaultTableLog, MaxTableLog} {
				roundTrip(t, tc.data, tableLog)
			}
		})
	}
}

// TestRoundTripRandomDistributions exercises the state machine with random
// alphabets, distributions, table sizes and lengths.
func TestRoundTripRandomDistributions(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 200; iter++ {
		nSym := 1 + rng.Intn(256)
		syms := rng.Perm(256)[:nSym]
		weights := make([]float64, nSym)
		sum := 0.0
		for i := range weights {
			weights[i] = 0.01 + rng.Float64()*rng.Float64()*20
			sum += weights[i]
		}
		n := rng.Intn(5000)
		data := make([]byte, n)
		for i := range data {
			x := rng.Float64() * sum
			acc := 0.0
			s := 0
			for ; s < nSym-1; s++ {
				acc += weights[s]
				if x < acc {
					break
				}
			}
			data[i] = byte(syms[s])
		}
		tableLog := MinTableLog + rng.Intn(MaxTableLog-MinTableLog+1)
		roundTrip(t, data, tableLog)
	}
}

// TestStreamingPushPop drives the Encoder/Decoder directly (no container
// header) and verifies the state machine returns to its initial state.
func TestStreamingPushPop(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := skewed(rng, 50000)
	freq := freqOf(data)
	tab := BuildTable(freq[:], 10)

	enc := NewEncoder(tab)
	for i := len(data) - 1; i >= 0; i-- {
		enc.Encode(data[i])
	}
	payload, pad := enc.Finish()

	dec := NewDecoder(tab, payload, pad)
	for i := range data {
		if got := dec.Decode(); got != data[i] {
			t.Fatalf("symbol %d: got %d, want %d", i, got, data[i])
		}
	}
	if dec.state != 0 {
		t.Fatalf("final decoder state = %d, want 0 (initial encoder state)", dec.state)
	}
	if dec.br.pos != 0 || dec.br.nbits != 0 {
		t.Fatalf("bit stream not fully consumed: pos=%d nbits=%d", dec.br.pos, dec.br.nbits)
	}
}

// TestCompressionRatioNearEntropy checks that the compressed size of a
// highly skewed sample stays within a small margin of the theoretical
// entropy bound, and matches the arithmetic-coding baseline.
func TestCompressionRatioNearEntropy(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	data := skewed(rng, 1<<18)
	freq := freqOf(data)
	entropy := EntropyBits(freq[:])

	compressed := Compress(data, DefaultTableLog)
	got := float64(len(compressed)) * 8
	t.Logf("entropy bound: %.0f bits, tANS: %.0f bits (%.2f%% overhead, header included)",
		entropy, got, 100*(got-entropy)/entropy)
	if got > entropy*1.02+1024 {
		t.Fatalf("compressed size %.0f bits exceeds entropy bound %.0f bits by more than 2%%", got, entropy)
	}
	if got < entropy*0.98 {
		t.Fatalf("compressed size %.0f bits suspiciously below entropy bound %.0f bits", got, entropy)
	}

	// Must be on par with the arithmetic-coding baseline.
	ac := ArithCompress(data, &freq)
	t.Logf("arithmetic coding payload: %d bytes, tANS total: %d bytes", len(ac), len(compressed))
	if float64(len(compressed)) > float64(len(ac))*1.01+128 {
		t.Fatalf("tANS (%d bytes) noticeably worse than arithmetic coding (%d bytes)", len(compressed), len(ac))
	}
}

func TestArithReferenceRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, data := range [][]byte{
		skewed(rng, 30000),
		bytes.Repeat([]byte{9}, 1000),
		func() []byte { b := make([]byte, 30000); rng.Read(b); return b }(),
	} {
		freq := freqOf(data)
		compressed := ArithCompress(data, &freq)
		decoded := ArithDecompress(compressed, len(data), &freq)
		if !bytes.Equal(decoded, data) {
			t.Fatalf("arithmetic reference round trip mismatch (%d bytes)", len(data))
		}
	}
}

func TestDecompressCorrupt(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	good := Compress(skewed(rng, 1000), DefaultTableLog)

	if _, err := Decompress(nil); err == nil {
		t.Fatal("expected error for nil input")
	}
	if _, err := Decompress([]byte("not a tans stream at all")); err == nil {
		t.Fatal("expected error for bad magic")
	}
	if _, err := Decompress(good[:len(good)/2]); err == nil {
		t.Fatal("expected error for truncated stream")
	}
	// Bit corruptions must never panic (output may be garbage).
	for i := 0; i < 200; i++ {
		buf := append([]byte{}, good...)
		buf[rand.New(rand.NewSource(int64(i))).Intn(len(buf))] ^= 1 << uint(i%8)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on corrupted input: %v", r)
				}
			}()
			Decompress(buf)
		}()
	}
}

// ---- Benchmarks: tANS vs traditional arithmetic coding ----

func benchData() ([]byte, [256]uint64) {
	rng := rand.New(rand.NewSource(7))
	data := skewed(rng, 1<<20)
	return data, freqOf(data)
}

func BenchmarkTANSEncode(b *testing.B) {
	data, freq := benchData()
	tab := BuildTable(freq[:], DefaultTableLog)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc := NewEncoder(tab)
		for j := len(data) - 1; j >= 0; j-- {
			enc.Encode(data[j])
		}
		_, _ = enc.Finish()
	}
}

func BenchmarkTANSDecode(b *testing.B) {
	data, freq := benchData()
	tab := BuildTable(freq[:], DefaultTableLog)
	enc := NewEncoder(tab)
	for j := len(data) - 1; j >= 0; j-- {
		enc.Encode(data[j])
	}
	payload, pad := enc.Finish()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec := NewDecoder(tab, payload, pad)
		for j := 0; j < len(data); j++ {
			_ = dec.Decode()
		}
	}
}

func BenchmarkArithEncode(b *testing.B) {
	data, freq := benchData()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ArithCompress(data, &freq)
	}
}

func BenchmarkArithDecode(b *testing.B) {
	data, freq := benchData()
	compressed := ArithCompress(data, &freq)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ArithDecompress(compressed, len(data), &freq)
	}
}
