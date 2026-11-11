package navigator

import (
	"net/http"
	"testing"
	"time"
)

func seedJar(t *testing.T) *Jar {
	t.Helper()
	jar := NewJar()
	base := mustURL(t, "https://bank.test/")
	jar.SetCookies(base, []*http.Cookie{
		{Name: "strict", Value: "s", SameSite: http.SameSiteStrictMode, Secure: true, Path: "/"},
		{Name: "lax", Value: "l", SameSite: http.SameSiteLaxMode, Secure: true, Path: "/"},
		{Name: "none", Value: "n", SameSite: http.SameSiteNoneMode, Secure: true, Path: "/"},
		{Name: "defaulted", Value: "d", Secure: true, Path: "/"},
		{Name: "insecure", Value: "i", SameSite: http.SameSiteLaxMode, Path: "/"},
	})
	return jar
}

func names(cookies []*http.Cookie) map[string]bool {
	out := map[string]bool{}
	for _, c := range cookies {
		out[c.Name] = true
	}
	return out
}

func TestSameSiteSelection(t *testing.T) {
	jar := seedJar(t)
	target := mustURL(t, "https://bank.test/pay")

	t.Run("same-site sends all", func(t *testing.T) {
		got := names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodPost, CrossSite: false, TopLevel: false,
		}))
		for _, want := range []string{"strict", "lax", "none", "defaulted", "insecure"} {
			if !got[want] {
				t.Errorf("missing %s in same-site request: %v", want, got)
			}
		}
	})

	t.Run("cross-site top-level GET is Lax", func(t *testing.T) {
		got := names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodGet, CrossSite: true, TopLevel: true,
		}))
		if got["strict"] {
			t.Error("Strict cookie leaked on cross-site GET")
		}
		if !got["lax"] || !got["none"] || !got["defaulted"] || !got["insecure"] {
			t.Errorf("Lax/None cookies missing on cross-site GET: %v", got)
		}
	})

	t.Run("cross-site top-level POST withholds Lax", func(t *testing.T) {
		got := names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodPost, CrossSite: true, TopLevel: true,
		}))
		if got["lax"] || got["defaulted"] || got["strict"] {
			t.Errorf("Lax/Strict cookies leaked on cross-site POST: %v", got)
		}
		if !got["none"] {
			t.Error("SameSite=None cookie withheld on cross-site POST")
		}
	})

	t.Run("cross-site embedded GET withholds Lax and Strict", func(t *testing.T) {
		got := names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodGet, CrossSite: true, TopLevel: false,
		}))
		if got["lax"] || got["strict"] || got["defaulted"] {
			t.Errorf("embedded request borrowed top-level login cookies: %v", got)
		}
		if !got["none"] {
			t.Error("SameSite=None cookie should still attach to embedded request")
		}
	})

	t.Run("Lax+POST window allows fresh Lax cookie", func(t *testing.T) {
		got := names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodPost, CrossSite: true, TopLevel: true,
			LaxPostWindow: time.Minute,
		}))
		if !got["lax"] {
			t.Error("fresh Lax cookie should ride cross-site POST inside window")
		}
		aged := jar.entries["bank.test"]["/|lax"]
		aged.createdAt = time.Now().Add(-3 * time.Minute)
		got = names(jar.SelectCookies(CookieRequest{
			URL: target, Method: http.MethodPost, CrossSite: true, TopLevel: true,
			LaxPostWindow: time.Minute,
		}))
		if got["lax"] {
			t.Error("aged Lax cookie must not ride cross-site POST")
		}
	})

	t.Run("Secure cookie rejected over plain http", func(t *testing.T) {
		httpURL := mustURL(t, "http://bank.test/")
		got := names(jar.SelectCookies(CookieRequest{
			URL: httpURL, Method: http.MethodGet, CrossSite: false,
		}))
		if got["strict"] || got["lax"] || got["none"] {
			t.Errorf("Secure cookies attached over http: %v", got)
		}
	})
}

func TestCookieDomainAndPath(t *testing.T) {
	jar := NewJar()
	jar.SetCookies(mustURL(t, "https://shop.example.com/"), []*http.Cookie{
		{Name: "root", Value: "r", Path: "/"},
		{Name: "deep", Value: "d", Path: "/cart"},
		{Name: "rejected", Value: "x", Domain: "evil.test", Path: "/"},
	})
	got := names(jar.SelectCookies(CookieRequest{
		URL: mustURL(t, "https://shop.example.com/cart/items"), CrossSite: false,
	}))
	if !got["root"] || !got["deep"] || got["rejected"] {
		t.Errorf("domain/path selection wrong: %v", got)
	}
	got = names(jar.SelectCookies(CookieRequest{
		URL: mustURL(t, "https://other.example.com/cart"), CrossSite: false,
	}))
	if got["root"] || got["deep"] {
		t.Errorf("host-only cookies leaked to sibling host: %v", got)
	}
}
