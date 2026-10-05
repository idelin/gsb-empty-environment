package arith

import (
	"bytes"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// splitFrame extracts the structural sections of a frame for inspection.
func splitFrame(t *testing.T, frame []byte) (n int, labels []string, seq []uint32, payload []byte) {
	t.Helper()
	c := &cursor{data: frame}
	if _, err := c.bytes(4); err != nil {
		t.Fatal(err)
	}
	nv, err := c.uvarint()
	if err != nil {
		t.Fatal(err)
	}
	n = int(nv)
	kv, err := c.uvarint()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(kv); i++ {
		l, err := c.uvarint()
		if err != nil {
			t.Fatal(err)
		}
		b, err := c.bytes(l)
		if err != nil {
			t.Fatal(err)
		}
		labels = append(labels, string(b))
	}
	seq = make([]uint32, n)
	if len(labels) > 1 { // single-context frames store no label sequence
		for i := range seq {
			v, err := c.uvarint()
			if err != nil {
				t.Fatal(err)
			}
			seq[i] = uint32(v)
		}
	}
	p, err := c.uvarint()
	if err != nil {
		t.Fatal(err)
	}
	payload, err = c.bytes(p)
	if err != nil {
		t.Fatal(err)
	}
	return n, labels, seq, payload
}

func roundTrip(t *testing.T, labels []string, bits []byte) []byte {
	t.Helper()
	frame, err := EncodeFrame(labels, bits)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	gotLabels, gotBits, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if !bytes.Equal(gotBits, bits) {
		t.Fatalf("bits mismatch: got %d bits, want %d", len(gotBits), len(bits))
	}
	if len(gotLabels) != len(labels) {
		t.Fatalf("labels mismatch: got %d, want %d", len(gotLabels), len(labels))
	}
	for i := range labels {
		if gotLabels[i] != labels[i] {
			t.Fatalf("label %d: got %q, want %q", i, gotLabels[i], labels[i])
		}
	}
	return frame
}

func TestRoundTripAlternatingContexts(t *testing.T) {
	// Two contexts, each fed a strongly biased but opposite stream.
	var labels []string
	var bits []byte
	for i := 0; i < 5000; i++ {
		if i%2 == 0 {
			labels = append(labels, "even")
			bits = append(bits, 0)
		} else {
			labels = append(labels, "odd")
			bits = append(bits, 1)
		}
	}
	frame := roundTrip(t, labels, bits)
	t.Logf("alternating contexts: %d bits -> %d bytes frame", len(bits), len(frame))
}

func TestRoundTripSkewed(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := 100000
	labels := make([]string, n)
	bits := make([]byte, n)
	for i := range bits {
		labels[i] = "skew"
		if rng.Intn(1000) == 0 { // p(1) = 0.001
			bits[i] = 1
		}
	}
	frame := roundTrip(t, labels, bits)
	t.Logf("skewed p(1)=0.001: %d bits -> %d bytes frame (%.5f bits/bit)",
		n, len(frame), float64(len(frame))*8/float64(n))
}

func TestRescaleBoundary(t *testing.T) {
	e := NewEncoder()
	// 2 zeros then 4092 ones: counts reach (3, 4093), sum 4096 -> rescale
	// to ceil halves (2, 2047).
	if err := e.Encode("c", []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := e.Encode("c", bytes.Repeat([]byte{1}, 4092)); err != nil {
		t.Fatal(err)
	}
	if got := e.ctxs[0]; got.c0 != 2 || got.c1 != 2047 {
		t.Fatalf("after rescale: got (%d,%d), want (2,2047)", got.c0, got.c1)
	}
	// 4094 zeros in a fresh context: (4095, 1) -> rescale -> (2048, 1).
	if err := e.Encode("d", bytes.Repeat([]byte{0}, 4094)); err != nil {
		t.Fatal(err)
	}
	if got := e.ctxs[1]; got.c0 != 2048 || got.c1 != 1 {
		t.Fatalf("after rescale: got (%d,%d), want (2048,1)", got.c0, got.c1)
	}
	// Rescale must be reproducible through a full round trip.
	var labels []string
	var bits []byte
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 60000; i++ {
		labels = append(labels, "hot")
		b := byte(0)
		if rng.Intn(8) == 0 {
			b = 1
		}
		bits = append(bits, b)
	}
	roundTrip(t, labels, bits) // crosses the 4096 threshold many times
}

func TestEmptyInput(t *testing.T) {
	frame, err := EncodeFrame(nil, nil)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	labels, bits, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if len(bits) != 0 || len(labels) != 0 {
		t.Fatalf("expected empty, got %d bits %d labels", len(bits), len(labels))
	}
	t.Logf("empty input: frame is %d bytes", len(frame))
}

// TestRationalReference checks the integer coder against an exact
// arbitrary-precision rational interval model:
//  1. the emitted code value lies inside the exact interval;
//  2. the code length matches the exact interval width (near-optimality);
//  3. an infinite-precision decoder over the exact interval recovers the
//     original bits from the emitted code value alone.
func TestRationalReference(t *testing.T) {
	labels := []string{"a", "b", "a", "a", "b", "b", "a", "b", "a", "a", "b", "a", "b", "b"}
	bits := []byte{0, 1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 1, 0, 1}
	frame, err := EncodeFrame(labels, bits)
	if err != nil {
		t.Fatal(err)
	}
	n, frameLabels, seq, payload := splitFrame(t, frame)
	if n != len(bits) {
		t.Fatalf("frame n=%d, want %d", n, len(bits))
	}

	// Exact interval after coding the whole sequence.
	type counts struct{ c0, c1 int64 }
	model := map[string]*counts{}
	get := func(l string) *counts {
		c, ok := model[l]
		if !ok {
			c = &counts{1, 1}
			model[l] = c
		}
		return c
	}
	update := func(c *counts, b byte) {
		if b == 0 {
			c.c0++
		} else {
			c.c1++
		}
		if c.c0+c.c1 >= maxTotal {
			c.c0 = (c.c0 + 1) / 2
			c.c1 = (c.c1 + 1) / 2
		}
	}
	low, high := big.NewRat(0, 1), big.NewRat(1, 1)
	for i, b := range bits {
		c := get(labels[i])
		tot := c.c0 + c.c1
		r := new(big.Rat).Sub(high, low)
		boundary := new(big.Rat).Add(low, new(big.Rat).Mul(r, big.NewRat(c.c0, tot)))
		prevLow, prevHigh := new(big.Rat).Set(low), new(big.Rat).Set(high)
		if b == 0 {
			high = boundary
		} else {
			low = boundary
		}
		// Interval must nest: new [low, high) within previous.
		if low.Cmp(prevLow) < 0 || high.Cmp(prevHigh) > 0 || low.Cmp(high) >= 0 {
			t.Fatalf("step %d: interval not nested", i)
		}
		update(c, b)
	}

	// 1. The emitted code value (payload as a binary fraction, zero-padded
	// exactly as the decoder reads it) must lie inside the exact interval.
	L := uint(len(payload)) * 8
	num := new(big.Int).SetBytes(payload)
	den := new(big.Int).Lsh(big.NewInt(1), L)
	code := new(big.Rat).SetFrac(num, den)
	if code.Cmp(low) < 0 || code.Cmp(high) >= 0 {
		t.Fatalf("code value %v outside exact interval [%v, %v)", code, low, high)
	}

	// 2. Code length vs exact width: L ~ -log2(width); allow termination
	// and byte padding slack.
	width := new(big.Rat).Sub(high, low)
	scaled := new(big.Rat).Mul(width, new(big.Rat).SetFrac(den, big.NewInt(1)))
	if scaled.Cmp(big.NewRat(1, 4)) < 0 || scaled.Cmp(big.NewRat(1024, 1)) > 0 {
		t.Fatalf("code length %d bits inconsistent with exact width %v", L, width)
	}
	t.Logf("exact interval width 2^-%.3f, code %d bits",
		-math.Log2(ratFloat(width)), L)

	// 3. Infinite-precision reference decode of the code value.
	refModel := map[string]*counts{}
	refGet := func(l string) *counts {
		c, ok := refModel[l]
		if !ok {
			c = &counts{1, 1}
			refModel[l] = c
		}
		return c
	}
	rl, rh := big.NewRat(0, 1), big.NewRat(1, 1)
	for i := 0; i < n; i++ {
		c := refGet(frameLabels[seq[i]])
		tot := c.c0 + c.c1
		// cum = floor((code - rl) * tot / (rh - rl))
		q := new(big.Rat).Sub(code, rl)
		q.Mul(q, big.NewRat(tot, 1))
		q.Quo(q, new(big.Rat).Sub(rh, rl))
		cum := new(big.Int).Quo(q.Num(), q.Denom()).Int64()
		r := new(big.Rat).Sub(rh, rl)
		boundary := new(big.Rat).Add(rl, new(big.Rat).Mul(r, big.NewRat(c.c0, tot)))
		var b byte
		if cum < c.c0 {
			b = 0
			rh = boundary
		} else {
			b = 1
			rl = boundary
		}
		if b != bits[i] {
			t.Fatalf("reference decode bit %d: got %d, want %d", i, b, bits[i])
		}
		update(c, b)
	}
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

func TestLongRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	n := 200000
	ctxNames := []string{"alpha", "beta", "gamma", "delta"}
	// Per-context bias: probability of a 1 bit.
	bias := []int{1, 20, 50, 90} // percent
	labels := make([]string, n)
	bits := make([]byte, n)
	for i := 0; i < n; i++ {
		c := rng.Intn(len(ctxNames))
		labels[i] = ctxNames[c]
		if rng.Intn(100) < bias[c] {
			bits[i] = 1
		}
	}
	frame := roundTrip(t, labels, bits)
	_, _, _, payload := splitFrame(t, frame)
	raw := (n + 7) / 8
	t.Logf("long mixed-context stream: %d bits (%d raw bytes) -> payload %d bytes, frame %d bytes (incl. %d label-index bytes)",
		n, raw, len(payload), len(frame), len(frame)-len(payload)-30)
}

func TestCompressionReport(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	n := 100000
	labels := make([]string, n)
	bits := make([]byte, n)
	for i := range bits {
		labels[i] = "p001"
		if rng.Intn(100) < 1 {
			bits[i] = 1
		}
	}
	frame := roundTrip(t, labels, bits)
	_, _, _, payload := splitFrame(t, frame)
	raw := (n + 7) / 8
	ratio := float64(len(frame)) / float64(raw)
	t.Logf("p(1)=0.01, %d bits: raw %d bytes, payload %d bytes, frame %d bytes, ratio %.4f (entropy bound ~0.081 bits/bit)",
		n, raw, len(payload), len(frame), ratio)
	if ratio > 0.25 {
		t.Fatalf("suspiciously poor compression: ratio %.4f", ratio)
	}
}
