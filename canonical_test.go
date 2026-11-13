package netsec

import (
	"bytes"
	"crypto/sha1"
	"testing"
)

func TestCanonicalizeNormalizesNamespacesAttributesAndEscaping(t *testing.T) {
	input := []byte(`<?xml version="1.0"?>
<!-- signature canonicalization omits this -->
<root xmlns:b="urn:b" xmlns="urn:root" b:z="2" a="3">
  text &amp; &lt; &gt;
  <b:child attr="x" b:attr="y"/>
</root>`)

	want := []byte(`<root xmlns="urn:root" xmlns:b="urn:b" a="3" b:z="2">
  text &amp; &lt; &gt;
  <b:child attr="x" b:attr="y"></b:child>
</root>`)

	got, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Canonicalize() =\n%s\nwant:\n%s", got, want)
	}
}

func TestCanonicalizeDeduplicatesInheritedNamespaces(t *testing.T) {
	input := []byte(`<a xmlns="urn:x"><b xmlns="urn:x"><c xmlns:p="urn:p" xmlns="urn:x"/></b></a>`)
	want := []byte(`<a xmlns="urn:x"><b><c xmlns:p="urn:p"></c></b></a>`)

	got, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Canonicalize() = %s, want %s", got, want)
	}
}

func TestCanonicalDigestUsesProvidedHash(t *testing.T) {
	got, err := CanonicalDigest([]byte(`<NameID>a@example.com</NameID>`), sha1.New())
	if err != nil {
		t.Fatalf("CanonicalDigest() error = %v", err)
	}
	want, err := SHA1Digest([]byte(`<NameID>a@example.com</NameID>`))
	if err != nil {
		t.Fatalf("SHA1Digest() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("CanonicalDigest() = %x, want %x", got, want)
	}
}
