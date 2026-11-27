package samlsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 11, 28, 6, 0, 0, 0, time.UTC)
}

func TestValidate_HappyPath_AssertionSigned_RSA(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))

	got, err := Validate(raw, baseOptions(key, now))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got.Subject != "alice@example.com" {
		t.Fatalf("subject = %q", got.Subject)
	}
	if got.Issuer != testIDP || got.ResponseIssuer != testIDP {
		t.Fatalf("issuer mismatch: %+v", got)
	}
	if got.AssertionID != "_assertion-1" || got.ResponseID != "_response-1" {
		t.Fatalf("id mismatch: %+v", got)
	}
	if len(got.Audiences) != 1 || got.Audiences[0] != testAudience {
		t.Fatalf("audiences = %v", got.Audiences)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Name != "role" ||
		len(got.Attributes[0].Values) != 1 || got.Attributes[0].Values[0] != "user" {
		t.Fatalf("attributes = %+v", got.Attributes)
	}
}

func TestValidate_HappyPath_ResponseSigned(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cfg := defaultFixture(now)
	cfg.signResponse = true
	raw := signedResponseBytes(key, cfg)

	got, err := Validate(raw, baseOptions(key, now))
	if err != nil {
		t.Fatalf("response-signed validation failed: %v", err)
	}
	if got.Subject != "alice@example.com" {
		t.Fatalf("subject = %q", got.Subject)
	}
}

func TestValidate_HappyPath_ECDSA_P256(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := &testKey{ec: ec}
	now := fixedNow()

	root := buildResponse(defaultFixture(now))
	sig := root.FindChild(NSSAML, "Assertion").FindChild(NSDSIG, "Signature")
	sig.FindDescendant(NSDSIG, "SignatureMethod").Attrs[0].Value = AlgoECDSASHA256
	key.sign(root.FindChild(NSSAML, "Assertion"))
	raw := []byte(serializeXML(root))

	if _, err := Validate(raw, baseOptions(key, now)); err != nil {
		t.Fatalf("ecdsa validation failed: %v", err)
	}
}

func TestValidate_TamperedContentFailsDigest(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))

	// 签名后篡改 NameID，DigestValue 必须失配。
	tampered := replaceFirst(raw, "alice@example.com", "attacker@example.com")
	_, err := Validate(tampered, baseOptions(key, now))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

func TestValidate_TamperedSignatureValue(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	doc := parseTree(signedResponseBytes(key, defaultFixture(now)))
	value := doc.Root.FindDescendant(NSDSIG, "SignatureValue")
	value.Children[0].Text = "AAAAAAAAAAAAAAAA"
	raw := []byte(serializeXML(doc.Root))

	_, err := Validate(raw, baseOptions(key, now))
	if !errors.Is(err, ErrSignatureValueMismatch) {
		t.Fatalf("expected ErrSignatureValueMismatch, got %v", err)
	}
}

func TestValidate_WrongTrustedKey(t *testing.T) {
	key := loadTestKey()
	other := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))

	opts := baseOptions(other, now)
	_, err := Validate(raw, opts)
	if !errors.Is(err, ErrSignatureValueMismatch) {
		t.Fatalf("expected ErrSignatureValueMismatch, got %v", err)
	}
}

func TestValidate_ReplayRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cache := NewMemoryReplayCache(time.Hour)
	raw := signedResponseBytes(key, defaultFixture(now))

	opts := baseOptions(key, now)
	opts.Replay = cache
	if _, err := Validate(raw, opts); err != nil {
		t.Fatalf("first use failed: %v", err)
	}
	_, err := Validate(raw, opts)
	if !errors.Is(err, ErrReplay) {
		t.Fatalf("expected ErrReplay, got %v", err)
	}
}

func TestValidate_TimeWindow(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()

	t.Run("expired", func(t *testing.T) {
		cfg := defaultFixture(now)
		cfg.notOnOrAfter = now.Add(-time.Minute)
		raw := signedResponseBytes(key, cfg)
		_, err := Validate(raw, baseOptions(key, now))
		if !errors.Is(err, ErrCondition) {
			t.Fatalf("expected ErrCondition, got %v", err)
		}
	})
	t.Run("not yet valid within skew", func(t *testing.T) {
		cfg := defaultFixture(now)
		cfg.notBefore = now.Add(10 * time.Second)
		raw := signedResponseBytes(key, cfg)
		if _, err := Validate(raw, baseOptions(key, now)); err != nil {
			t.Fatalf("skew window should accept: %v", err)
		}
	})
	t.Run("not yet valid beyond skew", func(t *testing.T) {
		cfg := defaultFixture(now)
		cfg.notBefore = now.Add(5 * time.Minute)
		raw := signedResponseBytes(key, cfg)
		_, err := Validate(raw, baseOptions(key, now))
		if !errors.Is(err, ErrCondition) {
			t.Fatalf("expected ErrCondition, got %v", err)
		}
	})
}

func TestValidate_AudienceRecipientInResponseTo(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))

	t.Run("wrong audience", func(t *testing.T) {
		opts := baseOptions(key, now)
		opts.ExpectedAudience = "https://evil.example.com/"
		_, err := Validate(raw, opts)
		if !errors.Is(err, ErrCondition) {
			t.Fatalf("expected ErrCondition, got %v", err)
		}
	})
	t.Run("wrong recipient", func(t *testing.T) {
		opts := baseOptions(key, now)
		opts.ExpectedRecipient = "https://evil.example.com/acs"
		_, err := Validate(raw, opts)
		if !errors.Is(err, ErrSubjectConfirmation) {
			t.Fatalf("expected ErrSubjectConfirmation, got %v", err)
		}
	})
	t.Run("wrong in-response-to", func(t *testing.T) {
		opts := baseOptions(key, now)
		opts.ExpectedInResponseTo = "_other-request"
		_, err := Validate(raw, opts)
		if !errors.Is(err, ErrSubjectConfirmation) {
			t.Fatalf("expected ErrSubjectConfirmation, got %v", err)
		}
	})
	t.Run("bearer expired", func(t *testing.T) {
		cfg := defaultFixture(now)
		cfg.bearerExpire = now.Add(-time.Minute)
		expired := signedResponseBytes(key, cfg)
		_, err := Validate(expired, baseOptions(key, now))
		if !errors.Is(err, ErrSubjectConfirmation) {
			t.Fatalf("expected ErrSubjectConfirmation, got %v", err)
		}
	})
}

func TestValidate_IssuerMismatch(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))

	opts := baseOptions(key, now)
	opts.ExpectedIssuer = "https://rogue.example.com/"
	_, err := Validate(raw, opts)
	if !errors.Is(err, ErrCondition) {
		t.Fatalf("expected ErrCondition, got %v", err)
	}
}

func TestValidate_WeakAlgorithmsRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()

	t.Run("sha1 digest", func(t *testing.T) {
		doc := parseTree(signedResponseBytes(key, defaultFixture(now)))
		node := doc.Root.FindDescendant(NSDSIG, "DigestMethod")
		node.Attrs[0].Value = "http://www.w3.org/2000/09/xmldsig#sha1"
		_, err := Validate([]byte(serializeXML(doc.Root)), baseOptions(key, now))
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("expected ErrUnsupportedAlgorithm, got %v", err)
		}
	})
	t.Run("inclusive c14n", func(t *testing.T) {
		doc := parseTree(signedResponseBytes(key, defaultFixture(now)))
		node := doc.Root.FindDescendant(NSDSIG, "CanonicalizationMethod")
		node.Attrs[0].Value = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315"
		_, err := Validate([]byte(serializeXML(doc.Root)), baseOptions(key, now))
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("expected ErrUnsupportedAlgorithm, got %v", err)
		}
	})
}

// replaceFirst 做最小字符串替换，用于签名后篡改。
func replaceFirst(raw []byte, old, new string) []byte {
	s := string(raw)
	idx := indexOf(s, old)
	if idx < 0 {
		panic("replacement target not found")
	}
	return []byte(s[:idx] + new + s[idx+len(old):])
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
