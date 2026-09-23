package prefixcode

import (
	"math/rand"
	"testing"
)

func TestBuildLengthsEmpty(t *testing.T) {
	lengths, err := BuildLengths(make([]uint64, 256), 16)
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	for i, l := range lengths {
		if l != 0 {
			t.Fatalf("symbol %d: want length 0, got %d", i, l)
		}
	}
}

func TestBuildLengthsSingleSymbol(t *testing.T) {
	freqs := make([]uint64, 256)
	freqs['A'] = 42
	lengths, err := BuildLengths(freqs, 16)
	if err != nil {
		t.Fatal(err)
	}
	if lengths['A'] != 1 {
		t.Fatalf("single symbol must get a 1-bit code, got %d", lengths['A'])
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	if codes['A'].Len != 1 {
		t.Fatalf("single symbol must not produce an empty code: %+v", codes['A'])
	}
	// Round-trip a run of the single symbol.
	msg := []byte("AAAAAAA")
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	if len(stream) != 1 {
		t.Fatalf("7 one-bit symbols should pack into 1 byte, got %d", len(stream))
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeAll(t, tbl, stream, len(msg), len(stream))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(msg) {
		t.Fatalf("got %q want %q", out, msg)
	}
}

func TestEqualFreqTieBreakByByteOrder(t *testing.T) {
	// Three equal-frequency symbols: optimal lengths are {1,2,2}; the
	// shortest code must go to the smallest byte value.
	freqs := make([]uint64, 256)
	freqs[200] = 7
	freqs[10] = 7
	freqs[100] = 7
	lengths, err := BuildLengths(freqs, 8)
	if err != nil {
		t.Fatal(err)
	}
	if lengths[10] != 1 || lengths[100] != 2 || lengths[200] != 2 {
		t.Fatalf("tie-break by byte order violated: 10->%d 100->%d 200->%d",
			lengths[10], lengths[100], lengths[200])
	}
}

func TestTieBreakPropertyRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		var freqs [256]uint64
		n := 2 + rng.Intn(30)
		for i := 0; i < n; i++ {
			freqs[rng.Intn(256)] = uint64(rng.Intn(4) + 1) // many ties
		}
		lengths, err := BuildLengths(freqs[:], 16)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 256; i++ {
			for j := i + 1; j < 256; j++ {
				if freqs[i] == freqs[j] && freqs[i] > 0 && lengths[i] > lengths[j] {
					t.Fatalf("tie-break violated: sym %d len %d, sym %d len %d (equal freq)",
						i, lengths[i], j, lengths[j])
				}
			}
		}
	}
}

func TestMaxLenEnforced(t *testing.T) {
	// Fibonacci-like weights force very long codes when unconstrained.
	freqs := []uint64{1, 1, 2, 3, 5, 8, 13, 21, 34, 55}
	unconstrained, err := BuildLengths(freqs, 16)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := BuildLengths(freqs, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range limited {
		if l > 4 {
			t.Fatalf("symbol %d length %d exceeds maxLen 4", i, l)
		}
	}
	if err := Validate(limited, 4); err != nil {
		t.Fatalf("limited lengths must be complete: %v", err)
	}
	if Cost(freqs, limited) < Cost(freqs, unconstrained) {
		t.Fatal("length-limited cost below unconstrained optimum: impossible")
	}
}

func TestMaxLenTooSmall(t *testing.T) {
	freqs := []uint64{1, 1, 1, 1, 1} // 5 symbols need at least 3 bits
	if _, err := BuildLengths(freqs, 2); err == nil {
		t.Fatal("want error for impossible maxLen")
	}
	if _, err := BuildLengths(freqs, 0); err == nil {
		t.Fatal("want error for maxLen 0")
	}
	if _, err := BuildLengths(freqs, MaxMaxLen+1); err == nil {
		t.Fatal("want error for maxLen above MaxMaxLen")
	}
}

func TestBuildLengthsDeterministic(t *testing.T) {
	var freqs [256]uint64
	rng := rand.New(rand.NewSource(99))
	for i := range freqs {
		freqs[i] = uint64(rng.Intn(1000))
	}
	a, err := BuildLengths(freqs[:], 20)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildLengths(freqs[:], 20)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic: symbol %d: %d vs %d", i, a[i], b[i])
		}
	}
}

func TestKraftCompleteForBuiltLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 200; trial++ {
		var freqs [256]uint64
		n := 2 + rng.Intn(200)
		for i := 0; i < n; i++ {
			freqs[rng.Intn(256)] += uint64(rng.Intn(100) + 1)
		}
		lengths, err := BuildLengths(freqs[:], 24)
		if err != nil {
			t.Fatal(err)
		}
		k := Kraft(lengths)
		if k.Num().Cmp(k.Denom()) != 0 {
			t.Fatalf("built lengths not complete: Kraft = %s", k.RatString())
		}
	}
}
