package netsec

import (
	"errors"
	"net/http"
	"testing"
)

func authHeader(value string) http.Header {
	return http.Header{"Authorization": []string{value}}
}

func TestRejectPlaintextMirror(t *testing.T) {
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "http://mirror.example.com", Capabilities: []string{CapPull}},
			{Host: "https://verify-off.example.com", Capabilities: []string{CapPull}, SkipVerify: true},
			{Host: "https://mirror2.example.com", Capabilities: []string{CapPull}},
		},
	}
	err := Validate(cfg, DefaultPolicy())
	if err == nil {
		t.Fatal("expected rejection of http and skip_verify hosts")
	}
	if !errors.Is(err, ErrPlaintext) {
		t.Fatalf("error does not wrap ErrPlaintext: %v", err)
	}

	var validationErrs []*ValidationError
	for _, wrapped := range splitJoin(err) {
		var ve *ValidationError
		if errors.As(wrapped, &ve) {
			validationErrs = append(validationErrs, ve)
		}
	}
	if len(validationErrs) != 2 {
		t.Fatalf("want 2 violations, got %d: %v", len(validationErrs), err)
	}

	// The secure sibling host is fine on its own.
	good := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://mirror2.example.com", Capabilities: []string{CapPull}},
		},
	}
	if err := Validate(good, DefaultPolicy()); err != nil {
		t.Fatalf("secure host must pass: %v", err)
	}
}

func TestLoopbackExemption(t *testing.T) {
	cfg := &Config{
		Server: "http://localhost:5000",
		Hosts: []Host{
			{Host: "http://127.0.0.1:5000", Capabilities: []string{CapPull}},
			{Host: "http://[::1]:5000", Capabilities: []string{CapPull}, SkipVerify: true},
		},
	}
	if err := Validate(cfg, DefaultPolicy()); err != nil {
		t.Fatalf("loopback endpoints must be exempt: %v", err)
	}

	// Without the policy exemption, loopback plaintext is rejected too.
	strict := Policy{}
	if err := Validate(cfg, strict); !errors.Is(err, ErrPlaintext) {
		t.Fatalf("strict policy must reject loopback plaintext, got: %v", err)
	}
}

func TestRejectCredentialHeaderExfiltration(t *testing.T) {
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://evil-mirror.example.net", Capabilities: []string{CapPull}, Header: authHeader("Bearer token")},
		},
	}
	err := Validate(cfg, DefaultPolicy())
	if !errors.Is(err, ErrCredentialLeak) {
		t.Fatalf("want ErrCredentialLeak, got %v", err)
	}

	// Same-domain host keeps the credential even when written differently.
	sameDomain := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://REGISTRY.EXAMPLE.COM.:443", Capabilities: []string{CapPull}, Header: authHeader("Bearer token")},
		},
	}
	if err := Validate(sameDomain, DefaultPolicy()); err != nil {
		t.Fatalf("same-domain credential must pass: %v", err)
	}

	// Different port breaks same-domain even for the same machine name.
	differentPort := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://registry.example.com:8443", Capabilities: []string{CapPull}, Header: authHeader("Bearer token")},
		},
	}
	if err := Validate(differentPort, DefaultPolicy()); !errors.Is(err, ErrCredentialLeak) {
		t.Fatalf("different port must reject credential header, got: %v", err)
	}
}

func TestResolveOnlyHostMustNotPull(t *testing.T) {
	policy := DefaultPolicy()
	policy.ResolveOnlyHosts = []string{"disco.example.com"}

	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://disco.example.com", Capabilities: []string{CapResolve, CapPull}},
		},
	}
	if err := Validate(cfg, policy); !errors.Is(err, ErrResolveOnlyPull) {
		t.Fatalf("want ErrResolveOnlyPull, got %v", err)
	}

	// Resolve-only with matching capabilities is accepted.
	good := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://DISCO.example.com.:443", Capabilities: []string{CapResolve}},
		},
	}
	if err := Validate(good, policy); err != nil {
		t.Fatalf("resolve-only host without pull must pass: %v", err)
	}
}

func TestMiscValidationRules(t *testing.T) {
	t.Run("invalid capability", func(t *testing.T) {
		cfg := &Config{
			Server: "https://registry.example.com",
			Hosts:  []Host{{Host: "https://mirror.example.com", Capabilities: []string{"frobnicate"}}},
		}
		if err := Validate(cfg, DefaultPolicy()); !errors.Is(err, ErrInvalidCapability) {
			t.Fatalf("want ErrInvalidCapability, got %v", err)
		}
	})

	t.Run("duplicate normalized host", func(t *testing.T) {
		cfg := &Config{
			Server: "https://registry.example.com",
			Hosts: []Host{
				{Host: "https://mirror.example.com", Capabilities: []string{CapPull}},
				{Host: "https://MIRROR.example.com.:443", Capabilities: []string{CapPull}},
			},
		}
		if err := Validate(cfg, DefaultPolicy()); !errors.Is(err, ErrDuplicateHost) {
			t.Fatalf("want ErrDuplicateHost, got %v", err)
		}
	})

	t.Run("bad server", func(t *testing.T) {
		cfg := &Config{Server: "://not a url", Hosts: []Host{{Host: "https://mirror.example.com"}}}
		if err := Validate(cfg, DefaultPolicy()); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("want ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("incomplete client cert", func(t *testing.T) {
		cfg := &Config{
			Server: "https://registry.example.com",
			Hosts: []Host{
				{Host: "https://mirror.example.com", Client: &ClientCert{Cert: "/etc/cert.pem"}},
			},
		}
		if err := Validate(cfg, DefaultPolicy()); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("want ErrInvalidConfig for partial client cert, got %v", err)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		if err := Validate(nil, DefaultPolicy()); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("want ErrInvalidConfig, got %v", err)
		}
	})
}

func TestSecureConfigPasses(t *testing.T) {
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{
				Host:         "https://mirror.example.com",
				Capabilities: []string{CapResolve, CapPull},
				CA:           []string{"/etc/ssl/ca.pem"},
				Client:       &ClientCert{Cert: "/etc/ssl/cert.pem", Key: "/etc/ssl/key.pem"},
				Header:       http.Header{"X-Registry": []string{"acme"}},
				OverridePath: true,
			},
		},
	}
	if err := Validate(cfg, DefaultPolicy()); err != nil {
		t.Fatalf("valid config must pass: %v", err)
	}
}

// splitJoin unwraps an errors.Join style multi error for assertions.
func splitJoin(err error) []error {
	type multiUnwrap interface{ Unwrap() []error }
	var mu multiUnwrap
	if errors.As(err, &mu) {
		return mu.Unwrap()
	}
	return []error{err}
}
