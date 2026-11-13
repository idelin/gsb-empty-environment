package netsec

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
)

func TestValidExchangeSucceeds(t *testing.T) {
	alice, err := GenerateKey()
	if err != nil {
		t.Fatalf("alice keygen: %v", err)
	}
	bob, err := GenerateKey()
	if err != nil {
		t.Fatalf("bob keygen: %v", err)
	}

	if err := ValidatePeerPoint(bob.PublicKeyBytes()); err != nil {
		t.Fatalf("valid point rejected: %v", err)
	}

	secretA, err := alice.SharedSecret(bob.PublicKeyBytes())
	if err != nil {
		t.Fatalf("alice exchange: %v", err)
	}
	secretB, err := bob.SharedSecret(alice.PublicKeyBytes())
	if err != nil {
		t.Fatalf("bob exchange: %v", err)
	}
	if !bytes.Equal(secretA, secretB) {
		t.Fatal("shared secrets do not match")
	}
	if got := len(secretA); got != PointSize {
		t.Fatalf("shared secret length = %d, want %d", got, PointSize)
	}
}

func TestTamperedYOffCurveRejected(t *testing.T) {
	bob, err := GenerateKey()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	point := bytes.Clone(bob.PublicKeyBytes())

	// The encoding still has the exact expected length, but the final byte of
	// the y coordinate is flipped so the point leaves the curve.
	point[len(point)-1] ^= 0x01

	if len(point) != UncompressedPointSize {
		t.Fatal("tampered point changed length")
	}
	if err := ValidatePoint(point); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("ValidatePoint err = %v, want ErrInvalidPoint", err)
	}

	alice, err := GenerateKey()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := alice.SharedSecret(point); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("SharedSecret err = %v, want ErrInvalidPoint", err)
	}
	if _, err := Exchange(alice, point); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("Exchange err = %v, want ErrInvalidPoint", err)
	}
}

func TestValidatePointRejectsMalformed(t *testing.T) {
	valid := mustGenerate(t).PublicKeyBytes()

	cases := map[string][]byte{
		"too short":      valid[:len(valid)-1],
		"too long":       append(bytes.Clone(valid), 0x00),
		"bad prefix":     append([]byte{0x02}, valid[1:]...),
		"point at limit": pointWithY(new(big.Int).Set(p256Params.P)),
	}
	for name, point := range cases {
		if err := ValidatePoint(point); !errors.Is(err, ErrInvalidPoint) {
			t.Errorf("%s: err = %v, want ErrInvalidPoint", name, err)
		}
	}
}

func TestIsOnCurveSymmetricY(t *testing.T) {
	priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	point := priv.PublicKeyBytes()
	x := new(big.Int).SetBytes(point[1 : 1+PointSize])
	y := new(big.Int).SetBytes(point[1+PointSize:])
	if !IsOnCurve(x, y) {
		t.Fatal("generated public key reported off-curve")
	}

	negY := new(big.Int).Sub(p256Params.P, y)
	if !IsOnCurve(x, negY) {
		t.Fatal("negated y of a valid point must remain on the curve")
	}
	if IsOnCurve(x, new(big.Int).Add(y, big.NewInt(1))) {
		t.Fatal("y+1 unexpectedly reported on-curve")
	}
}

func pointWithY(y *big.Int) []byte {
	point := make([]byte, UncompressedPointSize)
	point[0] = 0x04
	yb := y.Bytes()
	copy(point[UncompressedPointSize-len(yb):], yb)
	return point
}

func mustGenerate(t *testing.T) *KeyPair {
	t.Helper()
	priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return priv
}
