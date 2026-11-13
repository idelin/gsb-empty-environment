package netsec

import (
	"errors"
	"testing"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	r.MustRegister("https://api.example.com/v1/users")
	r.MustRegister("https://api.example.com/v1/orders")
	return r
}

func TestIssueTokenLocksAudienceToRegisteredResources(t *testing.T) {
	r := newTestRegistry(t)

	token, err := r.IssueToken("client-1", []string{
		"https://api.example.com/v1/users",
		"https://api.example.com/v1/orders",
	})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if len(token.Audience) != 2 || token.Subject != "client-1" {
		t.Fatalf("unexpected token: %+v", token)
	}

	// Duplicate resources collapse into a single audience entry.
	dup, err := r.IssueToken("client-1", []string{
		"https://api.example.com/v1/users",
		"https://api.example.com/v1/users",
	})
	if err != nil {
		t.Fatalf("IssueToken duplicates: %v", err)
	}
	if len(dup.Audience) != 1 {
		t.Fatalf("audience len = %d, want 1", len(dup.Audience))
	}
}

func TestIssueTokenRejectsUnregisteredAndInvalidResources(t *testing.T) {
	r := newTestRegistry(t)

	attacks := [][]string{
		nil,
		{},
		{"https://api.example.com/v1/users/admin"},
		{"https://api.example.com/v1/users?elevate=true"},
		{"https://api.example.com/v1/users#admin"},
		{"/v1/users"},
		{"https://evil.example.com/v1/users"},
	}
	for _, resources := range attacks {
		if _, err := r.IssueToken("attacker", resources); err == nil {
			t.Errorf("IssueToken(%v) = nil, want denial", resources)
		}
	}
}

func TestVerifyTokenRequiresExactAudience(t *testing.T) {
	r := newTestRegistry(t)

	token, err := r.IssueToken("client-1", []string{
		"https://api.example.com/v1/users",
	})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if err := r.VerifyToken(token, "https://api.example.com/v1/users"); err != nil {
		t.Fatalf("VerifyToken(allowed) = %v, want nil", err)
	}

	// The narrower audience must never authorize a wider, prefix-sharing API.
	widened := []string{
		"https://api.example.com/v1/users/admin",
		"https://api.example.com/v1/users/delete",
		"https://api.example.com/v1",
		"https://api.example.com/v1/orders",
	}
	for _, resource := range widened {
		err := r.VerifyToken(token, resource)
		if !errors.Is(err, ErrAudienceDenied) && !errors.Is(err, ErrResourceNotRegistered) {
			t.Errorf("VerifyToken(%q) = %v, want denial", resource, err)
		}
	}
}

func TestVerifyTokenRejectsForgedAndMalformedAudience(t *testing.T) {
	r := newTestRegistry(t)

	// A token whose audience was never issued/registered must not pass.
	forged := Token{
		Subject:  "attacker",
		Audience: []string{"https://api.example.com/v1/users/admin"},
	}
	if err := r.VerifyToken(forged, "https://api.example.com/v1/users/admin"); !errors.Is(err, ErrResourceNotRegistered) {
		t.Fatalf("forged token verify = %v, want ErrResourceNotRegistered", err)
	}

	empty := Token{Subject: "attacker"}
	if err := r.VerifyToken(empty, "https://api.example.com/v1/users"); !errors.Is(err, ErrAudienceDenied) {
		t.Fatalf("empty-audience verify = %v, want ErrAudienceDenied", err)
	}

	if err := r.VerifyToken(tokenFor("https://api.example.com/v1/users"), "/v1/users"); !errors.Is(err, ErrResourceNotAbsolute) {
		t.Fatalf("relative verify = %v, want ErrResourceNotAbsolute", err)
	}
}

func tokenFor(audience ...string) Token {
	return Token{Subject: "client", Audience: audience}
}

func TestRegistrySafeForConcurrentUse(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 200; i++ {
			_ = r.Register("https://api.example.com/resource")
		}
		close(done)
	}()

	for i := 0; i < 200; i++ {
		_ = r.Registered("https://api.example.com/resource")
		_ = r.Resources()
	}
	<-done
}
