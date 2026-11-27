package samlsec

import (
	"errors"
	"testing"
	"time"
)

func TestValidate_MissingSignature(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	assertion := root.FindChild(NSSAML, "Assertion")
	removeChild(assertion, assertion.FindChild(NSDSIG, "Signature"))

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrSignatureNotFound) {
		t.Fatalf("expected ErrSignatureNotFound, got %v", err)
	}
}

func TestValidate_ReferenceToDisallowedPath(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()

	// 签名挂在 Assertion，却让 Reference 指向 Response：承载者与被签名
	// 元素不一致，必须按绑定错误拒绝。
	cfg := defaultFixture(now)
	root := buildResponse(cfg)
	assertion := root.FindChild(NSSAML, "Assertion")
	ref := root.FindDescendant(NSDSIG, "Reference")
	for _, a := range ref.Attrs {
		if a.Local == "URI" {
			a.Value = "#" + cfg.responseID
		}
	}
	key.sign(assertion)

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrReferenceBinding) {
		t.Fatalf("expected ErrReferenceBinding, got %v", err)
	}
}

func TestValidate_ReferenceToExtensionsElement(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	extensions := elem(NSSAMLP, "Extensions", "samlp", attr("", "ID", "", "_ext-1"))
	insertChildAt(root, extensions, 1)
	extensions.Parent = root

	ref := root.FindDescendant(NSDSIG, "Reference")
	for _, a := range ref.Attrs {
		if a.Local == "URI" {
			a.Value = "#_ext-1"
		}
	}

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrReferenceBinding) {
		t.Fatalf("expected ErrReferenceBinding for Extensions target, got %v", err)
	}
}

func TestValidate_DisallowedTransform(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	transforms := root.FindDescendant(NSDSIG, "Transforms")
	transforms.Children[0].Attrs[0].Value = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315"

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestValidate_NoAudienceRestriction(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cfg := defaultFixture(now)
	root := buildResponse(cfg)
	conditions := root.FindDescendant(NSSAML, "Conditions")
	removeChild(conditions, conditions.FindChild(NSSAML, "AudienceRestriction"))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrCondition) {
		t.Fatalf("expected ErrCondition, got %v", err)
	}
}

func TestValidate_TwoReferencesRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	signedInfo := root.FindDescendant(NSDSIG, "SignedInfo")
	extraRef := deepClone(signedInfo.FindChild(NSDSIG, "Reference"))
	adopt(signedInfo, extraRef)

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrReferenceBinding) {
		t.Fatalf("expected ErrReferenceBinding for multiple references, got %v", err)
	}
}

func TestValidate_ReferenceURIStrictForm(t *testing.T) {
	badURIs := []string{
		"#",
		"#xpointer(//*[local-name()='Assertion'])",
		"https://evil.example.com/doc#_assertion-1",
		"#_assertion-1?x=1",
		"#_a b",
	}
	for _, bad := range badURIs {
		t.Run(bad, func(t *testing.T) {
			key := loadTestKey()
			now := fixedNow()
			root := buildResponse(defaultFixture(now))
			key.sign(root.FindChild(NSSAML, "Assertion"))

			ref := root.FindDescendant(NSDSIG, "Reference")
			for _, a := range ref.Attrs {
				if a.Local == "URI" {
					a.Value = bad
				}
			}
			_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
			if !errors.Is(err, ErrReferenceBinding) {
				t.Fatalf("URI %q: expected ErrReferenceBinding, got %v", bad, err)
			}
		})
	}
}

func TestMemoryReplayCache_DedupAndTTL(t *testing.T) {
	c := NewMemoryReplayCache(50 * time.Millisecond)
	seen, err := c.SeenBefore("id-1")
	if err != nil || seen {
		t.Fatalf("first use must be fresh, seen=%v err=%v", seen, err)
	}
	seen, _ = c.SeenBefore("id-1")
	if !seen {
		t.Fatal("second use must be a replay")
	}
	time.Sleep(60 * time.Millisecond)
	seen, _ = c.SeenBefore("id-1")
	if seen {
		t.Fatal("entry must expire after ttl")
	}
}
