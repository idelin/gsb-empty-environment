package framepolicy

import (
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

func header(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

func TestDecide(t *testing.T) {
	self := mustURL(t, "https://app.example.com/dashboard")
	sameOrigin := mustURL(t, "https://app.example.com/other")
	sameOriginExplicitPort := mustURL(t, "https://app.example.com:443/other")
	crossOrigin := mustURL(t, "https://evil.example.org/attack")
	example := mustURL(t, "https://example.com/widget")
	exampleSub := mustURL(t, "https://sub.example.com/widget")
	examplePort := mustURL(t, "https://example.com:8443/widget")
	httpAncestor := mustURL(t, "http://example.com/widget")

	threeLevelMix := []*url.URL{
		mustURL(t, "https://top.example.net/"),
		mustURL(t, "https://middle.evil.com/"),
		sameOrigin,
	}
	threeLevelSameOrigin := []*url.URL{
		mustURL(t, "https://app.example.com/top"),
		mustURL(t, "https://app.example.com/mid"),
		sameOriginExplicitPort,
	}

	tests := []struct {
		name string
		hdr  http.Header
		opt  Options
		want Decision
	}{
		{
			name: "no framing headers allows embedding",
			hdr:  header(),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Allow,
		},
		{
			name: "top-level load is allowed even with frame-ancestors none",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'none'"),
			opt:  Options{SelfURL: self},
			want: Allow,
		},

		// X-Frame-Options behavior.
		{
			name: "xfo deny",
			hdr:  header("X-Frame-Options", "DENY"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "xfo sameorigin allows same origin",
			hdr:  header("X-Frame-Options", "SAMEORIGIN"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOriginExplicitPort}},
			want: Allow,
		},
		{
			name: "xfo sameorigin rejects cross origin",
			hdr:  header("X-Frame-Options", "SAMEORIGIN"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},
		{
			name: "xfo token is case insensitive",
			hdr:  header("X-Frame-Options", "deny"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "xfo comma separated values are invalid and deny",
			hdr:  header("X-Frame-Options", "DENY, SAMEORIGIN"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "xfo multiple whitespace tokens are invalid and deny",
			hdr:  header("X-Frame-Options", "SAMEORIGIN DENY"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "repeated xfo headers are invalid and deny",
			hdr: header(
				"X-Frame-Options", "SAMEORIGIN",
				"X-Frame-Options", "SAMEORIGIN",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "unknown xfo token denies",
			hdr:  header("X-Frame-Options", "ALLOW-FROM https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "xfo sameorigin without known self url fails closed",
			hdr:  header("X-Frame-Options", "SAMEORIGIN"),
			opt:  Options{Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},

		// frame-ancestors precedence over X-Frame-Options.
		{
			name: "frame-ancestors ignores xfo deny when ancestor matches",
			hdr: header(
				"Content-Security-Policy", "frame-ancestors https://example.com",
				"X-Frame-Options", "DENY",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "frame-ancestors still denies non-matching ancestor despite xfo sameorigin",
			hdr: header(
				"Content-Security-Policy", "frame-ancestors https://example.com",
				"X-Frame-Options", "SAMEORIGIN",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},
		{
			name: "csp without frame-ancestors leaves xfo in effect",
			hdr: header(
				"Content-Security-Policy", "default-src 'self'",
				"X-Frame-Options", "DENY",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},

		// Report-Only never participates.
		{
			name: "report-only frame-ancestors none does not block",
			hdr: header(
				"Content-Security-Policy-Report-Only", "frame-ancestors 'none'",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Allow,
		},
		{
			name: "report-only is ignored and xfo sameorigin still applies allow",
			hdr: header(
				"Content-Security-Policy-Report-Only", "frame-ancestors 'none'",
				"X-Frame-Options", "SAMEORIGIN",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOrigin}},
			want: Allow,
		},
		{
			name: "report-only is ignored and xfo sameorigin still applies deny",
			hdr: header(
				"Content-Security-Policy-Report-Only", "frame-ancestors https://example.com",
				"X-Frame-Options", "SAMEORIGIN",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},

		// frame-ancestors source semantics.
		{
			name: "frame-ancestors none alone denies",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'none'"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "none coexisting with sources is ignored allow",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'none' https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "none coexisting with sources does not widen the list",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'none' https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},
		{
			name: "self source allows same origin with default port equivalence",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'self'"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{sameOriginExplicitPort}},
			want: Allow,
		},
		{
			name: "self source rejects cross origin",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'self'"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},
		{
			name: "self source without known self url fails closed",
			hdr:  header("Content-Security-Policy", "frame-ancestors 'self'"),
			opt:  Options{Ancestors: []*url.URL{sameOrigin}},
			want: Deny,
		},
		{
			name: "scheme source matches scheme only allow",
			hdr:  header("Content-Security-Policy", "frame-ancestors https:"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "scheme source rejects other scheme",
			hdr:  header("Content-Security-Policy", "frame-ancestors https:"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{httpAncestor}},
			want: Deny,
		},
		{
			name: "host source matches exact host",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "host source rejects subdomain without wildcard",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{exampleSub}},
			want: Deny,
		},
		{
			name: "wildcard host matches subdomain",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://*.example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{exampleSub}},
			want: Allow,
		},
		{
			name: "wildcard host also matches the base domain",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://*.example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "wildcard host rejects unrelated domain",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://*.example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Deny,
		},
		{
			name: "bare wildcard host matches any https origin",
			hdr:  header("Content-Security-Policy", "frame-ancestors *"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{crossOrigin}},
			want: Allow,
		},
		{
			name: "scheme-less host source does not match non-network scheme",
			hdr:  header("Content-Security-Policy", "frame-ancestors example.com"),
			opt: Options{
				SelfURL:   self,
				Ancestors: []*url.URL{mustURL(t, "file:///tmp/frame.html")},
			},
			want: Deny,
		},
		{
			name: "explicit port matches",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com:8443"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{examplePort}},
			want: Allow,
		},
		{
			name: "explicit port rejects default port",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com:8443"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "omitted port matches any port",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{examplePort}},
			want: Allow,
		},
		{
			name: "wildcard port matches",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com:*"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{examplePort}},
			want: Allow,
		},
		{
			name: "paths and userinfo in source are ignored",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://user@example.com/path?q=1"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "malformed source fails closed",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://[bad"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "one malformed source poisons the whole directive",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com ht!tp:"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "out-of-range port fails closed",
			hdr:  header("Content-Security-Policy", "frame-ancestors https://example.com:70000"),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "duplicate frame-ancestors in one policy first occurrence wins",
			hdr: header(
				"Content-Security-Policy",
				"frame-ancestors 'none'; frame-ancestors https://example.com",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},

		// Multiple enforced CSP headers are intersected.
		{
			name: "multiple csp headers intersect to allow when all match",
			hdr: header(
				"Content-Security-Policy", "frame-ancestors https://example.com",
				"Content-Security-Policy", "frame-ancestors https:",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Allow,
		},
		{
			name: "multiple csp headers intersect and deny when one does not match",
			hdr: header(
				"Content-Security-Policy", "frame-ancestors https://example.com",
				"Content-Security-Policy", "frame-ancestors https://other.example.net",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},
		{
			name: "one malformed csp header poisons the intersection",
			hdr: header(
				"Content-Security-Policy", "frame-ancestors https://example.com",
				"Content-Security-Policy", "frame-ancestors https://[bad",
			),
			opt:  Options{SelfURL: self, Ancestors: []*url.URL{example}},
			want: Deny,
		},

		// Full nested ancestor chains: every layer must match.
		{
			name: "three level chain allowed when every layer matches",
			hdr: header(
				"Content-Security-Policy",
				"frame-ancestors 'self' https://*.example.net https://*.evil.com",
			),
			opt:  Options{SelfURL: self, Ancestors: threeLevelMix},
			want: Allow,
		},
		{
			name: "three level chain denied when the middle layer does not match",
			hdr: header(
				"Content-Security-Policy",
				"frame-ancestors 'self' https://*.example.net",
			),
			opt:  Options{SelfURL: self, Ancestors: threeLevelMix},
			want: Deny,
		},
		{
			name: "three level sameorigin chain with explicit default port allowed",
			hdr:  header("X-Frame-Options", "SAMEORIGIN"),
			opt:  Options{SelfURL: self, Ancestors: threeLevelSameOrigin},
			want: Allow,
		},
		{
			name: "three level sameorigin chain denied at the top level",
			hdr:  header("X-Frame-Options", "SAMEORIGIN"),
			opt:  Options{SelfURL: self, Ancestors: threeLevelMix},
			want: Deny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.hdr, &tt.opt); got != tt.want {
				t.Errorf("Decide() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideWithNilOptions(t *testing.T) {
	hdr := header("Content-Security-Policy", "frame-ancestors 'none'")
	if got := Decide(hdr, nil); got != Allow {
		t.Fatalf("nil options with no ancestors: got %v, want Allow (top-level)", got)
	}
}
