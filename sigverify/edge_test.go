package sigverify

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestParseBundleRejectsBadBase64AndShapes(t *testing.T) {
	f := newValidFixture(t)
	valid, err := json.Marshal(f.bundle())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr error
	}{
		{
			name: "invalid base64 signature",
			mutate: func(m map[string]any) {
				m["artifactSignature"] = "not-base64!!"
			},
			wantErr: ErrMalformedBundle,
		},
		{
			name: "unsupported digest algorithm",
			mutate: func(m map[string]any) {
				m["artifactDigest"] = map[string]any{
					"algorithm": "SHA-512",
					"value":     base64.StdEncoding.EncodeToString(make([]byte, 32)),
				}
			},
			wantErr: ErrMalformedBundle,
		},
		{
			name: "empty leaf certificate",
			mutate: func(m map[string]any) {
				m["leafCertificateDER"] = ""
			},
			wantErr: ErrMalformedBundle,
		},
		{
			name: "zero integrated time",
			mutate: func(m map[string]any) {
				m["integratedTime"] = 0
			},
			wantErr: ErrMalformedBundle,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(valid, &raw); err != nil {
				t.Fatal(err)
			}
			tc.mutate(raw)
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseBundle(data); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyRejectsEmptyTrustAnchors(t *testing.T) {
	f := newValidFixture(t)

	noRoots := f.trust()
	noRoots.FulcioRoots = nil
	if _, err := Verify(f.bundle(), f.artifact, noRoots, f.policy()); !errors.Is(err, ErrCertificateChain) {
		t.Fatalf("got %v, want ErrCertificateChain", err)
	}

	noLogKey := f.trust()
	noLogKey.RekorPublicKey = nil
	if _, err := Verify(f.bundle(), f.artifact, noLogKey, f.policy()); !errors.Is(err, ErrCheckpointSignature) {
		t.Fatalf("got %v, want ErrCheckpointSignature", err)
	}
}

func TestExtractSANIdentitiesEmail(t *testing.T) {
	f := newValidFixture(t)
	opts := defaultLeafOptions(f.integratedAt.Add(-2 * time.Minute))
	opts.uriSAN = ""
	opts.emailSAN = "builder@example.com"
	cert, _, _ := mintLeaf(t, f.intermediate, opts)

	identities, err := ExtractSANIdentities(cert)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0] != "builder@example.com" {
		t.Fatalf("identities = %v", identities)
	}

	if _, err := ExtractSANIdentities(nil); !errors.Is(err, ErrSubject) {
		t.Fatalf("nil cert: got %v, want ErrSubject", err)
	}
	if _, err := ExtractOIDCIssuer(nil); !errors.Is(err, ErrIssuer) {
		t.Fatalf("nil cert issuer: got %v, want ErrIssuer", err)
	}
}

func TestVerifyRejectsMalformedArtifactSignature(t *testing.T) {
	f := newValidFixture(t)
	if err := verifyArtifactSignature(f.leafCert, f.artifact, []byte{0x01, 0x02}); !errors.Is(err, ErrArtifactSignature) {
		t.Fatalf("got %v, want ErrArtifactSignature", err)
	}
}

func TestCheckpointMalformedVariants(t *testing.T) {
	f := newValidFixture(t)
	valid := f.checkpoint

	if _, err := ParseCheckpoint(append(cloneBytes(valid), 0)); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("got %v, want ErrCheckpoint", err)
	}

	// Replace the em-dash signature separator with an illegal marker while
	// preserving the five-line envelope length.
	badSep := cloneBytes(valid)
	idx := bytesLastIndex(badSep, []byte(signatureSeparator))
	if idx < 0 {
		t.Fatal("signature separator not found")
	}
	badSep[idx] = 'x'
	if _, err := ParseCheckpoint(badSep); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("got %v, want ErrCheckpoint", err)
	}
}

func bytesLastIndex(haystack, needle []byte) int {
	for i := len(haystack) - len(needle); i >= 0; i-- {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestInclusionProofHashLengthErrors(t *testing.T) {
	f := newValidFixture(t)
	hashes, err := f.bundle().InclusionProof.proofHashes()
	if err != nil {
		t.Fatal(err)
	}

	short := append([][]byte{}, hashes...)
	short[0] = make([]byte, 31)
	if err := VerifyInclusion(f.logIndex, f.treeSize, f.leafHashes[f.logIndex], f.rootHash, short); !errors.Is(err, ErrProofHash) {
		t.Fatalf("got %v, want ErrProofHash", err)
	}

	if err := VerifyInclusion(f.logIndex, f.treeSize, make([]byte, 10), f.rootHash, hashes); !errors.Is(err, ErrProofHash) {
		t.Fatalf("got %v, want ErrProofHash", err)
	}

	if constantTimeEqual(make([]byte, 1), make([]byte, 2)) {
		t.Fatal("expected inequality on differing lengths")
	}
	digest := sha256.Sum256([]byte("x"))
	if !constantTimeEqual(digest[:], digest[:]) {
		t.Fatal("expected equality for identical hashes")
	}
}
