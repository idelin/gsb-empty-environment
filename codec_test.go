package framecomp

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestLZRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	random := make([]byte, 100000)
	rng.Read(random)
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 3000)
	cases := map[string][]byte{
		"empty":      {},
		"one":        {'x'},
		"two":        {'x', 'y'},
		"short":      []byte("abc"),
		"repetitive": bytes.Repeat([]byte("aaaaaaaaaa"), 5000),
		"text":       text,
		"random":     random,
		"binary":     {0, 1, 2, 3, 255, 254, 0, 0, 0, 128, 129, 130},
	}
	for name, src := range cases {
		comp := lzCompress(src)
		got, err := lzDecompress(comp, len(src))
		if err != nil {
			t.Fatalf("%s: decompress error: %v", name, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("%s: round trip mismatch (%d -> %d -> %d bytes)", name, len(src), len(comp), len(got))
		}
	}
}

func TestLZActuallyCompresses(t *testing.T) {
	src := bytes.Repeat([]byte("compressible payload "), 2000)
	if comp := lzCompress(src); len(comp) >= len(src)/4 {
		t.Fatalf("expected good compression, got %d from %d", len(comp), len(src))
	}
}

func TestLZRejectsTruncated(t *testing.T) {
	src := bytes.Repeat([]byte("truncate me please, truncate me. "), 100)
	comp := lzCompress(src)
	for _, n := range []int{1, len(comp) / 2, len(comp) - 1} {
		if _, err := lzDecompress(comp[:n], len(src)); err == nil {
			t.Fatalf("truncated to %d bytes: expected error", n)
		}
	}
}

func TestLZOutputCap(t *testing.T) {
	src := bytes.Repeat([]byte("cap me "), 1000)
	comp := lzCompress(src)
	if _, err := lzDecompress(comp, len(src)-1); err != ErrTooMuchOutput {
		t.Fatalf("want ErrTooMuchOutput, got %v", err)
	}
}

func TestLZRejectsBadDistance(t *testing.T) {
	// Match token with distance beyond produced output.
	bad := []byte{128 + 4, 10, 0}
	if _, err := lzDecompress(bad, 100); err != ErrMalformed {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
	// Zero distance.
	bad = []byte{128 + 4, 0, 0}
	if _, err := lzDecompress(bad, 100); err != ErrMalformed {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
}
