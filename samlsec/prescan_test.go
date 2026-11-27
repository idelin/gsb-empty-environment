package samlsec

import (
	"errors"
	"testing"
)

func TestPrescan_XMLDeclarationAllowed(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0">
  <saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>idp</saml:Issuer></saml:Assertion>
</samlp:Response>`)
	if _, err := Parse(doc, nil); err != nil {
		t.Fatalf("xml declaration must be accepted: %v", err)
	}
}

func TestPrescan_CommentWithGT(t *testing.T) {
	doc := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><!-- a > b --><saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>idp</saml:Issuer></saml:Assertion></samlp:Response>`)
	if _, err := Parse(doc, nil); err != nil {
		t.Fatalf("comment containing > must be accepted outside sensitive nodes: %v", err)
	}
}

func TestPrescan_DOCTYPECaseInsensitive(t *testing.T) {
	doc := []byte(`<!doctype html><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0"/>`)
	_, err := Parse(doc, nil)
	if !errors.Is(err, ErrDTD) {
		t.Fatalf("lowercase doctype must be rejected, got %v", err)
	}
}

func TestPrescan_CDATA(t *testing.T) {
	doc := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><saml:Assertion ID="_a1" Version="2.0"><saml:Issuer><![CDATA[is<su-er> & co]]></saml:Issuer></saml:Assertion></samlp:Response>`)
	parsed, err := Parse(doc, nil)
	if err != nil {
		t.Fatalf("cdata must be accepted: %v", err)
	}
	got := parsed.Root.FindDescendant(NSSAML, "Issuer").TextContent()
	if got != "is<su-er> & co" {
		t.Fatalf("cdata text = %q", got)
	}
}

func TestPrescan_DTDVariants(t *testing.T) {
	docs := map[string]string{
		"element":  `<!ELEMENT x ANY><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0"/>`,
		"attlist":  `<!ATTLIST x id ID #IMPLIED><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0"/>`,
		"notation": `<!NOTATION gif SYSTEM "image/gif"><samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0"/>`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(doc), nil); !errors.Is(err, ErrDTD) {
				t.Fatalf("expected ErrDTD, got %v", err)
			}
		})
	}
}

func TestPrescan_LegalAmpersandInAttribute(t *testing.T) {
	doc := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><saml:Assertion ID="_a1" Version="2.0"><saml:Subject><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData Recipient="https://sp.example.com/acs?a=1&amp;b=2"/></saml:SubjectConfirmation></saml:Subject></saml:Assertion></samlp:Response>`)
	parsed, err := Parse(doc, nil)
	if err != nil {
		t.Fatalf("escaped ampersand in URL must be accepted: %v", err)
	}
	data := parsed.Root.FindDescendant(NSSAML, "SubjectConfirmationData")
	got, _ := data.AttrValue("", "Recipient")
	if got != "https://sp.example.com/acs?a=1&b=2" {
		t.Fatalf("recipient = %q", got)
	}
}

func TestPrescan_CustomEntityStillRejectedByDecoder(t *testing.T) {
	doc := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0">&hax;</samlp:Response>`)
	_, err := Parse(doc, nil)
	if err == nil {
		t.Fatal("custom entity without DTD must be rejected by decoder")
	}
	if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrDTD) {
		t.Fatalf("expected ErrMalformed/ErrDTD, got %v", err)
	}
}
