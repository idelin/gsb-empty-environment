package netsec

import (
	"errors"
	"testing"
)

func TestParseImageDigestPinned(t *testing.T) {
	ref, err := ParseImage("registry.example.net/billing/api:v1.2.3@sha256:abc123def456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Registry != "registry.example.net" || ref.Repository != "billing/api" {
		t.Fatalf("unexpected location: %+v", ref)
	}
	if ref.Tag != "v1.2.3" || ref.Digest != "sha256:abc123def456" {
		t.Fatalf("unexpected tag/digest: %+v", ref)
	}
	if ref.Canonical != "registry.example.net/billing/api@sha256:abc123def456" {
		t.Fatalf("unexpected canonical form: %s", ref.Canonical)
	}
}

func TestParseImageRejectsFloatingTag(t *testing.T) {
	_, err := ParseImage("registry.example.net/billing/api:v1.2.3")
	if !errors.Is(err, errImage) {
		t.Fatalf("expected errImage, got %v", err)
	}
}

func TestParseImageDefaultRegistry(t *testing.T) {
	ref, err := ParseImage("nginx@sha256:0123abcd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Registry != defaultRegistry || ref.Repository != "nginx" {
		t.Fatalf("unexpected default registry parsing: %+v", ref)
	}
}

func TestSameDigestRejectsDifferentRepository(t *testing.T) {
	a, _ := ParseImage("registry.example.net/billing/api@sha256:aaaa")
	b, _ := ParseImage("registry.example.net/billing/web@sha256:aaaa")
	if a.SameDigest(b) {
		t.Fatal("digest match across repositories must be rejected")
	}

	c, _ := ParseImage("registry.example.net/billing/api:v2@sha256:aaaa")
	if !a.SameDigest(c) {
		t.Fatal("same repository and digest with a different tag must match")
	}
}
