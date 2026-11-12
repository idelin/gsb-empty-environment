package netsec

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

type ed25519PrivateKey = ed25519.PrivateKey

func ed25519Sign(priv ed25519.PrivateKey, msg []byte) []byte {
	return ed25519.Sign(priv, msg)
}

func ed25519PublicKeyOf(priv ed25519.PrivateKey) []byte {
	return []byte(priv.Public().(ed25519.PublicKey))
}

func mustKey(t *testing.T, seedByte byte) (ed25519.PrivateKey, []byte) {
	t.Helper()
	var seed [ed25519.SeedSize]byte
	seed[0] = seedByte
	seed[ed25519.SeedSize-1] = 0xA0 ^ seedByte
	pub, priv, err := ed25519.GenerateKey(newSeededReader(seed))
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv, []byte(pub)
}

// seededReader deterministically fills the SeedSize-byte buffer that
// ed25519 key generation requests.
type seededReader struct{ seed [ed25519.SeedSize]byte }

func newSeededReader(seed [ed25519.SeedSize]byte) *seededReader {
	return &seededReader{seed: seed}
}

func (r *seededReader) Read(p []byte) (int, error) {
	if len(p) != ed25519.SeedSize {
		return 0, errors.New("unexpected read size")
	}
	copy(p, r.seed[:])
	return len(p), nil
}

func buildV1EnvelopeBytes(t *testing.T, source, dest []byte, sigs []DecoratedSignature) ([]byte, []byte) {
	t.Helper()
	tx := &xdrWriter{}
	tx.v1Transaction(source, 100, 1, dest, 50)

	env := &xdrWriter{}
	env.v1Envelope(tx.bytes(), sigs...)
	return env.bytes(), tx.bytes()
}

func staticAccount(master []byte, masterWeight uint32, extra ...Signer) *Account {
	return &Account{
		Low: 1, Medium: 2, High: 3,
		Signers: append(
			[]Signer{{PublicKey: append([]byte(nil), master...), Weight: masterWeight}},
			extra...,
		),
	}
}

func lookupFor(accounts ...*Account) AccountLookup {
	return func(id []byte) (*Account, bool) {
		for _, a := range accounts {
			for _, s := range a.Signers {
				if string(s.PublicKey) == string(id) {
					cp := *a
					return &cp, true
				}
			}
		}
		return nil, false
	}
}

func lowThreshold(*Account) uint32 { return 1 }
