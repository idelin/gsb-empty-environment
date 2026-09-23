package huffcodec

import (
	"math/bits"
	"math/rand"
	"testing"
)

func TestEmptyInput(t *testing.T) {
	lengths, err := CodeLengths(make([]uint64, 256), 16)
	if err != nil {
		t.Fatalf("empty freqs: %v", err)
	}
	for i, l := range lengths {
		if l != 0 {
			t.Fatalf("symbol %d: want length 0, got %d", i, l)
		}
	}
}

func TestSingleSymbolGetsOneBitCode(t *testing.T) {
	var freqs [256]uint64
	freqs['x'] = 42
	lengths, err := CodeLengths(freqs[:], 16)
	if err != nil {
		t.Fatal(err)
	}
	if lengths['x'] != 1 {
		t.Fatalf("single symbol must get a 1-bit code, got %d", lengths['x'])
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatalf("single-symbol table must be usable: %v", err)
	}
	if codes['x'].Len != 1 {
		t.Fatalf("single symbol must not produce an empty codeword")
	}
}

func TestTieBreakByByteOrder(t *testing.T) {
	// Equal frequencies: byte order decides, lower symbol gets the
	// shorter-or-equal code, deterministically.
	var freqs [256]uint64
	freqs['c'], freqs['a'], freqs['b'] = 7, 7, 7
	lengths, err := CodeLengths(freqs[:], 16)
	if err != nil {
		t.Fatal(err)
	}
	if lengths['a'] != 1 || lengths['b'] != 2 || lengths['c'] != 2 {
		t.Fatalf("byte-order tie-break violated: a=%d b=%d c=%d",
			lengths['a'], lengths['b'], lengths['c'])
	}
	again, _ := CodeLengths(freqs[:], 16)
	for i := range lengths {
		if lengths[i] != again[i] {
			t.Fatalf("non-deterministic result at symbol %d", i)
		}
	}
}

func TestKraftCompleteForBuiltCodes(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		var freqs [256]uint64
		n := 2 + rng.Intn(40)
		for i := 0; i < n; i++ {
			freqs[rng.Intn(256)] = uint64(1 + rng.Intn(1000))
		}
		lengths, err := CodeLengths(freqs[:], MaxCodeLength)
		if err != nil {
			t.Fatal(err)
		}
		if k := KraftScaled(lengths); k != 1<<MaxCodeLength {
			t.Fatalf("Kraft sum = %d, want exactly 2^32", k)
		}
		if err := ValidateLengths(lengths); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaxCodeLengthEnforced(t *testing.T) {
	// Fibonacci weights force deep trees; the limit must hold exactly.
	var freqs [256]uint64
	a, b := uint64(1), uint64(1)
	for i := 0; i < 20; i++ {
		freqs[i] = a
		a, b = b, a+b
	}
	const limit = 10
	lengths, err := CodeLengths(freqs[:], limit)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if lengths[i] == 0 || lengths[i] > limit {
			t.Fatalf("symbol %d length %d outside [1,%d]", i, lengths[i], limit)
		}
	}
	if k := KraftScaled(lengths); k != 1<<MaxCodeLength {
		t.Fatalf("length-limited code not complete, Kraft=%d", k)
	}
}

func TestMaxBitsTooSmall(t *testing.T) {
	freqs := []uint64{1, 1, 1, 1, 1} // 5 symbols need at least 3 bits
	if _, err := CodeLengths(freqs, 2); err != ErrMaxBitsTooSmall {
		t.Fatalf("want ErrMaxBitsTooSmall, got %v", err)
	}
	if _, err := CodeLengths(freqs, 0); err != ErrMaxBitsTooSmall {
		t.Fatalf("want ErrMaxBitsTooSmall, got %v", err)
	}
}

func TestValidateLengths(t *testing.T) {
	cases := []struct {
		name    string
		lengths []uint8
		want    error
	}{
		{"oversubscribed", []uint8{1, 1, 1}, ErrOversubscribed},
		{"oversubscribed-2", []uint8{1, 2, 2, 2, 2}, ErrOversubscribed},
		{"incomplete", []uint8{2, 2}, ErrIncomplete},
		{"incomplete-hole", []uint8{1, 3, 3}, ErrIncomplete},
		{"empty", []uint8{0, 0, 0}, ErrIncomplete},
		{"complete", []uint8{1, 2, 3, 3}, nil},
		{"single-symbol", []uint8{1}, nil},
	}
	for _, c := range cases {
		if got := ValidateLengths(c.lengths); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCanonicalCodesAssignment(t *testing.T) {
	// Canonical order: by (length, symbol). lengths: sym0=1, sym1=2, sym2=3, sym3=3
	lengths := []uint8{1, 2, 3, 3}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	want := []Code{{0b0, 1}, {0b10, 2}, {0b110, 3}, {0b111, 3}}
	for i, w := range want {
		if codes[i] != w {
			t.Fatalf("symbol %d: got {%b,%d}, want {%b,%d}",
				i, codes[i].Bits, codes[i].Len, w.Bits, w.Len)
		}
	}
	if _, err := CanonicalCodes([]uint8{1, 1, 1}); err != ErrOversubscribed {
		t.Fatalf("oversubscribed table not rejected: %v", err)
	}
}

// bruteForceOptimalCost enumerates every legal length vector (Kraft sum
// exactly 1, every length in [1,maxBits]) and returns the minimum cost.
func bruteForceOptimalCost(weights []uint64, maxBits int) uint64 {
	n := len(weights)
	best := ^uint64(0)
	target := 1 << uint(maxBits)
	var rec func(i, kraft int, cost uint64)
	rec = func(i, kraft int, cost uint64) {
		if cost >= best {
			return
		}
		if i == n {
			if kraft == target {
				best = cost
			}
			return
		}
		rem := n - i
		for l := 1; l <= maxBits; l++ {
			add := 1 << uint(maxBits-l)
			k2 := kraft + add
			if k2 > target || k2+(rem-1) > target || k2+(rem-1)*(target>>1) < target {
				continue
			}
			rec(i+1, k2, cost+weights[i]*uint64(l))
		}
	}
	rec(0, 0, 0)
	return best
}

func TestOptimalityByEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(6) // 2..7 symbols
		minBits := bits.Len(uint(n - 1))
		maxBits := minBits + rng.Intn(3)
		if maxBits > 6 {
			maxBits = 6
		}
		freqs := make([]uint64, n)
		for i := range freqs {
			freqs[i] = uint64(1 + rng.Intn(60))
		}
		lengths, err := CodeLengths(freqs, maxBits)
		if err != nil {
			t.Fatalf("n=%d maxBits=%d: %v", n, maxBits, err)
		}
		for i, l := range lengths {
			if l < 1 || int(l) > maxBits {
				t.Fatalf("n=%d maxBits=%d: bad length %d at %d", n, maxBits, l, i)
			}
		}
		if k := KraftScaled(lengths); k != 1<<MaxCodeLength {
			t.Fatalf("n=%d maxBits=%d: incomplete code", n, maxBits)
		}
		got := Cost(freqs, lengths)
		want := bruteForceOptimalCost(freqs, maxBits)
		if got != want {
			t.Fatalf("n=%d maxBits=%d freqs=%v: cost %d, optimal %d",
				n, maxBits, freqs, got, want)
		}
	}
}

func TestLengthLimitedMatchesBruteForce(t *testing.T) {
	// Tight length limit on skewed (Fibonacci) weights, verified exactly.
	freqs := []uint64{1, 1, 2, 3, 5, 8, 13, 21}
	lengths, err := CodeLengths(freqs, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Cost(freqs, lengths), bruteForceOptimalCost(freqs, 4); got != want {
		t.Fatalf("length-limited cost %d, optimal %d", got, want)
	}
}
