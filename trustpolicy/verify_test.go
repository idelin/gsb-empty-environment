package trustpolicy

import "testing"

const digest = "sha256:0123456789abcdef"

func goodSignature() Signature {
	return Signature{SignedDigest: digest}
}

func TestEvaluateLevels(t *testing.T) {
	cases := []struct {
		name    string
		level   Level
		sig     Signature
		allowed bool
	}{
		{"strict clean", LevelStrict, goodSignature(), true},
		{"strict expired", LevelStrict, Signature{SignedDigest: digest, Expired: true}, false},
		{"strict revocation unknown", LevelStrict, Signature{SignedDigest: digest, RevocationUnknown: true}, false},
		{"permissive clean", LevelPermissive, goodSignature(), true},
		{"permissive expired", LevelPermissive, Signature{SignedDigest: digest, Expired: true}, false},
		{"permissive revocation unknown tolerated", LevelPermissive, Signature{SignedDigest: digest, RevocationUnknown: true}, true},
		{"audit expired", LevelAudit, Signature{SignedDigest: digest, Expired: true}, true},
		{"audit revocation unknown", LevelAudit, Signature{SignedDigest: digest, RevocationUnknown: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Evaluate(c.level, c.sig, digest)
			if d.Allowed != c.allowed {
				t.Fatalf("Allowed = %v, want %v (findings %v)", d.Allowed, c.allowed, d.Findings)
			}
			wantFindings := c.sig.Expired || c.sig.RevocationUnknown
			if wantFindings && len(d.Findings) == 0 {
				t.Fatal("expected findings to be recorded")
			}
		})
	}
}

func TestEvaluatePermissiveStillRecordsRevocationUnknown(t *testing.T) {
	d := Evaluate(LevelPermissive, Signature{SignedDigest: digest, RevocationUnknown: true}, digest)
	if !d.Allowed {
		t.Fatal("permissive should tolerate unknown revocation")
	}
	if len(d.Findings) != 1 {
		t.Fatalf("Findings = %v, want the revocation finding recorded", d.Findings)
	}
}

func TestSignatureMustBindDigestNotTag(t *testing.T) {
	// A signature made over a tag is rejected at every level, even though
	// the tag is the one being deployed: tags are mutable.
	tagSig := Signature{SignedTag: "1.0"}
	for _, level := range []Level{LevelStrict, LevelPermissive} {
		d := Evaluate(level, tagSig, digest)
		if d.Allowed {
			t.Errorf("level %s admitted a tag-bound signature", level)
		}
	}
	// Audit admits but must record the binding problem.
	d := Evaluate(LevelAudit, tagSig, digest)
	if !d.Allowed || len(d.Findings) == 0 {
		t.Errorf("audit = %+v, want allowed with findings", d)
	}
}

func TestSignatureDigestMismatch(t *testing.T) {
	// A valid signature for a different image must not be replayed.
	sig := Signature{SignedDigest: "sha256:other"}
	for _, level := range []Level{LevelStrict, LevelPermissive} {
		if d := Evaluate(level, sig, digest); d.Allowed {
			t.Errorf("level %s admitted a signature for a different digest", level)
		}
	}
}
