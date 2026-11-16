package netsec

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestCanonicalizeExcludingComments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "comment inside splits text",
			input: `<NameID>admin@victim.com<!-- changed -->.evil.com</NameID>`,
			want:  `<NameID>admin@victim.com.evil.com</NameID>`,
		},
		{
			name:  "comment outside root",
			input: `<!-- before --><Response><!-- inside --><NameID>user@example.com</NameID></Response><!-- after -->`,
			want:  `<Response><NameID>user@example.com</NameID></Response>`,
		},
		{
			name:  "CDATA becomes text",
			input: `<NameID><![CDATA[admin@victim.com.evil.com]]></NameID>`,
			want:  `<NameID>admin@victim.com.evil.com</NameID>`,
		},
		{
			name:  "empty element expansion",
			input: `<Response><Subject></Subject><Issuer/></Response>`,
			want:  `<Response><Subject></Subject><Issuer></Issuer></Response>`,
		},
		{
			name:  "text and attribute escaping",
			input: `<Value a="x&amp;&quot;&#9;&#10;&#13;">x&amp;&lt;&gt;&#13;</Value>`,
			want:  `<Value a="x&amp;&quot;&#x9;&#xA;&#xD;">x&amp;&lt;&gt;&#xD;</Value>`,
		},
		{
			name:  "literal attribute whitespace normalization",
			input: "<Value a=\"x\ty\nz\rw\"></Value>",
			want:  `<Value a="x y z w"></Value>`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := CanonicalizeExcludingComments([]byte(test.input))
			if err != nil {
				t.Fatalf("CanonicalizeExcludingComments() error = %v", err)
			}
			if string(got) != test.want {
				t.Fatalf("CanonicalizeExcludingComments() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalizeSortsNamespacesAndAttributes(t *testing.T) {
	t.Parallel()

	const input = `<r z:x="3" b="2" xmlns:z="urn:z" a="1"><z:c z:a="4" a="5"></z:c></r>`
	const want = `<r xmlns:z="urn:z" a="1" b="2" z:x="3"><z:c a="5" z:a="4"></z:c></r>`

	got, err := CanonicalizeExcludingComments([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeExcludingComments() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("CanonicalizeExcludingComments() = %q, want %q", got, want)
	}
}

func TestCanonicalizeDefaultNamespace(t *testing.T) {
	t.Parallel()

	const input = `<r xmlns="urn:one"><c xmlns="urn:two"><d xmlns=""></d></c></r>`
	const want = `<r xmlns="urn:one"><c xmlns="urn:two"><d xmlns=""></d></c></r>`

	got, err := CanonicalizeExcludingComments([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeExcludingComments() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("CanonicalizeExcludingComments() = %q, want %q", got, want)
	}
}

func TestDigestExcludingCommentsIsCommentInvariant(t *testing.T) {
	t.Parallel()

	inputs := [][]byte{
		[]byte(`<NameID>admin@victim.com<!-- one -->.evil.com</NameID>`),
		[]byte(`<NameID>admin@victim.com<!-- a completely different comment -->.evil.com</NameID>`),
		[]byte(`<!-- before --><NameID>admin@victim.com<!-- one -->.evil.com</NameID><!-- after -->`),
	}

	var reference []byte
	for _, input := range inputs {
		got, err := DigestExcludingComments(input, sha256.New())
		if err != nil {
			t.Fatalf("DigestExcludingComments() error = %v", err)
		}
		if reference == nil {
			reference = got
			canonical := []byte(`<NameID>admin@victim.com.evil.com</NameID>`)
			direct := sha256.Sum256(canonical)
			if !bytes.Equal(got, direct[:]) {
				t.Fatalf("DigestExcludingComments() = %x, want %x", got, direct)
			}
			continue
		}
		if !bytes.Equal(got, reference) {
			t.Fatalf("DigestExcludingComments() changed with comment content: %x != %x", got, reference)
		}
	}
}

func TestDigestAcceptsHashForms(t *testing.T) {
	t.Parallel()

	input := []byte(`<NameID>user@example.com</NameID>`)
	canonical := []byte(`<NameID>user@example.com</NameID>`)
	want := sha1.Sum(canonical)

	for _, hasher := range []any{sha1.New, sha1.New(), nil} {
		got, err := DigestExcludingComments(input, hasher)
		if err != nil {
			t.Fatalf("DigestExcludingComments(%T) error = %v", hasher, err)
		}
		if hasher == nil {
			sum := sha256.Sum256(canonical)
			if !bytes.Equal(got, sum[:]) {
				t.Fatalf("default digest = %x, want %x", got, sum)
			}
			continue
		}
		if !bytes.Equal(got, want[:]) {
			t.Fatalf("SHA-1 digest = %x, want %x", got, want)
		}
	}
}

func TestCanonicalizeRejectsMalformedXML(t *testing.T) {
	t.Parallel()

	inputs := []string{
		``,
		`<a>`,
		`<a></b>`,
		`<a/><b/>`,
		`text`,
		`<p:a></p:a>`,
	}
	for _, input := range inputs {
		_, err := CanonicalizeExcludingComments([]byte(input))
		if !errors.Is(err, ErrMalformedXML) {
			t.Errorf("CanonicalizeExcludingComments(%q) error = %v, want wrapped %v", input, err, ErrMalformedXML)
		}
	}
}
