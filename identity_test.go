package taskpkg

import (
	"strings"
	"testing"
)

const testBase = "http://shop.example/snap/2026/"

func newTestConfig(t *testing.T, clientRouting bool) *SiteConfig {
	t.Helper()
	cfg, err := NewSiteConfig(
		testBase,
		[]string{"utm_source", "sid", "ref"},
		[]string{"color", "tag"},
		[]string{"page", "sort"},
		clientRouting,
	)
	if err != nil {
		t.Fatalf("NewSiteConfig: %v", err)
	}
	return cfg
}

func mustIdentify(t *testing.T, cfg *SiteConfig, ref string) *Identity {
	t.Helper()
	id, err := cfg.Identify(ref)
	if err != nil {
		t.Fatalf("Identify(%q): %v", ref, err)
	}
	return id
}

// Equivalent references must collapse onto the exact same identity key.
func TestIdentifyEquivalence(t *testing.T) {
	cfg := newTestConfig(t, false)
	groups := map[string][]string{
		"relative representations": {
			"./item/42",
			"item/42",
			"../2026/item/42",
			"/snap/2026/item/42",
			"//shop.example/snap/2026/item/42",
			"http://shop.example/snap/2026/item/42",
		},
		"sortable multiset plus ignored params": {
			"list?color=red&color=blue&tag=cotton&sid=abc&utm_source=mail",
			"list?utm_source=mail&tag=cotton&color=blue&color=red&sid=abc",
			"list?color=red&tag=cotton&color=blue",
		},
		"unreserved path decoding": {
			"item/%61%62%2D%31%30%30",
			"item/ab-100",
			"item/a%62-100",
			"item/ab%2D100",
		},
		"sortable block anchored after ordered param": {
			"list?page=2&color=red&color=blue",
			"list?page=2&color=blue&color=red",
		},
		"fragments ignored without client routing": {
			"p#x",
			"p#y",
			"p",
		},
	}
	wantKey := map[string]string{
		"relative representations":                    "http://shop.example/snap/2026/item/42",
		"sortable multiset plus ignored params":       "http://shop.example/snap/2026/list?color=blue&color=red&tag=cotton",
		"unreserved path decoding":                    "http://shop.example/snap/2026/item/ab-100",
		"sortable block anchored after ordered param": "http://shop.example/snap/2026/list?page=2&color=blue&color=red",
		"fragments ignored without client routing":    "http://shop.example/snap/2026/p",
	}
	seen := map[string]string{}
	for name, refs := range groups {
		for _, ref := range refs {
			key := mustIdentify(t, cfg, ref).Key
			if key != wantKey[name] {
				t.Errorf("group %q: Identify(%q).Key = %q, want %q", name, ref, key, wantKey[name])
			}
			if prior, ok := seen[key]; ok && !sameGroup(groups, prior, ref) {
				t.Errorf("key %q unexpectedly shared by %q and %q from different groups", key, prior, ref)
			}
			seen[key] = ref
		}
	}
}

func sameGroup(groups map[string][]string, a, b string) bool {
	for _, refs := range groups {
		hasA, hasB := false, false
		for _, ref := range refs {
			hasA = hasA || ref == a
			hasB = hasB || ref == b
		}
		if hasA && hasB {
			return true
		}
	}
	return false
}

// Counterexamples must stay distinct even when they look superficially alike.
func TestIdentifyCounterexamples(t *testing.T) {
	cfg := newTestConfig(t, false)
	pairs := []struct {
		name string
		a, b string
	}{
		{"encoded slash is not a separator", "item/a%2Fb", "item/a/b"},
		{"encoded hash is not a fragment", "item/a%23b", "item/a#b"},
		{"duplicate ordered param is retained", "p?page=1", "p?page=1&page=1"},
		{"duplicate sortable value is retained", "p?color=red", "p?color=red&color=red"},
		{"different filter values", "list?color=red", "list?color=blue"},
		{"different repeated filter values", "list?color=red&color=blue", "list?color=red&color=green"},
		{"ordered sequence differs", "list?sort=asc&sort=desc", "list?sort=desc&sort=asc"},
		{"undeclared param position is preserved", "list?x=1&color=red", "list?color=red&x=1"},
		{"different hosts", "//shop.example/snap/2026/p", "//cdn.example/snap/2026/p"},
		{"non-ASCII encoding is byte-sensitive", "caf%C3%A9", "caf%c3%a9"},
		{"encoded ampersand is not a delimiter", "p?x=a%26b", "p?x=a&b"},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			ka := mustIdentify(t, cfg, tc.a).Key
			kb := mustIdentify(t, cfg, tc.b).Key
			if ka == kb {
				t.Errorf("references were wrongly merged:\n  %q -> %s\n  %q -> %s", tc.a, ka, tc.b, kb)
			}
		})
	}
}

func TestRequestURLIsResolvableAndUnnormalized(t *testing.T) {
	cfg := newTestConfig(t, false)
	cases := []struct {
		ref  string
		want string
	}{
		{"./item/42", "http://shop.example/snap/2026/item/42"},
		{"list?sid=x&color=red#sec", "http://shop.example/snap/2026/list?sid=x&color=red"},
		{"item/a%2Fb", "http://shop.example/snap/2026/item/a%2Fb"},
		{"item/a%23b", "http://shop.example/snap/2026/item/a%23b"},
		{"?color=red", "http://shop.example/snap/2026/?color=red"},
	}
	for _, tc := range cases {
		id := mustIdentify(t, cfg, tc.ref)
		if id.RequestURL != tc.want {
			t.Errorf("Identify(%q).RequestURL = %q, want %q", tc.ref, id.RequestURL, tc.want)
		}
		if strings.Contains(id.RequestURL, "#") {
			t.Errorf("request URL %q must not carry a fragment when client routing is off", id.RequestURL)
		}
	}

	// Ignored parameters remain fetchable even though they leave the key.
	id := mustIdentify(t, cfg, "list?sid=keep-me&color=red")
	if !strings.Contains(id.RequestURL, "sid=keep-me") {
		t.Errorf("ignored param vanished from request URL: %s", id.RequestURL)
	}
	if strings.Contains(id.Key, "sid") {
		t.Errorf("ignored param leaked into key: %s", id.Key)
	}
}

func TestFragmentOnlyCountsForClientRouting(t *testing.T) {
	server := newTestConfig(t, false)
	spa := newTestConfig(t, true)

	if keyA := mustIdentify(t, server, "p#/a").Key; keyA != mustIdentify(t, server, "p#/b").Key {
		t.Errorf("server-rendered fragments must not split identity")
	}
	spaA := mustIdentify(t, spa, "p#/a")
	spaB := mustIdentify(t, spa, "p#/b")
	if spaA.Key == spaB.Key {
		t.Errorf("client routes #/a and #/b must be distinct identities")
	}
	if want := "http://shop.example/snap/2026/p#/a"; spaA.Key != want || spaA.RequestURL != want {
		t.Errorf("client route identity = (%q, %q), want %q for both", spaA.Key, spaA.RequestURL, want)
	}
	if mustIdentify(t, spa, "p").Key == spaA.Key {
		t.Errorf("a fragment route must not equal the fragmentless page")
	}
}

func TestDedupKeepsDifferentFilters(t *testing.T) {
	cfg := newTestConfig(t, false)
	refs := []string{
		"item/42",
		"./item/42?sid=z",
		"../2026/item/42#top",
		"/snap/2026/item/42",
		"item/42?color=red",
		"list?color=red&color=blue",
		"list?color=blue&color=red",
		"list?color=red&color=green",
	}
	keys := map[string]int{}
	for _, ref := range refs {
		keys[mustIdentify(t, cfg, ref).Key]++
	}
	want := map[string]int{
		"http://shop.example/snap/2026/item/42":                    4,
		"http://shop.example/snap/2026/item/42?color=red":          1,
		"http://shop.example/snap/2026/list?color=blue&color=red":  2,
		"http://shop.example/snap/2026/list?color=green&color=red": 1,
	}
	if len(keys) != len(want) {
		t.Fatalf("distinct identities = %d (%v), want %d (%v)", len(keys), keys, len(want), want)
	}
	for key, count := range want {
		if keys[key] != count {
			t.Errorf("key %s observed %d times, want %d", key, keys[key], count)
		}
	}

	// The fragment-bearing duplicate still yields a fetchable fragmentless URL.
	id := mustIdentify(t, cfg, "../2026/item/42#top")
	if id.RequestURL != "http://shop.example/snap/2026/item/42" {
		t.Errorf("request URL = %q, want fragmentless form", id.RequestURL)
	}
}

func TestBaseSnapshotIsImmutableAndFixed(t *testing.T) {
	cfg := newTestConfig(t, false)
	before := cfg.Base.String()
	for _, ref := range []string{"a/b", "/elsewhere", "//other.host/x", "?q=1"} {
		_ = mustIdentify(t, cfg, ref)
	}
	if cfg.Base.String() != before {
		t.Fatalf("base snapshot mutated: was %s, now %s", before, cfg.Base.String())
	}

	// A root-relative reference always resolves against the configured snapshot.
	id := mustIdentify(t, cfg, "/snap/2026/deep/page")
	if !strings.HasPrefix(id.RequestURL, testBase) {
		t.Errorf("reference resolved outside fixed base: %s", id.RequestURL)
	}
}

func TestResolvedPathEncodingIsPreserved(t *testing.T) {
	cfg := newTestConfig(t, false)
	id := mustIdentify(t, cfg, "item/a%2Fb%7E")
	if want := "http://shop.example/snap/2026/item/a%2Fb~"; id.Key != want {
		t.Errorf("Key = %q, want %q (%%2F preserved, unreserved %%7E decoded)", id.Key, want)
	}
}

func TestCanonicalPathUnit(t *testing.T) {
	cases := map[string]string{
		"/a%2Fb":     "/a%2Fb", // slash stays encoded
		"/a%3Fb":     "/a%3Fb", // question mark stays encoded
		"/a%23b":     "/a%23b", // hash stays encoded
		"/a%26b":     "/a%26b", // ampersand stays encoded
		"/a%7Eb":     "/a~b",   // tilde is unreserved
		"/%41%42%43": "/ABC",
		"/%61%62%63": "/abc",
		"/caf%C3%A9": "/caf%C3%A9", // non-ASCII byte untouched
		"/%2d":       "/-",         // lower-case hex still decodes
		"/100%":      "/100%",      // dangling percent untouched
		"/100%2":     "/100%2",     // truncated triplet untouched
		"/a%00b":     "/a%00b",     // NUL is not unreserved
	}
	for in, want := range cases {
		if got := canonicalPath(in); got != want {
			t.Errorf("canonicalPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewSiteConfig("relative/base", nil, nil, nil, false); err == nil {
		t.Error("relative base should be rejected")
	}
	if _, err := NewSiteConfig("http://bad host/", nil, nil, nil, false); err == nil {
		t.Error("unparseable base should be rejected")
	}
	if _, err := NewSiteConfig("http://x.test", []string{"dup"}, []string{"dup"}, nil, false); err == nil {
		t.Error("ignored/sortable overlap should be rejected")
	}
	if _, err := NewSiteConfig("http://x.test", nil, []string{"dup"}, []string{"dup"}, false); err == nil {
		t.Error("sortable/ordered overlap should be rejected")
	}
	if _, err := NewSiteConfig("http://x.test", []string{"dup"}, nil, []string{"dup"}, false); err == nil {
		t.Error("ignored/ordered overlap should be rejected")
	}
	if _, err := NewSiteConfig("http://x.test", []string{""}, nil, nil, false); err == nil {
		t.Error("empty parameter name should be rejected")
	}

	var nilCfg *SiteConfig
	if err := nilCfg.Validate(); err == nil {
		t.Error("nil config should fail validation")
	}
}

func TestIdentifyRejectsInvalidReference(t *testing.T) {
	cfg := newTestConfig(t, false)
	if _, err := cfg.Identify("p\n"); err == nil {
		t.Error("reference containing a control character should be rejected")
	}
}

func TestIdentifySurfacesBrokenConfig(t *testing.T) {
	var cfg SiteConfig
	if _, err := cfg.Identify("p"); err == nil {
		t.Error("Identify on a config without a base should fail")
	}
}

func TestUserInfoIsPartOfIdentity(t *testing.T) {
	cfg, err := NewSiteConfig("http://crawl:token@shop.example/snap/", nil, nil, nil, false)
	if err != nil {
		t.Fatalf("NewSiteConfig: %v", err)
	}
	id := mustIdentify(t, cfg, "p")
	if want := "http://crawl:token@shop.example/snap/p"; id.Key != want {
		t.Errorf("Key = %q, want %q", id.Key, want)
	}
}

func TestInvalidHexTripletIsNotDecoded(t *testing.T) {
	cases := map[string]string{
		"/a%0zb": "/a%0zb",
		"/a%G0b": "/a%G0b",
	}
	for in, want := range cases {
		if got := canonicalPath(in); got != want {
			t.Errorf("canonicalPath(%q) = %q, want %q", in, got, want)
		}
	}
}
