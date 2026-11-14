package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAttributesWhitelistAndCopy(t *testing.T) {
	raw := map[string]string{
		AttrCopies: "2",
		AttrMedia:  "iso_a4_210x297mm",
		AttrSides:  "two-sided-long-edge",
		AttrTitle:  "Quarterly report 2026",
	}
	attributes, err := ParseAttributes(raw)
	if err != nil {
		t.Fatalf("ParseAttributes: %v", err)
	}
	raw[AttrCopies] = "999"
	if got, _ := attributes.Get(AttrCopies); got != "2" {
		t.Fatalf("parsed attributes aliased input: copies=%q", got)
	}
	values := attributes.Values()
	values[AttrCopies] = "888"
	if got, _ := attributes.Get(AttrCopies); got != "2" {
		t.Fatalf("parsed attributes aliased Values output: copies=%q", got)
	}
	if attributes.Len() != 4 {
		t.Fatalf("Len = %d", attributes.Len())
	}
}

func TestParseAttributesAliases(t *testing.T) {
	uri := "job:///q/u/j/doc.txt"
	digest := strings.Repeat("a", 64)
	attributes, err := ParseAttributes(map[string]string{
		AttrJobURI:        uri,
		AttrDocumentURI:   uri,
		AttrSHA256:        digest,
		AttrContentDigest: digest,
	})
	if err != nil {
		t.Fatalf("ParseAttributes aliases: %v", err)
	}
	if got, _ := attributes.Get(AttrDocumentURI); got != uri {
		t.Fatalf("document-uri = %q", got)
	}
}

func TestParseAttributesRejectsUnknownAndConflicting(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]string
		want error
	}{
		{"case variant", map[string]string{"Job-URI": "job:///q/u/j/d"}, ErrUnknownAttribute},
		{"underscore variant", map[string]string{"job_uri": "job:///q/u/j/d"}, ErrUnknownAttribute},
		{"unknown", map[string]string{"admin": "true"}, ErrUnknownAttribute},
		{"empty key", map[string]string{"": "x"}, ErrUnknownAttribute},
		{"huge key", map[string]string{strings.Repeat("a", maxAttributeKeyLen+1): "x"}, ErrUnknownAttribute},
		{"uri conflict", map[string]string{AttrJobURI: "job:///q/u/j/a", AttrDocumentURI: "job:///q/u/j/b"}, ErrDuplicateAttribute},
		{"digest conflict", map[string]string{AttrSHA256: strings.Repeat("a", 64), AttrContentDigest: strings.Repeat("b", 64)}, ErrDuplicateAttribute},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAttributes(test.raw)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseAttributesDoesNotNormalizeCase(t *testing.T) {
	if _, err := ParseAttributes(map[string]string{"Copies": "1"}); !errors.Is(err, ErrUnknownAttribute) {
		t.Fatalf("mixed-case whitelisted name error = %v", err)
	}
}

func TestAttributesZeroValue(t *testing.T) {
	var attributes Attributes
	if attributes.Len() != 0 || attributes.Has(AttrCopies) {
		t.Fatal("zero value is not empty")
	}
	if _, ok := attributes.Get(AttrCopies); ok {
		t.Fatal("zero value returned an attribute")
	}
	if len(attributes.Values()) != 0 {
		t.Fatal("zero value Values is not empty")
	}
}

func TestParseAttributesJSON(t *testing.T) {
	attributes, err := ParseAttributesJSON([]byte(`{"copies":"2","title":"hello"}`))
	if err != nil {
		t.Fatalf("ParseAttributesJSON: %v", err)
	}
	if got, _ := attributes.Get(AttrCopies); got != "2" {
		t.Fatalf("copies = %q", got)
	}
}

func TestParseAttributesJSONRejectsInjection(t *testing.T) {
	cases := []struct {
		name string
		data string
		want error
	}{
		{
			name: "duplicate binding key",
			data: `{"job-uri":"job:///q/u/j/own","job-uri":"job:///q/v/j/victim"}`,
			want: ErrDuplicateAttribute,
		},
		{
			name: "duplicate escaped key",
			data: `{"job-uri":"job:///q/u/j/own","job\u002duri":"job:///q/u/j/own"}`,
			want: ErrDuplicateAttribute,
		},
		{
			name: "case variant",
			data: `{"Job-URI":"job:///q/u/j/own"}`,
			want: ErrUnknownAttribute,
		},
		{
			name: "non-string value",
			data: `{"copies":2}`,
			want: ErrInvalidAttribute,
		},
		{
			name: "non-object",
			data: `["copies"]`,
			want: ErrInvalidAttribute,
		},
		{
			name: "trailing value",
			data: `{"copies":"1"}{"copies":"2"}`,
			want: ErrInvalidAttribute,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAttributesJSON([]byte(test.data))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
