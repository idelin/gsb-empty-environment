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

// bundle is a fully constructed, valid attestation run.
type bundle struct {
	fix       *testFixture
	log       []byte
	selection PCRSelection
	nonce     []byte
	attest    []byte
	sig       []byte
	baseline  *Whitelist
}

func pcr10Selection() PCRSelection {
	// 3-byte select, PCR 10 -> byte 1, bit 2.
	return PCRSelection{HashAlg: TPMAlgSHA256, Bitmap: []byte{0x00, 0x04, 0x00}}
}

func validBundle(t *testing.T) *bundle {
	t.Helper()
	fix := newTestFixture()

	lsDigest := fileDigest([]byte("fake /usr/bin/ls contents"))
	bashDigest := fileDigest([]byte("fake /usr/bin/bash contents"))
	bashSig := fix.signFileDigest(bashDigest)
	sshdDigest := fileDigest([]byte("fake /usr/sbin/sshd contents"))

	entries := []builtEntry{
		{pcr: 10, template: TemplateIMANG, fileDigest: lsDigest, path: "/usr/bin/ls"},
		{pcr: 10, template: TemplateIMASIG, fileDigest: bashDigest, path: "/usr/bin/bash", signature: bashSig},
		{pcr: 10, template: TemplateIMANG, fileDigest: sshdDigest, path: "/usr/sbin/sshd"},
	}
	log := serializeEntries(entries)

	replay, err := ReplayIMALog(log)
	if err != nil {
		t.Fatalf("replay setup failed: %v", err)
	}

	selection := pcr10Selection()
	digest := mustPCRDigest(t, replay.PCRs, selection)
	nonce := []byte("fresh-random-nonce-0123456789abcdef")
	attest, sig := fix.buildQuote(nonce, selection, digest)

	baseline := &Whitelist{AllowedDigests: map[string][]string{
		hex.EncodeToString(lsDigest):   {"/usr/bin/ls"},
		hex.EncodeToString(bashDigest): {"/usr/bin/bash"},
		hex.EncodeToString(sshdDigest): {"/usr/sbin/sshd"},
	}}

	return &bundle{
		fix: fix, log: log, selection: selection,
		nonce: nonce, attest: attest, sig: sig, baseline: baseline,
	}
}

func (b *bundle) config() *Config {
	replay, err := ReplayIMALog(b.log)
	if err != nil {
		panic(err)
	}
	return &Config{
		AttestBlob:    b.attest,
		SignatureBlob: b.sig,
		IMALog:        b.log,
		ExpectedNonce: b.nonce,
		AKCert:        b.fix.akCert,
		Intermediates: []*x509.Certificate{b.fix.interCert},
		Roots:         b.fix.roots,
		Baseline:      b.baseline,
		ImaSigKeys:    []*ecdsa.PublicKey{&b.fix.imaSigKey.PublicKey},
		PCRBank:       fullBank(nil, replay),
	}
}

func TestValidAttestationPasses(t *testing.T) {
	b := validBundle(t)
	report, err := Verify(b.config())
	if err != nil {
		t.Fatalf("expected valid attestation to pass, got %v", err)
	}
	if !report.Secure() {
		t.Fatalf("expected secure report, findings=%v", report.Findings)
	}
	if len(report.Replay.Entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(report.Replay.Entries))
	}
	if len(report.Replay.Violations) != 0 {
		t.Fatalf("unexpected violations: %d", len(report.Replay.Violations))
	}

	att := report.Attestation
	if att.Magic != MagicTCG || att.Type != TPMSTAttestQuote {
		t.Fatalf("decoded magic/type wrong: %x %x", att.Magic, att.Type)
	}
	if !bytes.Equal(att.ExtraData, b.nonce) {
		t.Fatal("extraData/nonce not round-tripped")
	}
	if got := att.SelectedPCRs(); len(got) != 1 || got[0] != 10 {
		t.Fatalf("selected PCRs = %v, want [10]", got)
	}
	if !bytes.Equal(att.PCRDigest, mustPCRDigest(t, report.Replay.PCRs, att.PCRSelection)) {
		t.Fatal("pcrDigest not bound to replayed PCRs")
	}
}

func TestTamperedClaimedTemplateHashRejected(t *testing.T) {
	b := validBundle(t)

	// Flip the last byte of entry 2's 20-byte claimed SHA-1 template hash.
	off := entryHeaderOffset(t, b.log, 1)
	mutated := bytes.Clone(b.log)
	mutated[off+4+19] ^= 0xFF

	cfg := b.config()
	cfg.IMALog = mutated
	if _, err := Verify(cfg); !errors.Is(err, ErrTemplateHashMismatch) {
		t.Fatalf("want ErrTemplateHashMismatch, got %v", err)
	}
}

func TestTamperedFileDigestIsPolicyViolation(t *testing.T) {
	b := validBundle(t)

	// Corrupt the d-ng file digest of the first entry. The template hash is
	// computed over path/signature only, so the hash check still passes; the
	// baseline check must catch the hidden payload.
	idx := digestFieldOffset(t, b.log, 0)
	mutated := bytes.Clone(b.log)
	mutated[idx] ^= 0x01

	cfg := b.config()
	cfg.IMALog = mutated
	report, err := Verify(cfg)
	if err == nil {
		t.Fatal("expected policy violation for forged file digest")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PolicyError, got %T %v", err, err)
	}
	if !hasFinding(report, FindingWhitelistMiss) {
		t.Fatalf("want WHITELIST_MISS finding, got %v", report.Findings)
	}
}

func TestReplayOldNonceRejected(t *testing.T) {
	b := validBundle(t)
	cfg := b.config()

	// Attacker replays a historically valid quote; the verifier demands a
	// fresh qualifyingData nonce.
	cfg.ExpectedNonce = []byte("a-different-fresh-nonce-aaaaaaaaaa")
	if _, err := Verify(cfg); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("want ErrNonceMismatch, got %v", err)
	}

	// A truncated nonce must also fail rather than match a prefix.
	cfg.ExpectedNonce = b.nonce[:len(b.nonce)-1]
	if _, err := Verify(cfg); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("want ErrNonceMismatch for short nonce, got %v", err)
	}
}

func TestPCRBitmapMismatchRejected(t *testing.T) {
	b := validBundle(t)

	replay, err := ReplayIMALog(b.log)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("forged bitmap keeps originally signed digest", func(t *testing.T) {
		// Attack: flip pcrSelect from PCR 10 to PCR 11 while leaving the
		// signed pcrDigest untouched. The blob bytes no longer match the AK
		// signature, so the tampering is rejected before replay.
		attest := bytes.Clone(b.attest)
		// Locate the selection bitmap: magic(4)+type(2)+qualified(2+33)+
		// nonce(2+len)+clock(8)+reset(4)+restart(4)+safe(1)+fw(8)+
		// hashAlg(2)+size(1); flip byte 1 of the bitmap.
		bitmapOff := 4 + 2 + 2 + compressedPointSize + 2 + len(b.nonce) +
			8 + 4 + 4 + 1 + 8 + 2 + 1
		if attest[bitmapOff] != 0x00 || attest[bitmapOff+1] != 0x04 {
			t.Fatalf("fixture bitmap offset wrong: %x", attest[bitmapOff:bitmapOff+3])
		}
		attest[bitmapOff] = 0x00
		attest[bitmapOff+1] = 0x08

		cfg := b.config()
		cfg.AttestBlob = attest
		if _, err := Verify(cfg); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("want ErrBadSignature for mutated signed blob, got %v", err)
		}
	})

	t.Run("re-signed quote over wrong PCR is rejected by selection policy", func(t *testing.T) {
		// Even with a valid AK signature and a consistent PCR 11 digest,
		// the quote no longer covers the IMA PCR 10, so the required-PCR
		// policy must reject it (and the bank must not silently supply a
		// zero PCR).
		wrong := PCRSelection{HashAlg: TPMAlgSHA256, Bitmap: []byte{0x00, 0x08, 0x00}}
		attest, sig := b.fix.buildQuote(b.nonce, wrong, mustPCRDigest(t, fullBank(nil, replay), wrong))

		cfg := b.config()
		cfg.AttestBlob = attest
		cfg.SignatureBlob = sig
		report, err := Verify(cfg)
		var pe *PolicyError
		if !errors.As(err, &pe) {
			t.Fatalf("want *PolicyError, got %v", err)
		}
		if !hasFinding(report, FindingPCRSelection) {
			t.Fatalf("want PCR_SELECTION_MISSING, got %v", report.Findings)
		}
	})

	t.Run("quote without bank entry cannot match", func(t *testing.T) {
		// A selection covering a PCR absent from the reconstructed bank
		// must hard-fail rather than defaulting that PCR to zero.
		wrong := PCRSelection{HashAlg: TPMAlgSHA256, Bitmap: []byte{0x00, 0x04, 0x08}}
		attest, sig := b.fix.buildQuote(b.nonce, wrong, make([]byte, sha256.Size))
		cfg := b.config()
		cfg.AttestBlob = attest
		cfg.SignatureBlob = sig
		cfg.PCRBank = nil // force missing-PCR path for index 11
		if _, err := Verify(cfg); !errors.Is(err, ErrPCRBankIncomplete) {
			t.Fatalf("want ErrPCRBankIncomplete, got %v", err)
		}
	})
}
