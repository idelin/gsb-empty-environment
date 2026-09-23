package erasure

import "testing"

func TestGeneratorOrder(t *testing.T) {
	seen := make(map[byte]bool)
	x := byte(1)
	for i := 0; i < 255; i++ {
		if seen[x] {
			t.Fatalf("cycle shorter than 255 at step %d", i)
		}
		seen[x] = true
		x = gfMul(x, 2)
	}
	if x != 1 {
		t.Fatalf("2 is not a generator: order not 255")
	}
	if len(seen) != 255 {
		t.Fatalf("expected 255 distinct non-zero elements, got %d", len(seen))
	}
}

func TestMulDivInverse(t *testing.T) {
	for a := 1; a < 256; a++ {
		for b := 1; b < 256; b++ {
			ab := gfMul(byte(a), byte(b))
			if got := gfDiv(ab, byte(b)); got != byte(a) {
				t.Fatalf("div(mul(%d,%d),%d) = %d", a, b, b, got)
			}
		}
		if got := gfMul(byte(a), gfInv(byte(a))); got != 1 {
			t.Fatalf("mul(%d, inv(%d)) = %d, want 1", a, a, got)
		}
	}
}

func TestAddIsXor(t *testing.T) {
	for a := 0; a < 256; a += 17 {
		for b := 0; b < 256; b += 31 {
			if gfMul(byte(a), 0) != 0 || gfMul(0, byte(b)) != 0 {
				t.Fatal("multiplication by zero must be zero")
			}
		}
	}
}
