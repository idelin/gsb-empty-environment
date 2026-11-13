package netsec

import (
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct{ target, want string }{
		{"/static/site.css", "/static/site.css"},
		{"/static/site.css?v=2", "/static/site.css"},
		{"/static/site.css;theme=dark", "/static/site.css"},
		{"/a;b=c/static/site.css", "/a/static/site.css"},
		{"/account/profile%2ecss", "/account/profile.css"},
		{"/account/profile%2e%63ss", "/account/profile.css"},
		{"/account/profile%2e.css", "/account/profile..css"},
		{"/account/profile", "/account/profile"},
		{"/account/profile?next=/x.css", "/account/profile"},
	}
	for _, c := range cases {
		if got := NormalizePath(c.target); got != c.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}

func TestShouldCache(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		// Real static paths remain cacheable.
		{"/static/site.css", true},
		{"/static/app.JS", true},
		{"/img/logo.png", true},
		// Query and semicolon parameters do not change the type.
		{"/static/site.css?v=2", true},
		{"/static/site.css;theme=dark", true},
		// Disguised suffixes: encoded dot and semicolon parameters make an
		// identity path look static.
		{"/account/profile%2ecss", true},
		{"/account/profile%2e%63ss", true},
		{"/account/profile%2e.css", true},
		{"/account/profile.css;session=abc", true},
		{"/account/profile;css=/x.css", true},
		// Genuine dynamic paths are not cacheable.
		{"/account/profile", false},
		{"/account/profile?next=/x.css", false},
		{"/account/settings.json/", false},
	}
	for _, c := range cases {
		if got := ShouldCache(c.target); got != c.want {
			t.Errorf("ShouldCache(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

// TestIdentityResponseServedFromCache plants an identity response under a
// disguised path, then replays the same cache key through the %2e and
// semicolon variants and confirms each one hits the cached identity body.
func TestIdentityResponseServedFromCache(t *testing.T) {
	backend := func(path string) []byte {
		if strings.HasPrefix(path, "/account/") {
			return []byte("identity:" + path)
		}
		return []byte("static")
	}
	cache := NewCache()

	// Each scenario plants an identity response under a disguised path,
	// then replays variants of the same normalized cache key.
	scenarios := []struct {
		plant    string
		variants []string
	}{
		// Encoded dot replaces the dot of .css.
		{
			plant: "/account/profile%2ecss",
			variants: []string{
				"/account/profile.css",
				"/account/profile%2e%63ss",
				"/account/profile.css;session=attacker",
				"/account/profile.css?v=1",
			},
		},
		// %2e inserted before .css yields a double-dot key, still static.
		{
			plant: "/account/profile%2e.css",
			variants: []string{
				"/account/profile..css",
				"/account/profile%2e%2ecss",
				"/account/profile..css;session=attacker",
			},
		},
		// Semicolon parameter appended after a real-looking suffix.
		{
			plant: "/account/settings.css;session=victim",
			variants: []string{
				"/account/settings.css",
				"/account/settings%2ecss",
			},
		},
	}
	for _, s := range scenarios {
		planted, hit := cache.Serve(s.plant, backend)
		if hit {
			t.Errorf("plant %q unexpectedly hit the cache", s.plant)
			continue
		}
		if !strings.HasPrefix(string(planted), "identity:") {
			t.Errorf("plant %q: expected identity response, got %q", s.plant, planted)
			continue
		}
		for _, target := range s.variants {
			body, hit := cache.Serve(target, backend)
			if !hit {
				t.Errorf("Serve(%q) missed the cache (planted via %q)", target, s.plant)
				continue
			}
			if string(body) != string(planted) {
				t.Errorf("Serve(%q) = %q, want cached identity body %q", target, body, planted)
			}
		}
	}
}

// TestRealStaticPathCached confirms ordinary static assets still cache.
func TestRealStaticPathCached(t *testing.T) {
	calls := 0
	backend := func(path string) []byte {
		calls++
		return []byte("body{}")
	}
	cache := NewCache()
	cache.Serve("/static/site.css", backend)
	body, hit := cache.Serve("/static/site.css?v=2", backend)
	if !hit || string(body) != "body{}" || calls != 1 {
		t.Errorf("static asset not cached: hit=%v body=%q calls=%d", hit, body, calls)
	}
}
