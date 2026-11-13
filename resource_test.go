package netsec

import (
	"errors"
	"testing"
)

func TestValidateResource(t *testing.T) {
	valid := []string{
		"https://api.example.com/v1/users",
		"https://api.example.com/v1/users?scope=read",
		"http://127.0.0.1:8080/resource",
		"urn:example:resource:42",
		"mailto:ops@example.com",
		"custom-scheme://host/path",
	}
	for _, resource := range valid {
		if err := ValidateResource(resource); err != nil {
			t.Errorf("ValidateResource(%q) = %v, want nil", resource, err)
		}
	}

	invalid := map[string]error{
		"":                                ErrEmptyResource,
		"/v1/users":                       ErrResourceNotAbsolute,
		"v1/users":                        ErrResourceNotAbsolute,
		"//api.example.com/v1":            ErrResourceNotAbsolute,
		"https:":                          ErrResourceNotAbsolute,
		"https://api.example.com/v1#frag": ErrResourceFragment,
		"urn:example:resource#section":    ErrResourceFragment,
		"https://api.example.com/v1#":     ErrResourceFragment,
	}
	for resource, want := range invalid {
		err := ValidateResource(resource)
		if !errors.Is(err, want) {
			t.Errorf("ValidateResource(%q) = %v, want %v", resource, err, want)
		}
	}
}

func TestRegistryRejectsInvalidRegistration(t *testing.T) {
	r := NewRegistry()

	if err := r.Register("/relative/path"); !errors.Is(err, ErrResourceNotAbsolute) {
		t.Fatalf("Register relative = %v, want ErrResourceNotAbsolute", err)
	}
	if err := r.Register("https://api.example.com/v1#part"); !errors.Is(err, ErrResourceFragment) {
		t.Fatalf("Register fragment = %v, want ErrResourceFragment", err)
	}
	if r.Registered("https://api.example.com/v1#part") {
		t.Fatal("invalid resource must not be registered")
	}

	r.MustRegister("https://api.example.com/v1")
	if !r.Registered("https://api.example.com/v1") {
		t.Fatal("registered resource should be present")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister with invalid resource should panic")
		}
	}()
	r.MustRegister("not-an-absolute-uri")
}

func TestAllowIsExactMatchNotPrefix(t *testing.T) {
	const base = "https://api.example.com/v1"
	r := NewRegistry()
	if err := r.Register(base); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := r.Allow(base); err != nil {
		t.Fatalf("Allow(registered) = %v, want nil", err)
	}

	// The core attack: append a path/query to a registered resource and ask
	// for a token against the wider API. None of these may be accepted.
	widened := []string{
		base + "/admin",
		base + "/users/../../admin",
		base + "?admin=true",
		base + "#fragment",
		"https://api.example.com/v10",
		"https://api.example.com/",
		"https://api.example.com",
		"https://attacker.example.com/v1",
		base + "/",
	}
	for _, resource := range widened {
		err := r.Allow(resource)
		if err == nil {
			t.Errorf("Allow(%q) = nil, want denial", resource)
			continue
		}
		if !errors.Is(err, ErrResourceNotRegistered) && !errors.Is(err, ErrResourceFragment) {
			t.Errorf("Allow(%q) = %v, want ErrResourceNotRegistered/ErrResourceFragment", resource, err)
		}
	}
}

func TestAllowIsCaseSensitiveAndLiteral(t *testing.T) {
	const base = "https://api.example.com/v1/Users"
	r := NewRegistry()
	r.MustRegister(base)

	for _, resource := range []string{
		"https://api.example.com/v1/users",
		"HTTPS://api.example.com/v1/Users",
		"https://api.example.com//v1/Users",
		"https://api.example.com/v1/Users/",
	} {
		if err := r.Allow(resource); !errors.Is(err, ErrResourceNotRegistered) {
			t.Errorf("Allow(%q) = %v, want ErrResourceNotRegistered", resource, err)
		}
	}
}

func TestResourcesSnapshot(t *testing.T) {
	r := NewRegistry()
	r.MustRegister("https://a.example/")
	r.MustRegister("https://b.example/")

	got := r.Resources()
	if len(got) != 2 {
		t.Fatalf("Resources() len = %d, want 2", len(got))
	}

	// Mutating the returned slice must not affect the registry.
	got[0] = "https://tampered.example/"
	if r.Registered("https://tampered.example/") {
		t.Fatal("registry must not be affected by mutating the returned snapshot")
	}
}
