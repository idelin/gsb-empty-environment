package npmverify

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"testing"
)

func TestParseIntegrity(t *testing.T) {
	data := []byte("subresource integrity payload")

	sha256, err := ComputeIntegrity("sha256", data)
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}
	sha512, err := ComputeIntegrity("sha512", data)
	if err != nil {
		t.Fatalf("sha512: %v", err)
	}

	entries, err := ParseIntegrity(sha256 + "  " + sha512)
	if err != nil {
		t.Fatalf("ParseIntegrity: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	best := StrongestIntegrity(entries)
	if best.Algorithm != "sha512" {
		t.Fatalf("strongest = %q, want sha512", best.Algorithm)
	}

	// First-occurrence wins for equal strength.
	first := StrongestIntegrity([]Integrity{
		{Algorithm: "sha256", Digest: "a"},
		{Algorithm: "sha256", Digest: "b"},
	})
	if first.Digest != "a" {
		t.Fatalf("tie-break digest = %q, want a", first.Digest)
	}
}

func TestParseIntegrityMalformed(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"sha256",
		"sha256-",
		"-AAAA",
		"nope-AAAA",
		"sha256-!!!notbase64!!!",
	}
	for _, in := range cases {
		if _, err := ParseIntegrity(in); !errors.Is(err, ErrMalformedIntegrity) {
			t.Errorf("ParseIntegrity(%q) err = %v, want ErrMalformedIntegrity", in, err)
		}
	}
}

func TestVerifyIntegrityStrengthAndMismatch(t *testing.T) {
	data := []byte("the quick brown fox")

	sha256, err := ComputeIntegrity("sha256", data)
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}
	sha512Integrity, err := ComputeIntegrity("sha512", data)
	if err != nil {
		t.Fatalf("sha512: %v", err)
	}

	best, err := verifyIntegrity(sha256+" "+sha512Integrity, data)
	if err != nil {
		t.Fatalf("verifyIntegrity: %v", err)
	}
	if best.Algorithm != "sha512" {
		t.Fatalf("matched %q, want sha512", best.Algorithm)
	}

	// A digest over different bytes must fail even when one algorithm's
	// metadata is otherwise valid.
	sum := sha512.Sum512([]byte("different content"))
	wrongIntegrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	if _, err := verifyIntegrity(wrongIntegrity, data); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("mismatch err = %v, want ErrIntegrityMismatch", err)
	}
}
