package netsec

import (
	"errors"
	"testing"
)

func TestReadNameIDPlainText(t *testing.T) {
	t.Parallel()

	const input = `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">admin@victim.com.evil.com</saml:NameID></saml:Subject></samlp:Response>`
	got, err := ReadNameID([]byte(input))
	if err != nil {
		t.Fatalf("ReadNameID() error = %v", err)
	}
	if want := "admin@victim.com.evil.com"; got != want {
		t.Fatalf("ReadNameID() = %q, want %q", got, want)
	}
}

func TestReadNameIDRejectsCommentSplit(t *testing.T) {
	t.Parallel()

	const input = `<saml:NameID xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">admin@victim.com<!-- attacker-controlled -->.evil.com</saml:NameID>`
	_, err := ReadNameID([]byte(input))
	if !errors.Is(err, ErrNameIDComment) {
		t.Fatalf("ReadNameID() error = %v, want %v", err, ErrNameIDComment)
	}
}

func TestReadNameIDRejectsCDATA(t *testing.T) {
	t.Parallel()

	tests := []string{
		`<NameID><![CDATA[admin@victim.com.evil.com]]></NameID>`,
		`<NameID>admin@victim.com<![CDATA[.evil.com]]></NameID>`,
		`<NameID>admin@victim.com<![CDATA[]]>.evil.com</NameID>`,
	}
	for _, input := range tests {
		_, err := ReadNameID([]byte(input))
		if !errors.Is(err, ErrNameIDCDATA) {
			t.Errorf("ReadNameID(%q) error = %v, want %v", input, err, ErrNameIDCDATA)
		}
	}
}

func TestReadNameIDAcceptsCommentOutsideElement(t *testing.T) {
	t.Parallel()

	const input = `<!-- before document --><Subject><!-- before NameID --><NameID>user@example.com</NameID><!-- after NameID --></Subject><!-- after document -->`
	got, err := ReadNameID([]byte(input))
	if err != nil {
		t.Fatalf("ReadNameID() error = %v", err)
	}
	if want := "user@example.com"; got != want {
		t.Fatalf("ReadNameID() = %q, want %q", got, want)
	}
}

func TestReadNameIDCDATAOutsideElementIsIgnored(t *testing.T) {
	t.Parallel()

	const input = `<Subject><![CDATA[not the NameID]]><NameID>user@example.com</NameID></Subject>`
	got, err := ReadNameID([]byte(input))
	if err != nil {
		t.Fatalf("ReadNameID() error = %v", err)
	}
	if want := "user@example.com"; got != want {
		t.Fatalf("ReadNameID() = %q, want %q", got, want)
	}
}

func TestReadNameIDWhitespace(t *testing.T) {
	t.Parallel()

	accepted := []string{
		`<NameID>user name@example.com</NameID>`,
		`<NameID>user@example.com</NameID>`,
	}
	for _, input := range accepted {
		if _, err := ReadNameID([]byte(input)); err != nil {
			t.Errorf("ReadNameID(%q) unexpected error = %v", input, err)
		}
	}

	rejected := []string{
		`<NameID> user@example.com</NameID>`,
		`<NameID>user@example.com </NameID>`,
		"<NameID>\tuser@example.com</NameID>",
		"<NameID>user@example.com\n</NameID>",
	}
	for _, input := range rejected {
		_, err := ReadNameID([]byte(input))
		if !errors.Is(err, ErrNameIDWhitespace) {
			t.Errorf("ReadNameID(%q) error = %v, want %v", input, err, ErrNameIDWhitespace)
		}
	}
}

func TestReadNameIDEntities(t *testing.T) {
	t.Parallel()

	const input = `<NameID>admin&#64;victim.com&amp;ops.evil.com</NameID>`
	got, err := ReadNameID([]byte(input))
	if err != nil {
		t.Fatalf("ReadNameID() error = %v", err)
	}
	if want := "admin@victim.com&ops.evil.com"; got != want {
		t.Fatalf("ReadNameID() = %q, want %q", got, want)
	}
}

func TestReadNameIDRejectsAmbiguity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  error
	}{
		{
			name:  "multiple NameIDs",
			input: `<Subject><NameID>one@example.com</NameID><NameID>two@example.com</NameID></Subject>`,
			want:  ErrNameIDMultiple,
		},
		{
			name:  "nested element",
			input: `<NameID><part>user@example.com</part></NameID>`,
			want:  ErrNameIDNestedElement,
		},
		{
			name:  "processing instruction",
			input: `<NameID>user<?split?>@example.com</NameID>`,
			want:  ErrNameIDProcessingInstruction,
		},
		{
			name:  "empty",
			input: `<NameID></NameID>`,
			want:  ErrNameIDEmpty,
		},
		{
			name:  "missing",
			input: `<Subject></Subject>`,
			want:  ErrNameIDNotFound,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadNameID([]byte(test.input))
			if !errors.Is(err, test.want) {
				t.Fatalf("ReadNameID() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestReadNameIDRejectsMalformedXML(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`<NameID>user@example.com`,
		`<NameID>user@example.com</Other>`,
		`<NameID>one</NameID><NameID>two</NameID>`,
		`plain text`,
	}
	for _, input := range inputs {
		_, err := ReadNameID([]byte(input))
		if !errors.Is(err, ErrMalformedXML) {
			t.Errorf("ReadNameID(%q) error = %v, want wrapped %v", input, err, ErrMalformedXML)
		}
	}
}
