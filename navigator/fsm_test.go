package navigator

import (
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestRedirectBehavior(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		status     int
		hasBody    bool
		canReplay  bool
		wantMethod string
		wantBody   bool
		wantGo     bool
		wantChange bool
	}{
		{"307 keeps POST and body", http.MethodPost, http.StatusTemporaryRedirect, true, true, http.MethodPost, true, true, false},
		{"308 keeps PUT and body", http.MethodPut, http.StatusPermanentRedirect, true, true, http.MethodPut, true, true, false},
		{"303 downgrades POST to GET", http.MethodPost, http.StatusSeeOther, true, true, http.MethodGet, false, true, true},
		{"303 keeps GET", http.MethodGet, http.StatusSeeOther, false, true, http.MethodGet, false, true, false},
		{"302 downgrades POST to GET", http.MethodPost, http.StatusFound, true, true, http.MethodGet, false, true, true},
		{"301 downgrades POST to GET", http.MethodPost, http.StatusMovedPermanently, true, true, http.MethodGet, false, true, true},
		{"307 without replayable body stops", http.MethodPost, http.StatusTemporaryRedirect, true, false, http.MethodPost, true, false, false},
		{"200 is not a redirect", http.MethodGet, http.StatusOK, false, true, http.MethodGet, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redirectBehavior(tc.method, tc.status, tc.hasBody, tc.canReplay)
			if got.redirect != tc.wantGo {
				t.Fatalf("redirect = %v, want %v", got.redirect, tc.wantGo)
			}
			if !got.redirect {
				return
			}
			if got.method != tc.wantMethod {
				t.Errorf("method = %s, want %s", got.method, tc.wantMethod)
			}
			if got.includeBody != tc.wantBody {
				t.Errorf("includeBody = %v, want %v", got.includeBody, tc.wantBody)
			}
			if got.methodChanged != tc.wantChange {
				t.Errorf("methodChanged = %v, want %v", got.methodChanged, tc.wantChange)
			}
		})
	}
}

func TestSiteOf(t *testing.T) {
	cases := []struct {
		raw    string
		scheme string
		domain string
	}{
		{"https://www.example.com/path", "https", "example.com"},
		{"https://a.b.example.co.uk/x", "https", "example.co.uk"},
		{"http://shop.example.com:8080/", "http", "example.com"},
		{"https://localhost:54321/", "https", "localhost"},
		{"https://127.0.0.1:54322/", "https", "127.0.0.1"},
		{"https://user.project.github.io/", "https", "project.github.io"},
	}
	for _, tc := range cases {
		got := SiteOf(mustURL(t, tc.raw))
		if got.Scheme != tc.scheme || got.Domain != tc.domain {
			t.Errorf("SiteOf(%q) = %v, want %s://%s", tc.raw, got, tc.scheme, tc.domain)
		}
	}
}

func TestCrossSiteRedirectChain(t *testing.T) {
	top := SiteOf(mustURL(t, "https://www.example.com/"))
	other := SiteOf(mustURL(t, "https://other.test/"))

	// Same-site hop followed by another same-site target.
	if crossSite(top, []Site{top}, top) {
		t.Error("same-site chain reported cross-site")
	}
	// Cross-site hop in the chain poisons every later hop, even when the
	// target returns to the original site.
	if !crossSite(top, []Site{top, other}, top) {
		t.Error("cross-site redirect hop did not poison chain")
	}
	// Opaque site-for-cookies means always cross-site.
	if !crossSite(Site{}, nil, top) {
		t.Error("opaque site-for-cookies should always be cross-site")
	}
}

func TestStateAdvanceKeepsInitiator(t *testing.T) {
	navCtx := NewEmbeddedContext(mustURL(t, "https://embedder.test/frame"))
	state := State{
		Phase:   PhaseIssuing,
		Context: navCtx,
		Method:  http.MethodPost,
		URL:     mustURL(t, "https://embedder.test/submit"),
	}
	next := state.advance(
		mustURL(t, "https://other.test/landing"),
		redirectBehavior(http.MethodPost, http.StatusSeeOther, true, true),
	)
	if next.Context.Kind() != EmbeddedResource {
		t.Fatal("redirect changed the navigation kind")
	}
	if next.Method != http.MethodGet || !next.MethodChanged {
		t.Fatalf("method = %s changed=%v, want GET downgrade", next.Method, next.MethodChanged)
	}
	if !next.CrossSite {
		t.Error("cross-site redirect not detected")
	}
	if next.Phase != PhaseRedirect || next.Hop != 1 {
		t.Errorf("phase=%s hop=%d, want redirect/1", next.Phase, next.Hop)
	}
}

func TestSensitiveHeaderStripping(t *testing.T) {
	a := SiteOf(mustURL(t, "https://a.test/"))
	b := SiteOf(mustURL(t, "https://b.test/"))
	if !shouldStripHeader("authorization", a, b) {
		t.Error("Authorization must be stripped on cross-site redirect")
	}
	if shouldStripHeader("Authorization", a, a) {
		t.Error("Authorization must be retained on same-site redirect")
	}
	if shouldStripHeader("X-Trace", a, b) {
		t.Error("custom headers must survive cross-site redirects")
	}
}
