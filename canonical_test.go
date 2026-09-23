package prefixcode

import (
	"math/big"
	"testing"
)

func TestValidateOversubscribed(t *testing.T) {
	lengths := []int{1, 1, 1} // Kraft = 3/2 > 1
	if err := Validate(lengths, 3); err != ErrOversubscribed {
		t.Fatalf("want ErrOversubscribed, got %v", err)
	}
	if _, err := NewTable(lengths); err != ErrOversubscribed {
		t.Fatalf("NewTable: want ErrOversubscribed, got %v", err)
	}
}

func TestValidateIncomplete(t *testing.T) {
	lengths := []int{2, 2} // Kraft = 1/2 < 1
	if err := Validate(lengths, 2); err != ErrIncomplete {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
	// Incomplete tables may still be built; missing patterns fail at decode.
	if _, err := NewTable(lengths); err != nil {
		t.Fatalf("NewTable should accept incomplete table: %v", err)
	}
}

func TestValidateComplete(t *testing.T) {
	if err := Validate([]int{1, 2, 3, 3}, 3); err != nil {
		t.Fatalf("complete table rejected: %v", err)
	}
}

func TestCanonicalAssignment(t *testing.T) {
	lengths := make([]int, 256)
	lengths['a'] = 1
	lengths['b'] = 2
	lengths['c'] = 3
	lengths['d'] = 3
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	want := map[byte]Code{
		'a': {Bits: 0, Len: 1}, // 0
		'b': {Bits: 2, Len: 2}, // 10
		'c': {Bits: 6, Len: 3}, // 110
		'd': {Bits: 7, Len: 3}, // 111
	}
	for sym, w := range want {
		if codes[sym] != w {
			t.Errorf("sym %q: got %+v want %+v", sym, codes[sym], w)
		}
	}
}

func TestKraftExact(t *testing.T) {
	one := big.NewRat(1, 1)
	if k := Kraft([]int{1, 2, 3, 3}); k.Cmp(one) != 0 {
		t.Fatalf("Kraft({1,2,3,3}) = %s, want 1", k.RatString())
	}
	half := big.NewRat(1, 2)
	if k := Kraft([]int{2, 2}); k.Cmp(half) != 0 {
		t.Fatalf("Kraft({2,2}) = %s, want 1/2", k.RatString())
	}
	if k := Kraft([]int{1, 1, 1}); k.Cmp(one) <= 0 {
		t.Fatalf("Kraft({1,1,1}) = %s, want > 1", k.RatString())
	}
}

func TestEntropyAndCostReference(t *testing.T) {
	freqs := []uint64{1, 1, 1, 1}
	if h := Entropy(freqs); h != 2.0 {
		t.Fatalf("entropy of uniform 4 symbols = %v, want 2", h)
	}
	lengths, err := BuildLengths(freqs, 4)
	if err != nil {
		t.Fatal(err)
	}
	if c := Cost(freqs, lengths); c != 8 {
		t.Fatalf("uniform 4 symbols: cost = %d bits, want 8", c)
	}
}
