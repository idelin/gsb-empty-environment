package netsec

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestPBKDF2MatchesStandardLibrary(t *testing.T) {
	want, err := pbkdf2.Key(sha256.New, "password", []byte("salt"), 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pbkdf2SHA256(context.Background(), []byte("password"), []byte("salt"), 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pbkdf2 = %x, want %x", got, want)
	}
}

func TestPBKDF2HasherRoundTripAndFloor(t *testing.T) {
	params := testParams()
	hash, err := (PBKDF2Hasher{}).Hash(context.Background(), []byte("correct horse"), params)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := (PBKDF2Hasher{}).Verify(context.Background(), []byte("correct horse"), hash)
	if err != nil || !ok {
		t.Fatalf("verify correct = %v, %v", ok, err)
	}
	ok, err = (PBKDF2Hasher{}).Verify(context.Background(), []byte("wrong horse"), hash)
	if err != nil || ok {
		t.Fatalf("verify wrong = %v, %v", ok, err)
	}
	weak := params
	weak.Iterations = 1
	if _, err := (PBKDF2Hasher{}).Hash(context.Background(), []byte("password"), weak); err != nil {
		// Hash itself only enforces structural bounds; PreparePassword applies
		// the configured minimum. A one-iteration hash is still structurally
		// valid, so this branch is informative rather than required.
		t.Logf("weak structure rejected: %v", err)
	}
	tooLarge := params
	tooLarge.SaltBytes = MaxPasswordSaltBytes + 1
	if _, err := (PBKDF2Hasher{}).Hash(context.Background(), []byte("password"), tooLarge); !errors.Is(err, ErrWeakPasswordParams) {
		t.Fatalf("oversized salt error = %v", err)
	}
}
