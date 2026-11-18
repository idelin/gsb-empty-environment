package netsec

import (
	"net/http"
	"testing"
)

func headers(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

func TestFrameAncestorsIgnoresXFrameOptions(t *testing.T) {
	h := headers(
		"X-Frame-Options", "DENY",
		"Content-Security-Policy", "frame-ancestors https://parent.example",
	)
	if !AllowFraming("https://site.example", h, []string{"https://parent.example"}) {
		t.Fatal("frame-ancestors present: X-Frame-Options DENY must be ignored")
	}

	h = headers(
		"X-Frame-Options", "SAMEORIGIN",
		"Content-Security-Policy", "frame-ancestors https://cross.example",
	)
	if !AllowFraming("https://site.example", h, []string{"https://cross.example"}) {
		t.Fatal("frame-ancestors present: X-Frame-Options SAMEORIGIN must be ignored")
	}
}

func TestNoneCoexistingWithOtherSources(t *testing.T) {
	h := headers(
		"Content-Security-Policy", "frame-ancestors 'none' https://parent.example",
	)
	if !AllowFraming("https://site.example", h, []string{"https://parent.example"}) {
		t.Fatal("'none' coexisting with other sources must be inert")
	}
	if AllowFraming("https://site.example", h, []string{"https://evil.example"}) {
		t.Fatal("unlisted source must still be blocked")
	}

	h = headers("Content-Security-Policy", "frame-ancestors 'none'")
	if AllowFraming("https://site.example", h, []string{"https://parent.example"}) {
		t.Fatal("lone 'none' must deny all embedding")
	}
}

func TestReportOnlyDoesNotParticipate(t *testing.T) {
	h := headers(
		"Content-Security-Policy-Report-Only", "frame-ancestors 'none'",
	)
	if !AllowFraming("https://site.example", h, []string{"https://evil.example"}) {
		t.Fatal("Report-Only frame-ancestors must not restrict embedding")
	}

	h = headers(
		"Content-Security-Policy-Report-Only", "frame-ancestors https://anyone.example",
		"X-Frame-Options", "SAMEORIGIN",
	)
	if AllowFraming("https://site.example", h, []string{"https://evil.example"}) {
		t.Fatal("Report-Only must not override X-Frame-Options SAMEORIGIN")
	}
	if !AllowFraming("https://site.example", h, []string{"https://site.example"}) {
		t.Fatal("same-origin ancestor must be allowed by X-Frame-Options SAMEORIGIN")
	}
}

func TestInvalidXFrameOptionsRefusesEmbedding(t *testing.T) {
	self := "https://site.example"
	sameOriginChain := []string{"https://site.example"}

	h := headers("X-Frame-Options", "SAMEORIGIN", "X-Frame-Options", "SAMEORIGIN")
	if AllowFraming(self, h, sameOriginChain) {
		t.Fatal("repeated X-Frame-Options headers are invalid: deny")
	}

	h = headers("X-Frame-Options", "SAMEORIGIN, DENY")
	if AllowFraming(self, h, sameOriginChain) {
		t.Fatal("comma-joined X-Frame-Options values are invalid: deny")
	}

	h = headers("X-Frame-Options", "ALLOW-FROM https://parent.example")
	if AllowFraming(self, h, sameOriginChain) {
		t.Fatal("unsupported X-Frame-Options token is invalid: deny")
	}

	h = headers("X-Frame-Options", "DENY")
	if AllowFraming(self, h, sameOriginChain) {
		t.Fatal("DENY must refuse all embedding")
	}

	h = headers("X-Frame-Options", "SAMEORIGIN")
	if !AllowFraming(self, h, sameOriginChain) {
		t.Fatal("SAMEORIGIN must allow a same-origin ancestor")
	}
	if AllowFraming(self, h, []string{"https://evil.example"}) {
		t.Fatal("SAMEORIGIN must refuse a cross-origin ancestor")
	}
}

func TestNestedAncestorChainEveryLayer(t *testing.T) {
	h := func() http.Header {
		return headers(
			"Content-Security-Policy",
			"frame-ancestors https://top.example https://mid.example",
		)
	}
	self := "https://site.example"

	// Three-level nesting: top -> mid -> site. Both ancestors listed.
	if !AllowFraming(self, h(), []string{"https://top.example", "https://mid.example"}) {
		t.Fatal("all ancestors listed: embedding must be allowed")
	}
	// The middle layer is not in the source list: deny even though the
	// top-level ancestor is allowed.
	if AllowFraming(self, h(), []string{"https://top.example", "https://evil.example"}) {
		t.Fatal("middle-layer ancestor not matching must deny embedding")
	}
	// The top layer is not in the source list: deny even though the
	// direct parent is allowed.
	if AllowFraming(self, h(), []string{"https://evil.example", "https://mid.example"}) {
		t.Fatal("top-level ancestor not matching must deny embedding")
	}
}

func TestMultipleCSPHeadersIntersect(t *testing.T) {
	h := headers(
		"Content-Security-Policy", "frame-ancestors https://a.example",
		"Content-Security-Policy", "frame-ancestors https://a.example https://b.example",
	)
	if !AllowFraming("https://site.example", h, []string{"https://a.example"}) {
		t.Fatal("ancestor allowed by every policy must be accepted")
	}
	if AllowFraming("https://site.example", h, []string{"https://b.example"}) {
		t.Fatal("ancestor allowed by only one policy must be rejected (intersection)")
	}
}

func TestSourceExpressionForms(t *testing.T) {
	self := "https://site.example"
	cases := []struct {
		name      string
		directive string
		ancestor  string
		want      bool
	}{
		{"self match", "frame-ancestors 'self'", "https://site.example", true},
		{"self mismatch", "frame-ancestors 'self'", "https://other.example", false},
		{"scheme source", "frame-ancestors https:", "https://anything.example", true},
		{"scheme source mismatch", "frame-ancestors https:", "http://anything.example", false},
		{"host without scheme inherits self scheme", "frame-ancestors parent.example", "https://parent.example", true},
		{"host without scheme wrong scheme", "frame-ancestors parent.example", "http://parent.example", false},
		{"wildcard subdomain", "frame-ancestors https://*.example.com", "https://a.example.com", true},
		{"wildcard deep subdomain", "frame-ancestors https://*.example.com", "https://a.b.example.com", true},
		{"wildcard bare domain", "frame-ancestors https://*.example.com", "https://example.com", false},
		{"wildcard other domain", "frame-ancestors https://*.example.com", "https://a.notexample.com", false},
		{"explicit port match", "frame-ancestors https://parent.example:8443", "https://parent.example:8443", true},
		{"explicit port mismatch", "frame-ancestors https://parent.example:8443", "https://parent.example", false},
		{"default port normalized", "frame-ancestors https://parent.example:443", "https://parent.example", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := headers("Content-Security-Policy", tc.directive)
			if got := AllowFraming(self, h, []string{tc.ancestor}); got != tc.want {
				t.Fatalf("AllowFraming(%q) = %v, want %v", tc.ancestor, got, tc.want)
			}
		})
	}
}

func TestUnframedNavigationAllowed(t *testing.T) {
	h := headers("X-Frame-Options", "DENY")
	if !AllowFraming("https://site.example", h, nil) {
		t.Fatal("top-level navigation (no ancestors) must be allowed")
	}
}
