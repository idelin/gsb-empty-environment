package netsec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustRequest(t *testing.T, target string, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req
}

// 1) 静态扩展名欺骗：私有资料被伪装成 /profile/secret.css。扩展名绝不参与
// 可缓存性判定，Set-Cookie/private/no-store 一律拒绝进入共享缓存。
func TestStaticExtensionSpoof_DoesNotCachePrivateProfile(t *testing.T) {
	cache := NewSharedCache()

	cases := []struct {
		name    string
		headers http.Header
		reason  string
	}{
		{
			name: "set-cookie on .css path",
			headers: http.Header{
				"Content-Type": []string{"text/html"},
				"Set-Cookie":   []string{"session=abc123; HttpOnly"},
			},
			reason: ReasonSetCookie,
		},
		{
			name: "private directive on .css path",
			headers: http.Header{
				"Cache-Control": []string{"private, max-age=3600"},
			},
			reason: ReasonPrivateDirective,
		},
		{
			name: "no-store on .css path",
			headers: http.Header{
				"Cache-Control": []string{"no-store"},
			},
			reason: ReasonNoStore,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mustRequest(t, "https://edge.example/profile/secret.css", nil)
			decision := cache.Store(req, http.StatusOK, tc.headers)
			if decision.Storable {
				t.Fatalf("private profile must not be stored, got %+v", decision)
			}
			if decision.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", decision.Reason, tc.reason)
			}
			if lookup := cache.Lookup(req); lookup.Hit {
				t.Fatalf("private profile leaked into shared cache: %+v", lookup)
			}
		})
	}

	// The same decision must hold for the extensionless path and for an
	// Accept: text/css request: neither suffix nor Accept changes the verdict.
	plainReq := mustRequest(t, "https://edge.example/profile/secret", nil)
	cssReq := mustRequest(t, "https://edge.example/profile/secret.css",
		map[string]string{"Accept": "text/css"})
	privateHeaders := http.Header{"Set-Cookie": []string{"session=z"}}
	if cache.Store(plainReq, http.StatusOK, privateHeaders).Storable ||
		cache.Store(cssReq, http.StatusOK, privateHeaders).Storable {
		t.Fatal("path extension or Accept must not turn private content cacheable")
	}
}

// 2) 无扩展名私有页：/profile 没有任何扩展名，仍然必须被识别为私有。
func TestExtensionlessPrivatePage_NotCached(t *testing.T) {
	cache := NewSharedCache()
	req := mustRequest(t, "https://edge.example/profile", nil)
	resp := http.Header{
		"Content-Type":  []string{"text/html; charset=utf-8"},
		"Cache-Control": []string{"private, no-cache"},
		"Set-Cookie":    []string{"sid=token-1"},
	}

	decision := cache.Store(req, http.StatusOK, resp)
	if decision.Storable {
		t.Fatalf("extensionless private page stored: %+v", decision)
	}
	if decision.Reason != ReasonPrivateDirective {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonPrivateDirective)
	}
	if lookup := cache.Lookup(req); lookup.Hit {
		t.Fatal("extensionless private page must not be served from shared cache")
	}
}

// 3) Vary 缺失：一个随凭据变化的响应若没有 Vary，就无法在缓存中区分用户，
// 因此带凭据请求且未显式 public 的响应必须直接拒绝存储。Vary:* 同样拒绝。
func TestVaryMissingOrWildcard_CredentialedResponseRejected(t *testing.T) {
	cache := NewSharedCache()

	// No Vary at all on a credentialed response: rejected instead of poisoning
	// a single shared entry.
	req := mustRequest(t, "https://edge.example/account",
		map[string]string{"Authorization": "Bearer alice-token"})
	decision := cache.Store(req, http.StatusOK, http.Header{
		"Content-Type": []string{"application/json"},
	})
	if decision.Storable {
		t.Fatalf("credentialed response without public must be rejected, got %+v", decision)
	}
	if decision.Reason != ReasonCredentialedNoPublic {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonCredentialedNoPublic)
	}

	// A wildcard Vary is unusable for keying and must also be refused.
	wildcardReq := mustRequest(t, "https://edge.example/account/report",
		map[string]string{"Authorization": "Bearer alice-token"})
	wildcardDecision := cache.Store(wildcardReq, http.StatusOK, http.Header{
		"Cache-Control": []string{"public, max-age=60"},
		"Vary":          []string{"*"},
	})
	if wildcardDecision.Storable || wildcardDecision.Reason != ReasonVaryWildcard {
		t.Fatalf("Vary:* response stored: %+v", wildcardDecision)
	}

	// A proper Vary is honored in the cache key: two Accept variants of the
	// same public URL are stored independently.
	publicHeaders := http.Header{
		"Cache-Control": []string{"public, max-age=60"},
		"Vary":          []string{"Accept-Encoding"},
	}
	gzipReq := mustRequest(t, "https://edge.example/app.js",
		map[string]string{"Accept-Encoding": "gzip"})
	identityReq := mustRequest(t, "https://edge.example/app.js",
		map[string]string{"Accept-Encoding": "identity"})
	cache.Store(gzipReq, http.StatusOK, publicHeaders)
	if !cache.Lookup(gzipReq).Hit {
		t.Fatal("gzip variant should hit")
	}
	if cache.Lookup(identityReq).Hit {
		t.Fatal("identity request must not receive the gzip variant")
	}
}

// 4) 带凭据请求：私有接口绝不入缓存；即使源站显式 public，命中前也要再次
// 核对凭据上下文，匿名用户和其他用户都不能拿到该响应。
func TestCredentialedRequests_StrictContextRecheck(t *testing.T) {
	cache := NewSharedCache()

	// Private API behind auth, no public marker: never stored.
	privateReq := mustRequest(t, "https://edge.example/api/me",
		map[string]string{"Authorization": "Bearer alice-token"})
	if d := cache.Store(privateReq, http.StatusOK, http.Header{
		"Cache-Control": []string{"max-age=3600"},
	}); d.Storable {
		t.Fatalf("credentialed private API stored: %+v", d)
	}

	// Explicitly public resource fetched with credentials: storable, but bound
	// to the exact credential context.
	explicitPublic := http.Header{"Cache-Control": []string{"public, max-age=60"}}
	aliceReq := mustRequest(t, "https://edge.example/static/banner.json",
		map[string]string{"Authorization": "Bearer alice-token"})
	if d := cache.Store(aliceReq, http.StatusOK, explicitPublic); !d.Storable {
		t.Fatalf("explicitly public response should be storable: %+v", d)
	}

	aliceAgain := mustRequest(t, "https://edge.example/static/banner.json",
		map[string]string{"Authorization": "Bearer alice-token"})
	if !cache.Lookup(aliceAgain).Hit {
		t.Fatal("same credential context must hit")
	}

	anonymousReq := mustRequest(t, "https://edge.example/static/banner.json", nil)
	if lookup := cache.Lookup(anonymousReq); lookup.Hit {
		t.Fatalf("anonymous request received credential-bound entry: %+v", lookup)
	}

	bobReq := mustRequest(t, "https://edge.example/static/banner.json",
		map[string]string{"Authorization": "Bearer bob-token"})
	if lookup := cache.Lookup(bobReq); lookup.Hit {
		t.Fatalf("different user received alice's entry: %+v", lookup)
	}

	// Cookie-based credentials receive the same treatment.
	cookieReq := mustRequest(t, "https://edge.example/dashboard/feed",
		map[string]string{"Cookie": "sid=alice"})
	if d := cache.Store(cookieReq, http.StatusOK, http.Header{}); d.Storable {
		t.Fatal("cookie request without public marker must not be stored")
	}
}

// 5) 合法公开资源：无凭据、允许共享的响应正常缓存并命中；no-store/private
// 以及不可缓存状态码仍然被拒绝。
func TestLegitimatePublicResources_CacheNormally(t *testing.T) {
	cache := NewSharedCache()

	publicAsset := http.Header{
		"Cache-Control": []string{"public, max-age=3600"},
		"Content-Type":  []string{"text/css"},
	}
	assetReq := mustRequest(t, "https://edge.example/assets/site.css", nil)
	if d := cache.Store(assetReq, http.StatusOK, publicAsset); !d.Storable {
		t.Fatalf("public asset rejected: %+v", d)
	}
	if lookup := cache.Lookup(assetReq); !lookup.Hit {
		t.Fatalf("public asset should hit: %+v", lookup)
	}

	// Heuristic-cachable status (200) with no explicit freshness is allowed.
	heuristicReq := mustRequest(t, "https://edge.example/landing", nil)
	if d := cache.Store(heuristicReq, http.StatusOK, http.Header{}); !d.Storable {
		t.Fatalf("default-cacheable 200 should be storable: %+v", d)
	}

	// A non-default status code needs explicit freshness directives.
	teapotReq := mustRequest(t, "https://edge.example/teapot", nil)
	d := cache.Store(teapotReq, http.StatusTeapot, http.Header{})
	if d.Storable || d.Reason != ReasonStatusNotDefaultCacheable {
		t.Fatalf("418 without explicit freshness stored: %+v", d)
	}
	explicitTeapot := cache.Store(teapotReq, http.StatusTeapot,
		http.Header{"Cache-Control": []string{"public, max-age=10"}})
	if !explicitTeapot.Storable {
		t.Fatalf("418 with explicit freshness should be storable: %+v", explicitTeapot)
	}

	// no-cache is stored but forces revalidation on every lookup.
	noCacheReq := mustRequest(t, "https://edge.example/mutable", nil)
	noCacheDecision := cache.Store(noCacheReq, http.StatusOK,
		http.Header{"Cache-Control": []string{"no-cache"}})
	if !noCacheDecision.Storable || !noCacheDecision.MustRevalidate {
		t.Fatalf("no-cache decision wrong: %+v", noCacheDecision)
	}
	lookup := cache.Lookup(noCacheReq)
	if !lookup.Hit || !lookup.MustRevalidate {
		t.Fatalf("no-cache hit must require revalidation: %+v", lookup)
	}

	// POST responses are never cached.
	postReq := httptest.NewRequest(http.MethodPost, "https://edge.example/form", nil)
	if d := cache.Store(postReq, http.StatusOK, publicAsset); d.Storable {
		t.Fatalf("POST response must not be cached: %+v", d)
	}
}

func TestPrimaryKeyIgnoresAcceptAndExtension(t *testing.T) {
	noExtension := mustRequest(t, "https://edge.example/profile",
		map[string]string{"Accept": "text/html"})
	withExtension := mustRequest(t, "https://edge.example/profile.css",
		map[string]string{"Accept": "text/css"})

	// Different paths remain distinct keys, but Accept never appears in the key
	// and cannot by itself partition or bypass the cache.
	samePathOtherAccept := mustRequest(t, "https://edge.example/profile",
		map[string]string{"Accept": "application/json"})
	if PrimaryKey(noExtension) != PrimaryKey(samePathOtherAccept) {
		t.Fatal("Accept header must not affect the primary cache key")
	}
	if PrimaryKey(noExtension) == PrimaryKey(withExtension) {
		t.Fatal("distinct paths must still produce distinct keys")
	}
}
