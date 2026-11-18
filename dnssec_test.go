package netsec

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"testing"
	"time"
)

func TestSignAndVerifyRRSetAlgorithms(t *testing.T) {
	zone := "zone.example."
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	inception := now.Add(-time.Hour)
	expiration := now.Add(time.Hour)
	record, err := ParseDS("12345 8 2 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatalf("ParseDS: %v", err)
	}

	tests := []struct {
		name string
		alg  uint8
		key  func(t *testing.T) (crypto.Signer, DNSKEY)
	}{
		{
			name: "RSA/SHA-256",
			alg:  AlgorithmRSASHA256,
			key:  rsaTestKey,
		},
		{
			name: "Ed25519",
			alg:  AlgorithmED25519,
			key:  ed25519TestKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			private, key := tc.key(t)
			records := [][]byte{record.rdata()}
			sig, err := SignRRSet(zone, TypeCDS, records, private, key, inception, expiration, 3600)
			if err != nil {
				t.Fatalf("SignRRSet: %v", err)
			}
			if err := VerifyRRSet(zone, TypeCDS, records, []RRSIG{sig}, key, now); err != nil {
				t.Fatalf("VerifyRRSet: %v", err)
			}
			tampered := [][]byte{append(append([]byte(nil), records[0]...), 1)}
			if err := VerifyRRSet(zone, TypeCDS, tampered, []RRSIG{sig}, key, now); err == nil {
				t.Fatal("VerifyRRSet accepted a tampered RRset")
			}
		})
	}
}

func rsaTestKey(t *testing.T) (crypto.Signer, DNSKEY) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	exponent := big.NewInt(int64(private.PublicKey.E)).Bytes()
	public := make([]byte, 1, 1+len(exponent)+len(private.PublicKey.N.Bytes()))
	public[0] = byte(len(exponent))
	public = append(public, exponent...)
	public = append(public, private.PublicKey.N.Bytes()...)
	return private, DNSKEY{
		Flags:     257,
		Protocol:  3,
		Algorithm: AlgorithmRSASHA256,
		PublicKey: public,
	}
}

func ed25519TestKey(t *testing.T) (crypto.Signer, DNSKEY) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	return private, DNSKEY{
		Flags:     257,
		Protocol:  3,
		Algorithm: AlgorithmED25519,
		PublicKey: public,
	}
}
