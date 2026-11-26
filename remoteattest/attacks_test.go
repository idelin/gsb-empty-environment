package remoteattest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"testing"
)

func TestForgedToMToUZeroHashMarksPlatformInsecure(t *testing.T) {
	fix := newTestFixture()

	goodDigest := fileDigest([]byte("good binary"))
	// Malicious binary hidden behind an all-zero d-ng digest (open writers /
	// ToMToU): the kernel records zero instead of measuring the content.
	malicious := builtEntry{
		pcr: 10, template: TemplateIMANG,
		fileDigest: make([]byte, sha256.Size), path: "/usr/bin/evil",
		zeroFileDigest: true,
	}
	entries := []builtEntry{
		{pcr: 10, template: TemplateIMANG, fileDigest: goodDigest, path: "/usr/bin/good"},
		malicious,
	}
	log := serializeEntries(entries)

	replay, err := ReplayIMALog(log)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if len(replay.Violations) != 1 || replay.Violations[0].Path != "/usr/bin/evil" {
		t.Fatalf("want one ToMToU violation on /usr/bin/evil, got %+v", replay.Violations)
	}

	// The violating extension must be 0xFF..FF, not the zero digest.
	wantPCR := extendAll(
		extendAll(make([]byte, sha256.Size), entryDigestFor(t, log, 0)),
		bytesFF(sha256.Size),
	)
	if !bytes.Equal(replay.PCRs[10], wantPCR) {
		t.Fatal("ToMToU entry was not extended with 0xFF..FF")
	}

	// Even a correctly signed quote over the replayed (0xFF-extended) PCRs
	// must be judged insecure because of the ToMToU policy violation.
	selection := pcr10Selection()
	nonce := []byte("nonce-for-tomtou-test-000000000000")
	attest, sig := fix.buildQuote(nonce, selection, mustPCRDigest(t, replay.PCRs, selection))

	baseline := &Whitelist{AllowedDigests: map[string][]string{
		hex.EncodeToString(goodDigest): {"/usr/bin/good"},
	}}
	cfg := &Config{
		AttestBlob: attest, SignatureBlob: sig, IMALog: log, ExpectedNonce: nonce,
		AKCert: fix.akCert, Intermediates: []*x509.Certificate{fix.interCert},
		Roots: fix.roots, Baseline: baseline, PCRBank: fullBank(t, replay),
	}
	report, err := Verify(cfg)
	if err == nil {
		t.Fatal("expected platform to be judged insecure on ToMToU")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PolicyError, got %v", err)
	}
	if !hasFinding(report, FindingToMToU) || !hasFinding(report, FindingWhitelistMiss) {
		t.Fatalf("want ToMToU + whitelist findings, got %v", report.Findings)
	}
}

func TestZeroClaimedTemplateHashAlsoViolation(t *testing.T) {
	fix := newTestFixture()
	digest := fileDigest([]byte("payload"))
	entries := []builtEntry{{
		pcr: 10, template: TemplateIMANG, fileDigest: digest,
		path: "/usr/bin/x", zeroHash: true,
	}}
	log := serializeEntries(entries)

	replay, err := ReplayIMALog(log)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if len(replay.Violations) != 1 {
		t.Fatalf("want zero-hash violation, got %d", len(replay.Violations))
	}
	if !bytes.Equal(replay.PCRs[10], extendAll(make([]byte, sha256.Size), bytesFF(sha256.Size))) {
		t.Fatal("zero claimed template hash must extend 0xFF..FF")
	}

	selection := pcr10Selection()
	nonce := []byte("nonce-zero-template-hash-000000000")
	attest, sig := fix.buildQuote(nonce, selection, mustPCRDigest(t, replay.PCRs, selection))
	cfg := &Config{
		AttestBlob: attest, SignatureBlob: sig, IMALog: log, ExpectedNonce: nonce,
		AKCert: fix.akCert, Intermediates: []*x509.Certificate{fix.interCert},
		Roots: fix.roots,
		Baseline: &Whitelist{AllowedDigests: map[string][]string{
			hex.EncodeToString(digest): {"/usr/bin/x"},
		}},
		PCRBank: fullBank(t, replay),
	}
	report, err := Verify(cfg)
	if err == nil || !hasFinding(report, FindingToMToU) {
		t.Fatalf("want ToMToU finding, got err=%v report=%v", err, report)
	}
}

func TestAKChainAndSignatureBinding(t *testing.T) {
	b := validBundle(t)

	t.Run("untrusted root", func(t *testing.T) {
		cfg := b.config()
		cfg.Roots = x509.NewCertPool()
		if _, err := Verify(cfg); !errors.Is(err, ErrAKNotTrusted) {
			t.Fatalf("want ErrAKNotTrusted, got %v", err)
		}
	})

	t.Run("forged AK signature", func(t *testing.T) {
		cfg := b.config()
		cfg.SignatureBlob = bytes.Clone(b.sig)
		cfg.SignatureBlob[len(cfg.SignatureBlob)-1] ^= 0xFF
		if _, err := Verify(cfg); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
	})

	t.Run("qualifiedSigner binds another key", func(t *testing.T) {
		cfg := b.config()
		cfg.AttestBlob = bytes.Clone(b.attest)
		// qualifiedSigner TPM2B starts after magic(4)+type(2); flip a byte
		// inside the 33-byte compressed point.
		cfg.AttestBlob[4+2+2+10] ^= 0x01
		if _, err := Verify(cfg); !errors.Is(err, ErrAKBinding) {
			t.Fatalf("want ErrAKBinding, got %v", err)
		}
	})
}

func TestImaSigSignatureVerification(t *testing.T) {
	b := validBundle(t)

	t.Run("trusted key passes", func(t *testing.T) {
		if _, err := Verify(b.config()); err != nil {
			t.Fatalf("unexpected failure: %v", err)
		}
	})

	t.Run("untrusted signing key rejected", func(t *testing.T) {
		cfg := b.config()
		other, _ := mustCA(nil, nil, "other", false)
		cfg.ImaSigKeys = []*ecdsa.PublicKey{&other.PublicKey}
		report, err := Verify(cfg)
		if err == nil {
			t.Fatal("expected ima-sig failure")
		}
		if !hasFinding(report, FindingSignatureBroken) {
			t.Fatalf("want IMA_SIG_INVALID, got %v", report.Findings)
		}
	})

	t.Run("no verification keys configured", func(t *testing.T) {
		cfg := b.config()
		cfg.ImaSigKeys = nil
		report, err := Verify(cfg)
		if err == nil || !hasFinding(report, FindingSignatureBroken) {
			t.Fatalf("want IMA_SIG_INVALID, got err=%v", err)
		}
	})
}
