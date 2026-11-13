package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
)

type testParty struct {
	key    *ecdsa.PrivateKey
	signer *Signer
	public []byte
	req    SignRequest
}

func generateTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func mustPublicKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	encoded, err := CompressedPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("compress public key: %v", err)
	}
	return encoded
}

func mustRoster(t *testing.T, keys ...*ecdsa.PrivateKey) *Roster {
	t.Helper()
	publicKeys := make([]*ecdsa.PublicKey, len(keys))
	for i, key := range keys {
		publicKeys[i] = &key.PublicKey
	}
	roster, err := NewRoster(publicKeys)
	if err != nil {
		t.Fatalf("new roster: %v", err)
	}
	return roster
}

func newTestParty(t *testing.T, key *ecdsa.PrivateKey, roster *Roster, sessionID, message []byte) *testParty {
	t.Helper()
	signer, err := NewSigner(key, NewMemoryNonceStore())
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return &testParty{
		key:    key,
		signer: signer,
		public: mustPublicKey(t, key),
		req: SignRequest{
			SessionID: sessionID,
			Message:   message,
			Roster:    roster,
		},
	}
}

func differentPoint(t *testing.T, current []byte) []byte {
	t.Helper()
	for i := int64(1); i < 16; i++ {
		encoded := scalarBaseMult(big.NewInt(i)).compressed()
		if !bytes.Equal(encoded, current) {
			return encoded
		}
	}
	t.Fatal("could not find a different point")
	return nil
}
