package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestEntityExpansionLimit(t *testing.T) {
	billionLaughs := strings.Join([]string{
		"<!DOCTYPE lolz [",
		"<!ENTITY lol \"lol\">",
		"<!ENTITY lol2 \"&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;\">",
		"<!ENTITY lol3 \"&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;\">",
		"<!ENTITY lol4 \"&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;\">",
		"]><lolz>&lol4;</lolz>",
	}, "\n")

	if err := New(64, nil).Check([]byte(billionLaughs)); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("billion laughs: want ErrEntityLimit, got %v", err)
	}

	linear := `<!DOCTYPE root [<!ENTITY a "abcdefghij">]><root>&a;</root>`
	if err := New(10, nil).Check([]byte(linear)); err != nil {
		t.Fatalf("exact-limit document rejected: %v", err)
	}
	if err := New(9, nil).Check([]byte(linear)); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("one-byte-over document: want ErrEntityLimit, got %v", err)
	}

	if err := New(0, nil).Check([]byte(linear)); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("zero limit: want ErrEntityLimit, got %v", err)
	}
	if err := New(0, nil).Check([]byte(samlMetadata)); err != nil {
		t.Fatalf("zero limit must still accept no-DOCTYPE documents: %v", err)
	}
}

func TestRecursiveEntitiesAreRejected(t *testing.T) {
	docs := map[string]string{
		"direct self reference": `<!DOCTYPE root [<!ENTITY a "&a;">]><root>&a;</root>`,
		"mutual reference":      `<!DOCTYPE root [<!ENTITY a "&b;"><!ENTITY b "&a;">]><root>&a;</root>`,
		"three entity cycle":    `<!DOCTYPE root [<!ENTITY a "&b;"><!ENTITY b "&c;"><!ENTITY c "&a;">]><root>&a;</root>`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			err := New(1<<20, nil).Check([]byte(doc))
			if !errors.Is(err, ErrRecursiveEntity) {
				t.Fatalf("want ErrRecursiveEntity, got %v", err)
			}
		})
	}
}

func TestCharacterReferencesAreDecodedAndBounded(t *testing.T) {
	doc := `<!DOCTYPE root [<!ENTITY a "&#65;&#x42;&quot;&amp;">]><root>&a;</root>`
	guard := New(1<<20, nil)
	if err := guard.Check([]byte(doc)); err != nil {
		t.Fatalf("valid character references rejected: %v", err)
	}
	got := guard.Entities()
	if got["a"] != "AB\"&" {
		t.Fatalf("expansion = %q, want %q", got["a"], "AB\"&")
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE root [<!ENTITY a \"")
	for i := 0; i < 32; i++ {
		b.WriteString("&#65;")
	}
	b.WriteString("\">]><root>&a;</root>")
	if err := New(16, nil).Check([]byte(b.String())); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("want ErrEntityLimit for 32 char refs under a 16 byte budget, got %v", err)
	}
}

func TestObfuscatedDeclarations(t *testing.T) {
	docs := map[string]error{
		"unterminated char reference": ErrMalformedDTD,
		"surrogate char reference":    ErrMalformedDTD,
		"nested comment in value":     ErrMalformedDTD,
		"lowercase doctype":           ErrNoProlog,
		"bare percent in entity":      ErrParameterEntity,
		"undeclared entity":           ErrUndeclaredEntity,
		"truncated doctype":           ErrMalformedDTD,
	}
	raw := map[string]string{
		"unterminated char reference": `<!DOCTYPE root [<!ENTITY a "&#65">]><root/>`,
		"surrogate char reference":    `<!DOCTYPE root [<!ENTITY a "&#xD800;">]><root/>`,
		"nested comment in value":     "<!DOCTYPE root [<!ENTITY a \"xx<!-- nope -->yy\">]><root/>",
		"lowercase doctype":           `<!doctype root [<!entity a "x">]><root/>`,
		"bare percent in entity":      "<!DOCTYPE root [<!ENTITY a \"xx%yy\">]><root/>",
		"undeclared entity":           `<!DOCTYPE root [<!ENTITY a "&ghost;">]><root/>`,
		"truncated doctype":           `<!DOCTYPE root [<!ENTITY a "x">`,
	}
	for name, want := range docs {
		t.Run(name, func(t *testing.T) {
			err := New(1<<20, nil).Check([]byte(raw[name]))
			if !errors.Is(err, want) {
				t.Fatalf("want %v, got %v", want, err)
			}
		})
	}

	accepted := []string{
		// Comment between markup declarations in the subset.
		"<!DOCTYPE root [<!-- harmless --><!ENTITY a \"x\">]><root/>",
		// PI in the subset and around the declaration.
		"<!DOCTYPE root [<?app note?><!ENTITY a \"x\">]><root/>",
		// Character references used as entity content.
		`<!DOCTYPE root [<!ENTITY a "&#65;&#x42;&#10;">]><root>&a;</root>`,
	}
	for idx, doc := range accepted {
		if err := New(1<<20, nil).Check([]byte(doc)); err != nil {
			t.Fatalf("accepted variant %d rejected: %v", idx, err)
		}
	}
}
