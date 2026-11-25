package reuseauth

import (
	"bytes"
	"errors"
	"testing"

	"crypto/ed25519"
)

func TestIndependentProofVerificationAndTampering(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]
	j := f.evidence(evidenceConfig{data: []byte("proof123"), public: true, knownLength: true})
	nav := newTestNav(t, "nav-proof", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	req := j.request("req-proof", app, nil)

	sealed, shared, err := f.auth.AuthorizeReuse(nav.Clone(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 4 {
		t.Fatalf("shared = %d ranges, want 4", len(shared))
	}

	independent, err := NewPublicAuthority("authority", f.trust, f.auth.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := independent.VerifyProof(nav.Clone(), sealed, req, 10); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}

	t.Run("epoch change", func(t *testing.T) {
		stale := nav.Clone()
		stale.Epoch++
		if _, err := independent.VerifyProof(stale, sealed, req, 10); !errors.Is(err, ErrEpoch) {
			t.Fatalf("err = %v, want ErrEpoch", err)
		}
	})

	t.Run("policy revocation append", func(t *testing.T) {
		revoked := nav.Clone()
		if err := revoked.Deny(PolicyDecision{Source: app, Target: cdn, Action: ActionRead, Visibility: VisibilityPublic}); err != nil {
			t.Fatal(err)
		}
		if _, err := independent.VerifyProof(revoked, sealed, req, 10); !errors.Is(err, ErrPolicyDenied) {
			t.Fatalf("err = %v, want ErrPolicyDenied", err)
		}
	})

	t.Run("offered digest tamper", func(t *testing.T) {
		tampered := req
		tampered.Offered = append([]RangeDigest(nil), req.Offered...)
		tampered.Offered[0].Digest[0] ^= 0xff
		if _, err := independent.VerifyProof(nav.Clone(), sealed, tampered, 10); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
	})

	t.Run("signed response tamper", func(t *testing.T) {
		tampered := req
		tampered.Response.Response.Public = false
		tampered.Response.Response.Recipient = app
		_, err := independent.VerifyProof(nav.Clone(), sealed, tampered, 10)
		if !errors.Is(err, ErrSignature) && !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want signature/digest failure", err)
		}
	})

	t.Run("proof body tamper", func(t *testing.T) {
		tamperedProof := sealed
		tamperedProof.Proof.Epoch = 99
		if _, err := independent.VerifyProof(nav.Clone(), tamperedProof, req, 10); !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})

	t.Run("wrong authority key", func(t *testing.T) {
		wrongSeed := bytes.Repeat([]byte{0x01}, ed25519.SeedSize)
		wrong := ed25519.NewKeyFromSeed(wrongSeed).Public().(ed25519.PublicKey)
		wrongAuthority, err := NewPublicAuthority("authority", f.trust, wrong)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wrongAuthority.VerifyProof(nav.Clone(), sealed, req, 10); !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})
}

func TestSignedRedirectChainRejectsLinkTampering(t *testing.T) {
	f := newFixture(t)
	j := f.evidence(evidenceConfig{data: []byte("redirect"), public: true, knownLength: true})
	if _, err := j.Chain.Verify(f.trust, 10); err != nil {
		t.Fatal(err)
	}

	tampered := j.Chain
	tampered.Hops[0].Location = "https://evil.example/steal"
	if _, err := tampered.Verify(f.trust, 10); err == nil {
		t.Fatal("tampered redirect location verified")
	}

	forged := j.Chain
	forged.Hops[0].Status = 301
	if _, err := forged.Verify(f.trust, 10); err == nil {
		t.Fatal("unsigned status change verified")
	}
}
