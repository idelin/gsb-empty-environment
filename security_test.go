package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestCommentSpliceFailsClosed(t *testing.T) {
	t.Parallel()

	attacker := []byte(`<NameID>admin@victim.com<!-- signed comment -->.evil.com</NameID>`)
	fullPlaintext := []byte(`<NameID>admin@victim.com.evil.com</NameID>`)

	if _, err := ReadNameID(attacker); !errors.Is(err, ErrNameIDComment) {
		t.Fatalf("ReadNameID() error = %v, want %v", err, ErrNameIDComment)
	}

	canonical, err := CanonicalizeExcludingComments(attacker)
	if err != nil {
		t.Fatalf("CanonicalizeExcludingComments() error = %v", err)
	}
	if !bytes.Equal(canonical, fullPlaintext) {
		t.Fatalf("canonical form = %q, want %q", canonical, fullPlaintext)
	}

	attackerDigest, err := DigestExcludingComments(attacker, sha256.New())
	if err != nil {
		t.Fatalf("DigestExcludingComments(attacker) error = %v", err)
	}
	plaintextDigest, err := DigestExcludingComments(fullPlaintext, sha256.New())
	if err != nil {
		t.Fatalf("DigestExcludingComments(plaintext) error = %v", err)
	}
	if !bytes.Equal(attackerDigest, plaintextDigest) {
		t.Fatalf("attacker digest = %x, want %x", attackerDigest, plaintextDigest)
	}
}
