package netsec

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	targetFile = "myapp-2.0.0.tar.gz"
	oldVersion = "1.4.2"
	newVersion = "2.0.0"
)

func signedFixture(t *testing.T, alg string, file string, ts int64, version, content string) (*PublicKey, *Signature, []byte) {
	t.Helper()
	key := newTestKey(t)
	pk := mustParsePublicKey(t, key.publicKeyFile())
	comment := trustedComment(file, ts, version)
	sig := mustParseSignature(t, key.sign(alg, []byte(content), comment, "display only: "+version))
	return pk, sig, []byte(content)
}

func TestVerifyUpdateAcceptsValidPackage(t *testing.T) {
	now := time.Now().Unix()
	pk, sig, content := signedFixture(t, AlgPrehashed, targetFile, now, newVersion, "new binary payload")

	verifier := NewVerifier(pk, time.Time{})
	update, err := verifier.VerifyUpdate(targetFile, sig, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}
	if update.File != targetFile || update.Version != newVersion {
		t.Fatalf("unexpected metadata: %+v", update)
	}
	if update.Timestamp.Unix() != now {
		t.Fatalf("timestamp = %d, want %d", update.Timestamp.Unix(), now)
	}
	if update.UntrustedComment != "display only: "+newVersion {
		t.Fatalf("untrusted comment not preserved for display: %q", update.UntrustedComment)
	}
	if accepted, ok := verifier.LastAccepted(); !ok || accepted.Unix() != now {
		t.Fatalf("watermark not advanced: %v (%v)", accepted, ok)
	}
}

func TestVerifySupportsLegacyAlgorithm(t *testing.T) {
	now := time.Now().Unix()
	pk, sig, content := signedFixture(t, AlgLegacy, targetFile, now, newVersion, "legacy payload")

	if err := Verify(pk, sig, bytes.NewReader(content)); err != nil {
		t.Fatalf("legacy signature failed: %v", err)
	}
	update, err := NewVerifier(pk, time.Time{}).VerifyUpdate(targetFile, sig, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("legacy update rejected: %v", err)
	}
	if update.Version != newVersion {
		t.Fatalf("version = %q, want %q", update.Version, newVersion)
	}
}

// TestTamperedTrustedCommentRejected is the core rollback-by-comment attack:
// an attacker rewrites file/timestamp/version in the comments. The trusted
// comment is bound by the global signature, so the rewrite must fail.
func TestTamperedTrustedCommentRejected(t *testing.T) {
	now := time.Now().Unix()
	key := newTestKey(t)
	pk := mustParsePublicKey(t, key.publicKeyFile())
	original := trustedComment(targetFile, now, newVersion)
	sigBytes := key.sign(AlgPrehashed, []byte("payload"), original, "display only")
	sig := mustParseSignature(t, sigBytes)

	t.Run("rollback version in trusted comment", func(t *testing.T) {
		tampered := *sig
		tampered.TrustedComment = trustedComment(targetFile, now-100000, oldVersion)
		err := Verify(pk, &tampered, bytes.NewReader([]byte("payload")))
		if !errors.Is(err, ErrBadGlobalSignature) {
			t.Fatalf("err = %v, want ErrBadGlobalSignature", err)
		}
	})

	t.Run("rewrite file name in trusted comment", func(t *testing.T) {
		tampered := *sig
		tampered.TrustedComment = trustedComment("evil.tar.gz", now, newVersion)
		err := Verify(pk, &tampered, bytes.NewReader([]byte("payload")))
		if !errors.Is(err, ErrBadGlobalSignature) {
			t.Fatalf("err = %v, want ErrBadGlobalSignature", err)
		}
	})

	t.Run("untrusted comment rewrite stays display-only", func(t *testing.T) {
		// The untrusted comment is not signed: rewriting it must not break
		// verification, and decisions must still come from the trusted comment.
		tampered := *sig
		tampered.UntrustedComment = "version: 99.99.99 (forged display text)"
		update, err := NewVerifier(pk, time.Time{}).
			VerifyUpdate(targetFile, &tampered, bytes.NewReader([]byte("payload")))
		if err != nil {
			t.Fatalf("untrusted comment rewrite must not affect verification: %v", err)
		}
		if update.Version != newVersion {
			t.Fatalf("decision used untrusted comment: version = %q", update.Version)
		}
	})
}

// TestSwappedFileRejected ensures a valid signature cannot be replayed over a
// different package body.
func TestSwappedFileRejected(t *testing.T) {
	now := time.Now().Unix()
	pk, sig, content := signedFixture(t, AlgPrehashed, targetFile, now, newVersion, "genuine package")

	t.Run("different content", func(t *testing.T) {
		err := Verify(pk, sig, bytes.NewReader(append(append([]byte{}, content...), 'x')))
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v, want ErrBadSignature", err)
		}
	})

	t.Run("truncated content", func(t *testing.T) {
		err := Verify(pk, sig, bytes.NewReader(content[:len(content)-1]))
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v, want ErrBadSignature", err)
		}
	})

	t.Run("other key's signature", func(t *testing.T) {
		other := newTestKey(t)
		otherSig := mustParseSignature(t, other.sign(AlgPrehashed, content,
			trustedComment(targetFile, now, newVersion), "x"))
		err := Verify(pk, otherSig, bytes.NewReader(content))
		if !errors.Is(err, ErrKeyIDMismatch) {
			t.Fatalf("err = %v, want ErrKeyIDMismatch", err)
		}
	})
}

// TestOldPackageReplayRejected replays a genuinely signed but older package
// against a client that already accepted a newer release. The signature is
// valid, so only the timestamp watermark can stop the rollback.
func TestOldPackageReplayRejected(t *testing.T) {
	newTS := time.Now().Unix()
	oldTS := newTS - 86400

	key := newTestKey(t)
	pk := mustParsePublicKey(t, key.publicKeyFile())
	oldSig := mustParseSignature(t, key.sign(AlgPrehashed, []byte("old package"),
		trustedComment(targetFile, oldTS, oldVersion), "old"))
	newSig := mustParseSignature(t, key.sign(AlgPrehashed, []byte("new package"),
		trustedComment(targetFile, newTS, newVersion), "new"))

	verifier := NewVerifier(pk, time.Unix(newTS, 0))

	if _, err := verifier.VerifyUpdate(targetFile, oldSig, bytes.NewReader([]byte("old package"))); !errors.Is(err, ErrRollback) {
		t.Fatalf("old package replay err = %v, want ErrRollback", err)
	}

	// The watermark must not move backwards after a rejected replay.
	if accepted, ok := verifier.LastAccepted(); !ok || accepted.Unix() != newTS {
		t.Fatalf("watermark changed after rollback attempt: %v (%v)", accepted, ok)
	}

	// Same-timestamp release is not a rollback and remains acceptable.
	equalSig := mustParseSignature(t, key.sign(AlgPrehashed, []byte("equal package"),
		trustedComment(targetFile, newTS, newVersion+".1"), "equal"))
	if _, err := verifier.VerifyUpdate(targetFile, equalSig, bytes.NewReader([]byte("equal package"))); err != nil {
		t.Fatalf("equal-timestamp package rejected: %v", err)
	}

	// A genuinely newer package is accepted and moves the watermark forward.
	if _, err := verifier.VerifyUpdate(targetFile, newSig, bytes.NewReader([]byte("new package"))); err != nil {
		t.Fatalf("newer package rejected: %v", err)
	}
	if accepted, _ := verifier.LastAccepted(); accepted.Unix() != newTS {
		t.Fatalf("watermark = %d, want %d", accepted.Unix(), newTS)
	}
}

func TestFileNameMustMatchTarget(t *testing.T) {
	now := time.Now().Unix()
	pk, sig, content := signedFixture(t, AlgPrehashed, targetFile, now, newVersion, "payload")

	_, err := NewVerifier(pk, time.Time{}).VerifyUpdate("different-name.tar.gz", sig, bytes.NewReader(content))
	if !errors.Is(err, ErrFileNameMismatch) {
		t.Fatalf("err = %v, want ErrFileNameMismatch", err)
	}
	if _, err := NewVerifier(pk, time.Time{}).VerifyUpdate(targetFile, sig, bytes.NewReader(content)); err != nil {
		t.Fatalf("exact target name rejected: %v", err)
	}
}

func TestMalformedInputs(t *testing.T) {
	key := newTestKey(t)

	t.Run("bad public key", func(t *testing.T) {
		if _, err := ParsePublicKey(strings.NewReader("not a key file\n")); !errors.Is(err, ErrMalformedPublicKey) {
			t.Fatalf("err = %v, want ErrMalformedPublicKey", err)
		}
	})

	t.Run("public key bad base64", func(t *testing.T) {
		in := "untrusted comment: minisign public key x\n!!!not-base64!!!\n"
		if _, err := ParsePublicKey(strings.NewReader(in)); !errors.Is(err, ErrMalformedPublicKey) {
			t.Fatalf("err = %v, want ErrMalformedPublicKey", err)
		}
	})

	t.Run("public key wrong decoded length", func(t *testing.T) {
		in := "untrusted comment: minisign public key x\n" + base64.StdEncoding.EncodeToString([]byte("short")) + "\n"
		if _, err := ParsePublicKey(strings.NewReader(in)); !errors.Is(err, ErrMalformedPublicKey) {
			t.Fatalf("err = %v, want ErrMalformedPublicKey", err)
		}
	})

	t.Run("bad signature structure", func(t *testing.T) {
		if _, err := ParseSignature(strings.NewReader("untrusted comment: x\nAAAA\n")); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("err = %v, want ErrMalformedSignature", err)
		}
	})

	t.Run("signature bad base64 and short lines", func(t *testing.T) {
		bad := "untrusted comment: x\n!!!!\ntrusted comment: y\n" + base64.StdEncoding.EncodeToString(make([]byte, 10)) + "\n"
		if _, err := ParseSignature(strings.NewReader(bad)); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("err = %v, want ErrMalformedSignature", err)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		if _, err := ParsePublicKey(strings.NewReader("")); !errors.Is(err, ErrMalformedPublicKey) {
			t.Fatalf("err = %v, want ErrMalformedPublicKey", err)
		}
		if _, err := ParseSignature(strings.NewReader("")); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("err = %v, want ErrMalformedSignature", err)
		}
		if err := Verify(nil, nil, bytes.NewReader(nil)); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("err = %v, want ErrMalformedSignature", err)
		}
	})

	t.Run("unknown algorithm", func(t *testing.T) {
		pk := mustParsePublicKey(t, key.publicKeyFile())
		sig := mustParseSignature(t, key.sign(AlgPrehashed, []byte("x"),
			trustedComment(targetFile, 1, newVersion), "x"))
		sig.Algorithm = [2]byte{'X', 'Y'}
		err := Verify(pk, sig, bytes.NewReader([]byte("x")))
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
		}
	})

	t.Run("forged global signature", func(t *testing.T) {
		pk, sig, content := signedFixture(t, AlgPrehashed, targetFile, 1, newVersion, "payload")
		sig.GlobalSignature[0] ^= 0xff
		err := Verify(pk, sig, bytes.NewReader(content))
		if !errors.Is(err, ErrBadGlobalSignature) {
			t.Fatalf("err = %v, want ErrBadGlobalSignature", err)
		}
	})

	t.Run("nil verifier key", func(t *testing.T) {
		if _, err := NewVerifier(nil, time.Time{}).
			VerifyUpdate(targetFile, &Signature{}, bytes.NewReader(nil)); !errors.Is(err, ErrMalformedPublicKey) {
			t.Fatalf("err = %v, want ErrMalformedPublicKey", err)
		}
	})
}

func TestParseTrustedComment(t *testing.T) {
	t.Run("canonical unix timestamp", func(t *testing.T) {
		tc, err := ParseTrustedComment("file=app.tar.gz\ttimestamp=1700000000\tversion=1.2.3")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if tc.File != "app.tar.gz" || tc.Version != "1.2.3" || tc.Timestamp.Unix() != 1700000000 {
			t.Fatalf("unexpected comment: %+v", tc)
		}
	})

	t.Run("rfc3339 timestamp and extra fields", func(t *testing.T) {
		tc, err := ParseTrustedComment("file=a\ttimestamp=2026-01-02T15:04:05Z\tversion=9\tchannel=stable")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if tc.Timestamp.Year() != 2026 {
			t.Fatalf("timestamp not parsed: %+v", tc)
		}
	})

	for _, comment := range []string{
		"timestamp=1\tversion=1",                 // missing file
		"file=a\tversion=1",                      // missing timestamp
		"file=a\ttimestamp=1",                    // missing version
		"file=a\ttimestamp=-5\tversion=1",        // negative timestamp
		"file=a\ttimestamp=notatime\tversion=1",  // unparsable timestamp
		"file=a\ttimestamp=1\tversion=1\tfile=b", // duplicate key
		"file=\ttimestamp=1\tversion=1",          // empty value
	} {
		if _, err := ParseTrustedComment(comment); !errors.Is(err, ErrMalformedTrustedComment) {
			t.Fatalf("comment %q: err = %v, want ErrMalformedTrustedComment", comment, err)
		}
	}
}

func TestVerifyUpdateRejectsMalformedTrustedComment(t *testing.T) {
	key := newTestKey(t)
	pk := mustParsePublicKey(t, key.publicKeyFile())
	// The trusted comment is properly signed, so both signatures verify, but
	// its contents do not carry the required file/timestamp/version fields.
	sig := mustParseSignature(t, key.sign(AlgPrehashed, []byte("payload"),
		"this is not structured metadata", "display"))

	_, err := NewVerifier(pk, time.Time{}).VerifyUpdate(targetFile, sig, bytes.NewReader([]byte("payload")))
	if !errors.Is(err, ErrMalformedTrustedComment) {
		t.Fatalf("err = %v, want ErrMalformedTrustedComment", err)
	}
}
