package prefixcode

import (
	"math/rand"
	"testing"
)

// 2^L < n must be refused, never silently truncated.
func TestInfeasibleRejected(t *testing.T) {
	freqs := []uint64{1, 1, 1, 1, 1}                         // 5 symbols
	if _, err := BuildCode(freqs, 2); err != ErrInfeasible { // 2^2 = 4 < 5
		t.Fatalf("want ErrInfeasible, got %v", err)
	}
	if _, err := BuildCode(freqs, 1); err != ErrInfeasible {
		t.Fatalf("want ErrInfeasible, got %v", err)
	}
}

// Boundary: n == 2^L is feasible and forces a complete code.
func TestBoundaryFeasible(t *testing.T) {
	freqs := []uint64{1, 1, 1, 1}
	code, err := BuildCode(freqs, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range code.Lengths {
		if l != 2 {
			t.Fatalf("symbol %d: want length 2, got %d", i, l)
		}
	}
	num, complete := code.Kraft()
	if !complete || num != 4 {
		t.Fatalf("want complete Kraft sum 4/4, got %d/4 complete=%v", num, complete)
	}
}

// Classic Huffman example: optimal weighted cost is 224 bits.
func TestClassicHuffmanCost(t *testing.T) {
	freqs := []uint64{5, 9, 12, 13, 16, 45}
	code, err := BuildCode(freqs, 6)
	if err != nil {
		t.Fatal(err)
	}
	total, _ := code.Cost(freqs)
	if total != 224 {
		t.Fatalf("want cost 224, got %d", total)
	}
	if _, complete := code.Kraft(); !complete {
		t.Fatal("want complete code")
	}
}

// Tight limit: 8 skewed symbols under L=3 must all get length 3.
func TestTightLimitUniformLengths(t *testing.T) {
	freqs := []uint64{1, 1, 2, 4, 8, 16, 32, 64}
	code, err := BuildCode(freqs, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range code.Lengths {
		if l != 3 {
			t.Fatalf("symbol %d: want length 3, got %d", i, l)
		}
	}
	total, _ := code.Cost(freqs)
	if total != 3*128 {
		t.Fatalf("want cost %d, got %d", 3*128, total)
	}
}

// Loosening the limit must never increase cost.
func TestCostMonotoneInLimit(t *testing.T) {
	freqs := []uint64{1, 1, 2, 3, 5, 8, 13, 21, 34, 55}
	prev := ^uint64(0)
	for L := 4; L <= 10; L++ {
		code, err := BuildCode(freqs, L)
		if err != nil {
			t.Fatal(err)
		}
		cost, _ := code.Cost(freqs)
		if cost > prev {
			t.Fatalf("L=%d: cost %d > previous %d", L, cost, prev)
		}
		prev = cost
	}
}

// Property test on random instances: lengths within limit, Kraft <= 1,
// prefix-free round trip through the binary format.
func TestRandomInstances(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		L := 1 + rng.Intn(10)
		n := 1 + rng.Intn(1<<uint(L))
		freqs := make([]uint64, n)
		for i := range freqs {
			freqs[i] = uint64(rng.Intn(1000))
		}
		code, err := BuildCode(freqs, L)
		if err != nil {
			t.Fatalf("trial %d (n=%d L=%d): %v", trial, n, L, err)
		}
		for i, l := range code.Lengths {
			if l < 1 || int(l) > L {
				t.Fatalf("trial %d: symbol %d length %d outside [1,%d]", trial, i, l, L)
			}
		}
		if num, _ := code.Kraft(); num > uint64(1)<<uint(L) {
			t.Fatalf("trial %d: Kraft sum %d/2^%d > 1", trial, num, L)
		}
		// Round-trip a random message.
		m := rng.Intn(200)
		msg := make([]int, m)
		for i := range msg {
			msg[i] = rng.Intn(n)
		}
		blob, err := Encode(code, msg)
		if err != nil {
			t.Fatalf("trial %d: encode: %v", trial, err)
		}
		decCode, dec, err := Decode(blob, -1)
		if err != nil {
			t.Fatalf("trial %d: decode: %v", trial, err)
		}
		if !equalUint8(code.Lengths, decCode.Lengths) {
			t.Fatalf("trial %d: code table mismatch", trial)
		}
		if !equalInt(msg, dec) {
			t.Fatalf("trial %d: round-trip mismatch", trial)
		}
	}
}

func TestSingleSymbol(t *testing.T) {
	code, err := BuildCode([]uint64{7}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if code.Lengths[0] != 1 {
		t.Fatalf("want length 1, got %d", code.Lengths[0])
	}
	blob, err := Encode(code, []int{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	_, dec, err := Decode(blob, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt(dec, []int{0, 0, 0}) {
		t.Fatalf("got %v", dec)
	}
}

func TestEmptyRejected(t *testing.T) {
	if _, err := BuildCode(nil, 4); err == nil {
		t.Fatal("want error for zero symbols")
	}
}

func TestLengthValidation(t *testing.T) {
	// Length exceeding the limit.
	if _, err := NewFromLengths([]uint8{1, 3}, 2); err == nil {
		t.Fatal("want error for length > maxLen")
	}
	// Kraft violation: three length-1 codes need 3/2 > 1.
	if _, err := NewFromLengths([]uint8{1, 1, 1}, 2); err == nil {
		t.Fatal("want Kraft violation error")
	}
	// Zero length.
	if _, err := NewFromLengths([]uint8{1, 0}, 2); err == nil {
		t.Fatal("want error for zero length")
	}
}

func TestCorruptionDetected(t *testing.T) {
	freqs := []uint64{5, 9, 12, 13, 16, 45}
	code, _ := BuildCode(freqs, 6)
	blob, err := Encode(code, []int{0, 1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in every position of the payload region; each must fail.
	for i := 0; i < len(blob); i++ {
		bad := append([]byte(nil), blob...)
		bad[i] ^= 0x01
		if _, _, err := Decode(bad, -1); err == nil {
			t.Fatalf("corruption at byte %d not detected", i)
		}
	}
}

func TestTruncationDetected(t *testing.T) {
	freqs := []uint64{5, 9, 12, 13, 16, 45}
	code, _ := BuildCode(freqs, 6)
	blob, _ := Encode(code, []int{0, 1, 2, 3})
	for _, cut := range []int{0, 3, 8, len(blob) - 1} {
		if _, _, err := Decode(blob[:cut], -1); err == nil {
			t.Fatalf("truncation to %d bytes not detected", cut)
		}
	}
}

func TestBadVersionRejected(t *testing.T) {
	freqs := []uint64{1, 1}
	code, _ := BuildCode(freqs, 2)
	blob, _ := Encode(code, []int{0})
	blob[3] = '9' // bump version nibble of magic
	if _, _, err := Decode(blob, -1); err == nil {
		t.Fatal("want unsupported version error")
	}
}

func TestDecodeOutputBound(t *testing.T) {
	freqs := []uint64{5, 9, 12, 13, 16, 45}
	code, _ := BuildCode(freqs, 6)
	msg := []int{0, 1, 2, 3, 4, 5, 5, 5}
	blob, _ := Encode(code, msg)
	// Exact bound succeeds.
	if _, dec, err := Decode(blob, len(msg)); err != nil || !equalInt(dec, msg) {
		t.Fatalf("exact bound: err=%v", err)
	}
	// One less must fail.
	if _, _, err := Decode(blob, len(msg)-1); err == nil {
		t.Fatal("want output bound error")
	}
	// Early rejection: payloadBits > maxSymbols*maxLen.
	if _, _, err := Decode(blob, 0); err == nil {
		t.Fatal("want early bound rejection")
	}
}

func TestCostAndKraftReported(t *testing.T) {
	freqs := []uint64{5, 9, 12, 13, 16, 45}
	code, _ := BuildCode(freqs, 4)
	num, complete := code.Kraft()
	if !complete || num != 16 {
		t.Fatalf("want Kraft 16/16 complete, got %d complete=%v", num, complete)
	}
	total, avg := code.Cost(freqs)
	if total != 224 {
		t.Fatalf("want cost 224, got %d", total)
	}
	if avg < 2.2 || avg > 2.3 {
		t.Fatalf("want avg ~2.24, got %f", avg)
	}
}

func equalUint8(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
