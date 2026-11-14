package netsec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKeyPreservesDuplicateQueryOrder(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{Identity: Identity{Site: "site-a", Tenant: "tenant-a"}})

	first := mustBuildKey(t, builder, "/account?role=user&role=admin")
	reversed := mustBuildKey(t, builder, "/account?role=admin&role=user")

	if first.Equal(reversed) || first.Hex() == reversed.Hex() {
		t.Fatal("different duplicate-parameter orders produced the same cache key")
	}
	if first.NormalizedQuery != "role=user&role=admin" {
		t.Fatalf("unexpected canonical query %q", first.NormalizedQuery)
	}
	if reversed.NormalizedQuery != "role=admin&role=user" {
		t.Fatalf("unexpected canonical query %q", reversed.NormalizedQuery)
	}
}

func TestKeyNormalizesQueryNamesButNotDuplicateValues(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})

	one := mustBuildKey(t, builder, "/resource?a=1&b=2&a=3")
	two := mustBuildKey(t, builder, "/resource?b=2&a=1&a=3")
	if !one.Equal(two) {
		t.Fatalf("equivalent normalized queries differ:\n%s\n%s", one.NormalizedQuery, two.NormalizedQuery)
	}
	if one.NormalizedQuery != "a=1&a=3&b=2" {
		t.Fatalf("unexpected canonical query %q", one.NormalizedQuery)
	}

	three := mustBuildKey(t, builder, "/resource?a=3&a=1&b=2")
	if one.Equal(three) {
		t.Fatal("values reordered within the same name produced the same key")
	}
}

func TestKeySeparatesPathAndIdentity(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{Identity: Identity{Site: "site-a", Tenant: "tenant-a"}})
	base := mustBuildKey(t, builder, "/same/path")

	otherTenant, err := builder.BuildFor(mustRequest("/same/path"), Identity{Site: "site-a", Tenant: "tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	otherSite, err := builder.BuildFor(mustRequest("/same/path"), Identity{Site: "site-b", Tenant: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}

	if base.Equal(otherTenant) || base.Equal(otherSite) {
		t.Fatal("site or tenant identity collision")
	}
}

func TestKeyHeaderNamesAreCaseInsensitive(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{
		NegotiationHeaders: []string{"Accept", "Accept-Encoding"},
	})

	canonical := mustRequest("/asset.js")
	canonical.Header.Set("Accept", "application/javascript")
	canonical.Header.Set("Accept-Encoding", "br, gzip")

	lowercase := mustRequest("/asset.js")
	lowercase.Header["accept"] = []string{"application/javascript"}
	lowercase.Header["ACCEPT-ENCODING"] = []string{"br, gzip"}

	canonicalKey, err := builder.Build(canonical)
	if err != nil {
		t.Fatal(err)
	}
	lowercaseKey, err := builder.Build(lowercase)
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalKey.Equal(lowercaseKey) {
		t.Fatal("header name casing changed the key")
	}

	lowercase.Header["accept-encoding"] = []string{"gzip"}
	differentEncoding, err := builder.Build(lowercase)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalKey.Equal(differentEncoding) {
		t.Fatal("different negotiation header values produced the same key")
	}
}

func TestKeyCanonicalizesQueryEscapes(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})

	escaped := mustBuildKey(t, builder, "/search?q=hello+world&role=us%65r")
	canonical := mustBuildKey(t, builder, "/search?role=user&q=hello%20world")
	if !escaped.Equal(canonical) {
		t.Fatalf("query escape normalization differs: %q vs %q", escaped.NormalizedQuery, canonical.NormalizedQuery)
	}

	request := mustRequest("/search?q=%zz")
	if _, err := builder.Build(request); err == nil {
		t.Fatal("invalid percent escape was accepted")
	}
}

func TestKeyUsesFrozenRequestTarget(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})

	request := mustRequest("/original?role=user")
	frozen := mustBuildKeyRequest(t, builder, request)
	request.RequestURI = "/rewritten?role=admin"
	frozenAgain := mustBuildKeyRequest(t, builder, request)

	if frozen.Equal(frozenAgain) {
		t.Fatal("RequestURI rewrite unexpectedly changed an already computed key")
	}
	if frozen.NormalizedQuery != "role=user" {
		t.Fatalf("computed key was not based on the frozen request target: %q", frozen.NormalizedQuery)
	}
}

func TestCanonicalQueryRejectsSemicolon(t *testing.T) {
	if _, err := CanonicalQuery("a=1;b=2"); err == nil {
		t.Fatal("semicolon-separated query was accepted")
	}
}

func TestCanonicalQueryDoesNotCollapseAmbiguousForms(t *testing.T) {
	query, err := CanonicalQuery("a&a=")
	if err != nil {
		t.Fatal(err)
	}
	if query != "a&a=" {
		t.Fatalf("valueless and empty-valued parameters were collapsed: %q", query)
	}

	for _, raw := range []string{"a=1&&b=2", "a=1&", "&a=1"} {
		if _, err := CanonicalQuery(raw); err == nil {
			t.Fatalf("ambiguous query %q was accepted", raw)
		}
	}
}

func TestBuildTargetMatchesBuild(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{Identity: Identity{Site: "site", Tenant: "tenant"}})
	request := mustRequest("/x?a=1&a=2")
	request.Header.Set("Accept", "text/html")

	fromRequest, err := builder.Build(request)
	if err != nil {
		t.Fatal(err)
	}
	fromTarget, err := builder.BuildTarget(builder.Identity(), "/x?a=1&a=2", request.Header)
	if err != nil {
		t.Fatal(err)
	}
	if !fromRequest.Equal(fromTarget) {
		t.Fatal("Build and BuildTarget disagree")
	}
}

func TestBuildTargetHandlesAbsoluteForm(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})

	originForm := mustBuildKey(t, builder, "/x?a=1&a=2")
	absoluteForm, err := builder.BuildTarget(Identity{}, "https://origin.example/x?a=1&a=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !originForm.Equal(absoluteForm) {
		t.Fatal("absolute-form target was not normalized to the same path and query")
	}

	if _, err := builder.BuildTarget(Identity{}, "", nil); err == nil {
		t.Fatal("empty request target was accepted")
	}
}

func mustBuildKey(t *testing.T, builder *KeyBuilder, target string) Key {
	t.Helper()
	return mustBuildKeyRequest(t, builder, mustRequest(target))
}

func mustBuildKeyRequest(t *testing.T, builder *KeyBuilder, request *http.Request) Key {
	t.Helper()
	key, err := builder.Build(request)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustRequest(target string) *http.Request {
	return httptest.NewRequest(http.MethodGet, target, nil)
}
