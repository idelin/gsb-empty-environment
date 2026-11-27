package samlsec

import (
	"errors"
	"testing"
)

func TestParse_NamespaceConfusionRejected(t *testing.T) {
	t.Run("forged namespace assertion", func(t *testing.T) {
		doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:evil="urn:evil" ID="_r1" Version="2.0">
			<evil:Assertion ID="_e1"><evil:Issuer>x</evil:Issuer></evil:Assertion>
		</samlp:Response>`
		_, err := Parse([]byte(doc), nil)
		if !errors.Is(err, ErrNamespace) {
			t.Fatalf("expected ErrNamespace, got %v", err)
		}
	})

	t.Run("forged namespace signature", func(t *testing.T) {
		doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:evil="urn:evil" ID="_r1" Version="2.0">
			<evil:Signature/>
		</samlp:Response>`
		_, err := Parse([]byte(doc), nil)
		if !errors.Is(err, ErrNamespace) {
			t.Fatalf("expected ErrNamespace, got %v", err)
		}
	})
}

func TestParse_CommentInForeignNameIDNotTrusted(t *testing.T) {
	// 异命名空间的 NameID 不享受 SAML 语义，但其中注释也不能影响真正的
	// SAML NameID 提取；真正的 SAML NameID 仍受敏感保护。
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	subject := root.FindDescendant(NSSAML, "Subject")
	foreign := elem("urn:evil", "NameID", "evil",
		nsAttr("evil", "urn:evil"))
	adopt(foreign, text("admin"), comment(" "), text("@evil.com"))
	adopt(subject, foreign)

	parsed, err := Parse([]byte(serializeXML(root)), nil)
	if err != nil {
		t.Fatalf("foreign NameID comment is structurally legal: %v", err)
	}
	got := parsed.Root.FindDescendant(NSSAML, "NameID").TextContent()
	if got != "alice@example.com" {
		t.Fatalf("trusted NameID must remain untouched, got %q", got)
	}
}
