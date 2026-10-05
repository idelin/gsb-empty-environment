package bigsqrt

import (
	"math/big"
	"strings"
	"testing"
)

func TestStreamChunks(t *testing.T) {
	s := NewStream(10)
	for _, c := range []string{"123", "4567", "89", "0"} {
		if err := s.Feed(c); err != nil {
			t.Fatalf("Feed(%q): %v", c, err)
		}
	}
	if s.Digits() != 10 {
		t.Fatalf("Digits() = %d", s.Digits())
	}
	n, err := s.Int()
	if err != nil {
		t.Fatal(err)
	}
	if n.Cmp(big.NewInt(1234567890)) != 0 {
		t.Fatalf("got %s", n)
	}
}

func TestStreamLimitSeals(t *testing.T) {
	s := NewStream(4)
	if err := s.Feed("12"); err != nil {
		t.Fatal(err)
	}
	if err := s.Feed("345"); err != ErrDigitLimit {
		t.Fatalf("want ErrDigitLimit, got %v", err)
	}
	if !s.Rejected() {
		t.Fatal("stream should be sealed")
	}
	// Further feeds fail without growing the buffer, even tiny ones.
	for _, c := range []string{"3", "9999999999"} {
		if err := s.Feed(c); err != ErrDigitLimit {
			t.Fatalf("sealed stream accepted %q", c)
		}
	}
	if s.Digits() != 2 {
		t.Fatalf("buffer grew after limit breach: %d digits", s.Digits())
	}
	n, err := s.Int()
	if err != nil {
		t.Fatal(err)
	}
	if n.Cmp(big.NewInt(12)) != 0 {
		t.Fatalf("got %s", n)
	}
}

func TestStreamExactLimit(t *testing.T) {
	s := NewStream(3)
	if err := s.Feed("999"); err != nil {
		t.Fatalf("exact-limit chunk rejected: %v", err)
	}
	if err := s.Feed(""); err != nil {
		t.Fatalf("empty chunk rejected: %v", err)
	}
	if err := s.Feed("0"); err != ErrDigitLimit {
		t.Fatalf("want ErrDigitLimit, got %v", err)
	}
}

func TestStreamInvalidDigit(t *testing.T) {
	s := NewStream(100)
	if err := s.Feed("12a3"); err == nil {
		t.Fatal("accepted non-digit input")
	}
	if s.Digits() != 0 {
		t.Fatal("invalid chunk partially stored")
	}
	if err := s.Feed(" 12"); err == nil {
		t.Fatal("accepted whitespace")
	}
	if err := s.Feed("-5"); err == nil {
		t.Fatal("accepted sign")
	}
}

func TestStreamLeadingZeros(t *testing.T) {
	s := NewStream(10)
	if err := s.Feed("000123"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Int()
	if err != nil {
		t.Fatal(err)
	}
	if n.Cmp(big.NewInt(123)) != 0 {
		t.Fatalf("got %s", n)
	}

	empty := NewStream(10)
	n, err = empty.Int()
	if err != nil {
		t.Fatal(err)
	}
	if n.Sign() != 0 {
		t.Fatalf("empty stream gave %s", n)
	}

	zeros := NewStream(10)
	if err := zeros.Feed(strings.Repeat("0", 10)); err != nil {
		t.Fatal(err)
	}
	n, err = zeros.Int()
	if err != nil {
		t.Fatal(err)
	}
	if n.Sign() != 0 {
		t.Fatalf("all-zero stream gave %s", n)
	}
}
