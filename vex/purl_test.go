package vex

import "testing"

func TestParsePurl(t *testing.T) {
	p, err := parsePurl("pkg:maven/org.apache/xml-sec@2.3.0?classifier=sources#src/main")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	want := purl{Type: "maven", Namespace: "org.apache", Name: "xml-sec", Version: "2.3.0"}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}
}

func TestParsePurlWithoutNamespace(t *testing.T) {
	p, err := parsePurl("pkg:golang/github.com/foo/bar@v1.2.3")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if p.Type != "golang" || p.Namespace != "github.com/foo" ||
		p.Name != "bar" || p.Version != "v1.2.3" {
		t.Fatalf("unexpected purl: %+v", p)
	}
}

func TestParsePurlErrors(t *testing.T) {
	for _, raw := range []string{
		"",
		"http://example.com/x",
		"pkg:",
		"pkg:maven/",
		"pkg:/no-name@1.0",
	} {
		if _, err := parsePurl(raw); err == nil {
			t.Fatalf("parsePurl(%q) expected error", raw)
		}
	}
}

func TestPurlSameComponent(t *testing.T) {
	base := purl{Type: "generic", Namespace: "acme", Name: "libxml2", Version: "2.9.10"}

	otherVersion := base
	otherVersion.Version = "3.0.0"
	if !base.sameComponent(otherVersion) {
		t.Fatal("different version must remain the same component")
	}

	otherName := base
	otherName.Name = "libxslt"
	if base.sameComponent(otherName) {
		t.Fatal("different name must not match")
	}

	otherType := base
	otherType.Type = "maven"
	if base.sameComponent(otherType) {
		t.Fatal("different type must not match")
	}
}
