package sigverify

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func newValidFixture(t *testing.T) *fixture {
	t.Helper()
	return buildFixture(t, 13, 7)
}

func TestVerifyAcceptsValidBundle(t *testing.T) {
	f := newValidFixture(t)
	result, err := Verify(f.bundle(), f.artifact, f.trust(), f.policy())
	if err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
	if result.OIDCIssuer != testIssuer {
		t.Fatalf("issuer = %q, want %q", result.OIDCIssuer, testIssuer)
	}
	if len(result.Subjects) != 1 || result.Subjects[0] != testSubject {
		t.Fatalf("subjects = %v, want [%s]", result.Subjects, testSubject)
	}
	if !result.IntegratedTime.Equal(f.integratedAt) {
		t.Fatalf("integrated time = %v, want %v", result.IntegratedTime, f.integratedAt)
	}

	t.Run("json round trip", func(t *testing.T) {
		raw, err := json.Marshal(f.bundle())
		if err != nil {
			t.Fatalf("marshal bundle: %v", err)
		}
		parsed, err := ParseBundle(raw)
		if err != nil {
			t.Fatalf("parse bundle: %v", err)
		}
		if _, err := Verify(parsed, f.artifact, f.trust(), f.policy()); err != nil {
			t.Fatalf("round-tripped bundle rejected: %v", err)
		}
	})

	t.Run("without optional SET", func(t *testing.T) {
		b := f.bundle()
		b.SET = nil
		if _, err := Verify(b, f.artifact, f.trust(), f.policy()); err != nil {
			t.Fatalf("bundle without SET rejected: %v", err)
		}
	})
}

func TestVerifyAcceptsV2IssuerExtension(t *testing.T) {
	f := newValidFixture(t)
	opts := defaultLeafOptions(f.integratedAt.Add(-2 * time.Minute))
	opts.issuerV2 = true
	leafCert, leafKey, leafDER := mintLeaf(t, f.intermediate, opts)
	f.leafCert, f.leafKey, f.leafDER = leafCert, leafKey, leafDER

	digest := sha256.Sum256(f.artifact)
	signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("re-sign artifact: %v", err)
	}
	f.signature = signature
	f.entry.CertificateDER = leafDER
	f.entry.Signature = signature
	f.rebuildTree(t)

	issuer, err := ExtractOIDCIssuer(leafCert)
	if err != nil || issuer != testIssuer {
		t.Fatalf("V2 issuer extraction = %q, %v", issuer, err)
	}
	if _, err := Verify(f.bundle(), f.artifact, f.trust(), f.policy()); err != nil {
		t.Fatalf("V2 issuer bundle rejected: %v", err)
	}
}

func TestVerifyValidityWindowBoundariesInclusive(t *testing.T) {
	for _, delta := range []time.Duration{0, 10 * time.Minute} {
		f := newValidFixture(t)
		f.integratedAt = f.leafCert.NotBefore.Add(delta)
		f.rebuildTree(t)
		if _, err := Verify(f.bundle(), f.artifact, f.trust(), f.policy()); err != nil {
			t.Fatalf("boundary delta=%s rejected: %v", delta, err)
		}
	}
}

func TestVerifyRejectsAttackSamples(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(t *testing.T, f *fixture)
		want           error
		policyOverride *Policy
	}{
		{
			name: "forged merkle path hash",
			mutate: func(t *testing.T, f *fixture) {
				f.proof[0] = flipByte(cloneBytes(f.proof[0]))
			},
			want: ErrRootMismatch,
		},
		{
			name: "truncated merkle path",
			mutate: func(t *testing.T, f *fixture) {
				f.proof = f.proof[:len(f.proof)-1]
			},
			want: ErrProofLength,
		},
		{
			name: "extra merkle node",
			mutate: func(t *testing.T, f *fixture) {
				f.proof = append(f.proof, make([]byte, sha256.Size))
			},
			want: ErrProofLength,
		},
		{
			name: "illegal log index equals tree size",
			mutate: func(t *testing.T, f *fixture) {
				f.logIndex = f.treeSize
			},
			want: ErrProofIndex,
		},
		{
			name: "checkpoint root tampered",
			mutate: func(t *testing.T, f *fixture) {
				root := flipByte(cloneBytes(f.rootHash))
				f.checkpoint = encodeCheckpoint(t, testOrigin, f.treeSize, root, f.logPriv)
			},
			want: ErrRootMismatch,
		},
		{
			name: "checkpoint signed by attacker key",
			mutate: func(t *testing.T, f *fixture) {
				_, attackerPriv, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				f.checkpoint = encodeCheckpoint(t, testOrigin, f.treeSize, f.rootHash, attackerPriv)
			},
			want: ErrCheckpointSignature,
		},
		{
			name: "checkpoint origin mismatch",
			mutate: func(t *testing.T, f *fixture) {
				f.checkpoint = encodeCheckpoint(t, "evil-rekor.attacker", f.treeSize, f.rootHash, f.logPriv)
			},
			want: ErrCheckpointOrigin,
		},
		{
			name: "forged SET signature",
			mutate: func(t *testing.T, f *fixture) {
				_, attackerPriv, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				f.set = signSET(t, f.logIndex, f.integratedAt.Unix(), f.leafHashes[f.logIndex], attackerPriv)
			},
			want: ErrSETSignature,
		},
		{
			name: "integrated time after certificate expiry",
			mutate: func(t *testing.T, f *fixture) {
				f.integratedAt = f.leafCert.NotAfter.Add(time.Second)
				f.rebuildTree(t)
			},
			want: ErrTimeWindow,
		},
		{
			name: "integrated time before certificate start",
			mutate: func(t *testing.T, f *fixture) {
				f.integratedAt = f.leafCert.NotBefore.Add(-time.Second)
				f.rebuildTree(t)
			},
			want: ErrTimeWindow,
		},
		{
			name:   "oidc issuer not in allowlist",
			mutate: func(t *testing.T, f *fixture) {},
			want:   ErrIssuer,
			policyOverride: &Policy{
				AllowedOIDCIssuers: []string{"https://evil-issuer.attacker"},
				AllowedSubjects:    []string{testSubject},
			},
		},
		{
			name: "certificate carries spoofed issuer extension",
			mutate: func(t *testing.T, f *fixture) {
				opts := defaultLeafOptions(f.integratedAt.Add(-2 * time.Minute))
				leafCert, leafKey, leafDER := mintLeafWithIssuer(t, f.intermediate, opts, "https://evil-issuer.attacker")
				f.leafCert, f.leafKey, f.leafDER = leafCert, leafKey, leafDER
				digest := sha256.Sum256(f.artifact)
				signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
				if err != nil {
					t.Fatal(err)
				}
				f.signature = signature
				f.entry.CertificateDER = leafDER
				f.entry.Signature = signature
				f.rebuildTree(t)
			},
			want: ErrIssuer,
		},
		{
			name:   "san subject not in allowlist",
			mutate: func(t *testing.T, f *fixture) {},
			want:   ErrSubject,
			policyOverride: &Policy{
				AllowedOIDCIssuers: []string{testIssuer},
				AllowedSubjects:    []string{"spiffe://other/identity"},
			},
		},
		{
			name: "log entry digest swapped",
			mutate: func(t *testing.T, f *fixture) {
				fakeDigest := sha256.Sum256([]byte("malicious-artifact"))
				f.entry.ArtifactDigest = Digest{Algorithm: "SHA-256", Value: fakeDigest[:]}
				f.rebuildTree(t)
			},
			want: ErrLogEntryBindingMismatch,
		},
		{
			name: "bundle digest swapped against unchanged artifact",
			mutate: func(t *testing.T, f *fixture) {
				fakeDigest := sha256.Sum256([]byte("malicious-artifact"))
				f.artifactHash = fakeDigest[:]
			},
			want: ErrArtifactDigestMismatch,
		},
		{
			name: "log entry signature swapped",
			mutate: func(t *testing.T, f *fixture) {
				f.entry.Signature = flipByte(cloneBytes(f.entry.Signature))
				f.rebuildTree(t)
			},
			want: ErrLogEntryBindingMismatch,
		},
		{
			name: "log entry certificate replaced",
			mutate: func(t *testing.T, f *fixture) {
				_, _, rogueDER := mintLeaf(t, f.intermediate, defaultLeafOptions(f.integratedAt.Add(-2*time.Minute)))
				f.entry.CertificateDER = rogueDER
				f.rebuildTree(t)
			},
			want: ErrLogEntryBindingMismatch,
		},
		{
			name: "artifact signature invalid",
			mutate: func(t *testing.T, f *fixture) {
				// Flip the signature in both the bundle and the log entry so
				// the binding check passes and only ECDSA verification fails.
				bad := flipByte(cloneBytes(f.signature))
				f.signature = bad
				f.entry.Signature = cloneBytes(bad)
				f.rebuildTree(t)
			},
			want: ErrArtifactSignature,
		},
		{
			name: "leaf missing code signing EKU",
			mutate: func(t *testing.T, f *fixture) {
				opts := defaultLeafOptions(f.integratedAt.Add(-2 * time.Minute))
				opts.codeSigning = false
				leafCert, leafKey, leafDER := mintLeaf(t, f.intermediate, opts)
				f.leafCert, f.leafKey, f.leafDER = leafCert, leafKey, leafDER
				digest := sha256.Sum256(f.artifact)
				signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
				if err != nil {
					t.Fatal(err)
				}
				f.signature = signature
				f.entry.CertificateDER = leafDER
				f.entry.Signature = signature
				f.rebuildTree(t)
			},
			want: ErrEKU,
		},
		{
			name: "untrusted root ca",
			mutate: func(t *testing.T, f *fixture) {
				f.root = mintCA(t, nil, true)
			},
			want: ErrCertificateChain,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newValidFixture(t)
			tc.mutate(t, f)
			policy := f.policy()
			if tc.policyOverride != nil {
				policy = *tc.policyOverride
			}
			_, err := Verify(f.bundle(), f.artifact, f.trust(), policy)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseBundleRejectsMalformedInput(t *testing.T) {
	f := newValidFixture(t)

	t.Run("trailing json value", func(t *testing.T) {
		raw, _ := json.Marshal(f.bundle())
		raw = append(raw, []byte("{}")...)
		if _, err := ParseBundle(raw); !errors.Is(err, ErrMalformedBundle) {
			t.Fatalf("got %v, want ErrMalformedBundle", err)
		}
	})

	t.Run("bad version", func(t *testing.T) {
		b := f.bundle()
		b.Version = "other"
		raw, _ := json.Marshal(b)
		if _, err := ParseBundle(raw); !errors.Is(err, ErrMalformedBundle) {
			t.Fatalf("got %v, want ErrMalformedBundle", err)
		}
	})

	t.Run("index out of range", func(t *testing.T) {
		b := f.bundle()
		b.InclusionProof.LogIndex = b.InclusionProof.TreeSize
		raw, _ := json.Marshal(b)
		if _, err := ParseBundle(raw); !errors.Is(err, ErrProofIndex) {
			t.Fatalf("got %v, want ErrProofIndex", err)
		}
	})
}

func TestCheckpointParserRejectsMalformedNotes(t *testing.T) {
	f := newValidFixture(t)
	valid := f.checkpoint

	if _, err := ParseCheckpoint(valid); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	if _, err := ParseCheckpoint(append(cloneBytes(valid), []byte("extra\n")...)); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("got %v, want ErrCheckpoint", err)
	}
	if _, err := ParseCheckpoint(valid[:len(valid)-1]); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("got %v, want ErrCheckpoint", err)
	}
}
