package netsec

import "testing"

func identitySBox() []uint8 {
	s := make([]uint8, 16)
	for i := range s {
		s[i] = uint8(i)
	}
	return s
}

func identityPerm() []uint8 {
	p := make([]uint8, 16)
	for i := range p {
		p[i] = uint8(i)
	}
	return p
}

// presentSBox is the nonlinear bijective PRESENT S-box.
func presentSBox() []uint8 {
	return []uint8{0xC, 0x5, 0x6, 0xB, 0x9, 0x0, 0xA, 0xD, 0x3, 0xE, 0xF, 0x8, 0x4, 0x7, 0x1, 0x2}
}

// presentPerm16 is the 16-bit analogue of the PRESENT bit permutation:
// bit i moves to position 4i mod 15 for i < 15, bit 15 stays.
func presentPerm16() []uint8 {
	p := make([]uint8, 16)
	for i := 0; i < 15; i++ {
		p[i] = uint8((4 * i) % 15)
	}
	p[15] = 15
	return p
}

func mustCipher(t *testing.T, sbox, perm []uint8, rounds int) *Cipher {
	t.Helper()
	c, err := NewCipher(sbox, perm, rounds)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestNewCipherRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		sbox   []uint8
		perm   []uint8
		rounds int
	}{
		{"rounds too few", identitySBox(), identityPerm(), 0},
		{"rounds too many", identitySBox(), identityPerm(), 5},
		{"short sbox", identitySBox()[:15], identityPerm(), 1},
		{"sbox value out of range", append(append([]uint8(nil), identitySBox()[:15]...), 16), identityPerm(), 1},
		{"sbox not bijective", []uint8{0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, identityPerm(), 1},
		{"short perm", identitySBox(), identityPerm()[:15], 1},
		{"perm value out of range", identitySBox(), append(append([]uint8(nil), identityPerm()[:15]...), 16), 1},
		{"perm not bijective", identitySBox(), []uint8{0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCipher(tc.sbox, tc.perm, tc.rounds); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestIdentitySBoxDDT(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 1)
	ddt := c.DDT()
	for a := 0; a < 16; a++ {
		for b := 0; b < 16; b++ {
			want := uint8(0)
			if a == b {
				want = 16
			}
			if ddt[a][b] != want {
				t.Fatalf("identity DDT[%d][%d] = %d, want %d", a, b, ddt[a][b], want)
			}
		}
	}
}

func TestPresentDDTProperties(t *testing.T) {
	c := mustCipher(t, presentSBox(), presentPerm16(), 2)
	ddt := c.DDT()
	if ddt[0][0] != 16 {
		t.Fatalf("DDT[0][0] = %d, want 16", ddt[0][0])
	}
	for a := 0; a < 16; a++ {
		sum := 0
		for b := 0; b < 16; b++ {
			v := ddt[a][b]
			if v%2 != 0 {
				t.Fatalf("DDT[%d][%d] = %d is odd; DDT entries must be even", a, b, v)
			}
			sum += int(v)
		}
		if sum != 16 {
			t.Fatalf("DDT row %d sums to %d, want 16", a, sum)
		}
		if a != 0 && ddt[a][0] != 0 {
			t.Fatalf("DDT[%d][0] = %d, want 0 for a bijective S-box", a, ddt[a][0])
		}
	}
}

func TestEncryptIdentityCipher(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 3)
	keys := []uint16{0x1234, 0x5678, 0x9ABC}
	got, err := c.Encrypt(0x00FF, keys)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	want := uint16(0x00FF ^ 0x1234 ^ 0x5678 ^ 0x9ABC)
	if got != want {
		t.Fatalf("identity cipher Encrypt = %#04x, want %#04x", got, want)
	}
	if _, err := c.Encrypt(0, keys[:2]); err == nil {
		t.Fatalf("expected error for wrong round-key count")
	}
}

func TestPermuteBitsSemantics(t *testing.T) {
	// Bit 0 moves to position 5, everything else stays.
	perm := identityPerm()
	perm[0] = 5
	perm[5] = 0
	c := mustCipher(t, identitySBox(), perm, 1)
	if got := c.PermuteBits(0x0001); got != 1<<5 {
		t.Fatalf("PermuteBits(1) = %#04x, want %#04x", got, uint16(1<<5))
	}
	if got := c.PermuteBits(1 << 5); got != 0x0001 {
		t.Fatalf("PermuteBits(1<<5) = %#04x, want 1", got)
	}
}
