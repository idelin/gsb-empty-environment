package netsec

import "testing"

func TestParseEndpointNormalization(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		host string
		port string
	}{
		{"https default port", "https://example.com", "example.com:443", "443"},
		{"http default port", "http://example.com", "example.com:80", "80"},
		{"upper case folded", "HTTPS://EXAMPLE.COM:443", "example.com:443", "443"},
		{"trailing dot removed", "https://example.com.:443", "example.com:443", "443"},
		{"explicit port kept", "https://example.com:5000", "example.com:5000", "5000"},
		{"missing scheme defaults https", "example.com:5000", "example.com:5000", "5000"},
		{"ipv6 loopback", "http://[::1]:5000", "[::1]:5000", "5000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep, err := parseEndpoint(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.host != tc.host {
				t.Errorf("host = %q, want %q", ep.host, tc.host)
			}
			if ep.port != tc.port {
				t.Errorf("port = %q, want %q", ep.port, tc.port)
			}
		})
	}
}

func TestSameDomainVariants(t *testing.T) {
	cases := []struct {
		name   string
		server string
		host   string
		want   bool
	}{
		{"identical", "https://registry.example.com", "https://registry.example.com", true},
		{"case variant", "HTTPS://Registry.Example.com", "https://registry.example.com", true},
		{"trailing dot", "https://registry.example.com", "https://registry.example.com.", true},
		{"explicit default port", "https://registry.example.com", "https://registry.example.com:443", true},
		{"dot plus port", "https://registry.example.com:443", "https://REGISTRY.EXAMPLE.COM.:443", true},
		{"different host", "https://registry.example.com", "https://mirror.example.com", false},
		{"different port", "https://registry.example.com:443", "https://registry.example.com:8443", false},
		{"subdomain", "https://example.com", "https://registry.example.com", false},
		{"http default port folded", "http://registry.example.com", "http://registry.example.com:80", true},
		{"scheme differs -> default port differs", "http://registry.example.com", "https://registry.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := parseEndpoint(tc.server)
			if err != nil {
				t.Fatalf("server parse: %v", err)
			}
			host, err := parseEndpoint(tc.host)
			if err != nil {
				t.Fatalf("host parse: %v", err)
			}
			if got := sameDomain(server, host); got != tc.want {
				t.Errorf("sameDomain(%q, %q) = %v, want %v", tc.server, tc.host, got, tc.want)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	loopback := []string{
		"http://localhost",
		"https://LOCALHOST.:8443",
		"http://127.0.0.1:5000",
		"http://127.1.2.3",
		"http://[::1]",
		"http://registry.localhost",
	}
	for _, raw := range loopback {
		ep, err := parseEndpoint(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if !ep.isLoopback() {
			t.Errorf("%q should be loopback", raw)
		}
	}

	external := []string{
		"https://mirror.example.com",
		"https://10.0.0.1",
		"https://192.168.1.1",
		"https://169.254.169.254",
	}
	for _, raw := range external {
		ep, err := parseEndpoint(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if ep.isLoopback() {
			t.Errorf("%q should not be loopback", raw)
		}
	}
}
