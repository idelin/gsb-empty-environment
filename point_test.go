package netsec

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

func mustKey(t *testing.T, curve ecdh.Curve) (*ecdh.PrivateKey, []byte) {
	t.Helper()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return priv, priv.PublicKey().Bytes()
}

func TestDecodePoint(t *testing.T) {
	curves := []struct {
		name  string
		curve ecdh.Curve
		size  int
	}{
		{"P-256", ecdh.P256(), 32},
		{"P-384", ecdh.P384(), 48},
		{"P-521", ecdh.P521(), 66},
	}

	for _, tc := range curves {
		t.Run(tc.name, func(t *testing.T) {
			_, raw := mustKey(t, tc.curve)

			t.Run("uncompressed on curve accepted", func(t *testing.T) {
				key, err := DecodePoint(tc.curve, raw)
				if err != nil {
					t.Fatalf("DecodePoint: %v", err)
				}
				if key == nil || key.Curve() != tc.curve {
					t.Fatalf("returned key is not on the negotiated curve")
				}
			})

			for _, prefix := range []byte{0x02, 0x03, 0x06, 0x07, 0x00, 0x05, 0x41} {
				p := prefix
				t.Run(fmt.Sprintf("prefix 0x%02x", p), func(t *testing.T) {
					mutated := append([]byte(nil), raw...)
					mutated[0] = p
					if _, err := DecodePoint(tc.curve, mutated); !errors.Is(err, ErrRejectedPointFormat) {
						t.Fatalf("prefix 0x%02x: got err %v, want ErrRejectedPointFormat", p, err)
					}
				})
			}

			t.Run("compressed payload rejected", func(t *testing.T) {
				compressed := append([]byte{0x02}, raw[1:1+tc.size]...)
				if _, err := DecodePoint(tc.curve, compressed); !errors.Is(err, ErrRejectedPointFormat) {
					t.Fatalf("compressed point: got err %v, want ErrRejectedPointFormat", err)
				}
			})

			t.Run("truncated uncompressed rejected", func(t *testing.T) {
				if _, err := DecodePoint(tc.curve, raw[:len(raw)-1]); err == nil {
					t.Fatal("truncated point was accepted")
				}
			})

			t.Run("off-curve uncompressed rejected", func(t *testing.T) {
				off := append([]byte(nil), raw...)
				off[len(off)-1] ^= 0xff
				if _, err := DecodePoint(tc.curve, off); err == nil {
					t.Fatal("off-curve point was accepted")
				} else if errors.Is(err, ErrRejectedPointFormat) {
					t.Fatalf("off-curve point failed on format instead of curve check: %v", err)
				}
			})

			t.Run("empty and nil rejected", func(t *testing.T) {
				if _, err := DecodePoint(tc.curve, nil); !errors.Is(err, ErrRejectedPointFormat) {
					t.Fatalf("nil: got %v, want ErrRejectedPointFormat", err)
				}
				if _, err := DecodePoint(tc.curve, []byte{}); !errors.Is(err, ErrRejectedPointFormat) {
					t.Fatalf("empty: got %v, want ErrRejectedPointFormat", err)
				}
			})
		})
	}
}

func TestCurveWrappers(t *testing.T) {
	wrappers := []struct {
		name   string
		curve  ecdh.Curve
		decode func([]byte) (*ecdh.PublicKey, error)
	}{
		{"P-256", ecdh.P256(), DecodeP256Point},
		{"P-384", ecdh.P384(), DecodeP384Point},
		{"P-521", ecdh.P521(), DecodeP521Point},
	}
	for _, tc := range wrappers {
		t.Run(tc.name, func(t *testing.T) {
			_, raw := mustKey(t, tc.curve)
			key, err := tc.decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if key.Curve() != tc.curve {
				t.Fatal("wrapper decoded against the wrong curve")
			}

			mutated := append([]byte(nil), raw...)
			mutated[0] = 0x03
			if _, err := tc.decode(mutated); !errors.Is(err, ErrRejectedPointFormat) {
				t.Fatalf("compressed prefix: got %v, want ErrRejectedPointFormat", err)
			}
		})
	}
}
