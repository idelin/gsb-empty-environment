package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const (
	plainNameID = `<NameID>admin@victim.com.evil.com</NameID>`
	samlNameID  = `<saml:NameID xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">admin@victim.com.evil.com</saml:NameID>`
)

func TestParseNameIDAcceptsPlainFullText(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "fragment",
			xml:  plainNameID,
			want: "admin@victim.com.evil.com",
		},
		{
			name: "namespaced",
			xml:  samlNameID,
			want: "admin@victim.com.evil.com",
		},
		{
			name: "inside response",
			xml: `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol">
  <saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">
    <saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">admin@victim.com.evil.com</saml:NameID></saml:Subject>
  </saml:Assertion>
</samlp:Response>`,
			want: "admin@victim.com.evil.com",
		},
		{
			name: "character references are one text node",
			xml:  `<NameID>admin&#64;victim.com.evil.com</NameID>`,
			want: "admin@victim.com.evil.com",
		},
		{
			name: "comment outside NameID",
			xml: `<Response>
  <!-- attacker-controlled comment before the value -->
  <NameID>admin@victim.com.evil.com</NameID>
  <!-- attacker-controlled comment after the value -->
</Response>`,
			want: "admin@victim.com.evil.com",
		},
		{
			name: "comment in sibling element",
			xml:  `<Response><Other><!--x--></Other><NameID>admin@victim.com.evil.com</NameID></Response>`,
			want: "admin@victim.com.evil.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNameID([]byte(tt.xml))
			if err != nil {
				t.Fatalf("ParseNameID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseNameID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseNameIDRejectsMarkupInsideNameID(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want error
	}{
		{
			name: "comment split attack",
			xml:  `<NameID>admin@victim.com<!-- padding to hide the suffix -->.evil.com</NameID>`,
			want: ErrNameIDComment,
		},
		{
			name: "comment split attack with whitespace",
			xml:  `<NameID>admin@victim.com <!--x--> .evil.com</NameID>`,
			want: ErrNameIDComment,
		},
		{
			name: "CDATA carries suffix",
			xml:  `<NameID>admin@victim.com<![CDATA[.evil.com]]></NameID>`,
			want: ErrNameIDCDATA,
		},
		{
			name: "CDATA alone",
			xml:  `<NameID><![CDATA[admin@victim.com.evil.com]]></NameID>`,
			want: ErrNameIDCDATA,
		},
		{
			name: "child element",
			xml:  `<NameID>admin@victim.com<evil>evil.com</evil></NameID>`,
			want: ErrNameIDChildElement,
		},
		{
			name: "processing instruction splits text",
			xml:  `<NameID>admin@victim.com<?evil suffix?>.evil.com</NameID>`,
			want: ErrNameIDProcessingInst,
		},
		{
			name: "directive inside",
			xml:  `<NameID>admin@victim.com<!DOCTYPE x>.evil.com</NameID>`,
			want: ErrNameIDDirective,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNameID([]byte(tt.xml))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseNameID() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseNameIDRejectsWhitespaceAndMissingValues(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want error
	}{
		{
			name: "leading space",
			xml:  `<NameID> admin@victim.com.evil.com</NameID>`,
			want: ErrNameIDWhitespace,
		},
		{
			name: "trailing newline",
			xml:  "<NameID>admin@victim.com.evil.com\n</NameID>",
			want: ErrNameIDWhitespace,
		},
		{
			name: "surrounding tabs",
			xml:  "<NameID>\tadmin@victim.com.evil.com\t</NameID>",
			want: ErrNameIDWhitespace,
		},
		{
			name: "whitespace only",
			xml:  "<NameID>\n  \n</NameID>",
			want: ErrNameIDWhitespace,
		},
		{
			name: "empty element",
			xml:  `<NameID/>`,
			want: ErrEmptyNameID,
		},
		{
			name: "missing NameID",
			xml:  `<Response><Subject/></Response>`,
			want: ErrNameIDNotFound,
		},
		{
			name: "multiple NameID elements",
			xml:  `<Response><NameID>a@example.com</NameID><NameID>b@example.com</NameID></Response>`,
			want: ErrMultipleNameIDs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNameID([]byte(tt.xml))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseNameID() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadNameID(t *testing.T) {
	got, err := ReadNameID(strings.NewReader(samlNameID))
	if err != nil {
		t.Fatalf("ReadNameID() error = %v", err)
	}
	if got != "admin@victim.com.evil.com" {
		t.Fatalf("ReadNameID() = %q", got)
	}
}

func TestCommentSplitAttackDoesNotBecomeTruncatedIdentity(t *testing.T) {
	// This is the identity the vulnerable first-text-node parser would return.
	truncated := "admin@victim.com"
	attacks := []string{
		`<NameID>admin@victim.com<!--x-->.evil.com</NameID>`,
		`<NameID>admin@victim.com<!--x-->.evil.com</NameID>`,
		`<NameID>admin@victim.com<!---->.evil.com</NameID>`,
	}

	for i, attack := range attacks {
		_, err := ParseNameID([]byte(attack))
		if !errors.Is(err, ErrNameIDComment) {
			t.Fatalf("attack %d: error = %v, want %v", i, err, ErrNameIDComment)
		}
		// The attacker must not receive a successful parse of the truncated
		// identity, regardless of comment contents.
		if strings.Contains(err.Error(), truncated) && !strings.Contains(err.Error(), "comment") {
			t.Fatalf("attack %d exposed the truncated identity: %v", i, err)
		}
	}
}

func TestCanonicalizationIgnoresCommentsInsideNameID(t *testing.T) {
	withoutComment := []byte(`<NameID Format="email">admin@victim.com.evil.com</NameID>`)
	withComment := []byte(`<NameID Format="email">admin@victim.com<!--changed by attacker-->.evil.com</NameID>`)

	digestWithout, err := Digest(withoutComment)
	if err != nil {
		t.Fatalf("Digest(without comment) error = %v", err)
	}
	digestWith, err := Digest(withComment)
	if err != nil {
		t.Fatalf("Digest(with comment) error = %v", err)
	}
	if !bytes.Equal(digestWithout, digestWith) {
		t.Fatalf("comment changed the digest:\n%x\n%x", digestWithout, digestWith)
	}
}

func TestCanonicalizationIgnoresChangedCommentsOutsideElement(t *testing.T) {
	one := []byte(`<!--one--><NameID>admin@victim.com.evil.com</NameID><!--two-->`)
	another := []byte(`<!--redacted--><NameID>admin@victim.com.evil.com</NameID><!--rewritten-->`)

	d1, err := Digest(one)
	if err != nil {
		t.Fatalf("Digest(one) error = %v", err)
	}
	d2, err := Digest(another)
	if err != nil {
		t.Fatalf("Digest(another) error = %v", err)
	}
	if !bytes.Equal(d1, d2) {
		t.Fatalf("comment change outside the element changed digest:\n%x\n%x", d1, d2)
	}
}

func TestDigestDetectsTextChange(t *testing.T) {
	truncated, err := Digest([]byte(`<NameID>admin@victim.com</NameID>`))
	if err != nil {
		t.Fatalf("Digest(truncated) error = %v", err)
	}
	full, err := Digest([]byte(`<NameID>admin@victim.com.evil.com</NameID>`))
	if err != nil {
		t.Fatalf("Digest(full) error = %v", err)
	}
	if bytes.Equal(truncated, full) {
		t.Fatal("different text produced the same digest")
	}
}
