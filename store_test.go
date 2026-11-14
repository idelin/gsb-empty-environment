package netsec

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreRejectsDuplicateParameterCrossWrite(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)

	attacker := mustRequest("/checkout?role=admin&role=user")
	victim := mustRequest("/checkout?role=user&role=admin")
	attackerKey := mustBuildKeyRequest(t, builder, attacker)

	// The origin echoes the digest of the attacker's actual request.
	response := testResponse(attacker, http.StatusOK, "attacker-admin-view")
	response.Header.Set("Cache-Control", "public, max-age=60")
	response.Header.Set(HeaderCacheKeyDigest, attackerKey.Hex())
	if err := store.Put(attacker, response, ""); err != nil {
		t.Fatalf("store attacker response: %v", err)
	}

	if body, ok := getBody(t, store, attacker); !ok || body != "attacker-admin-view" {
		t.Fatalf("attacker's own request did not hit: body=%q hit=%v", body, ok)
	}
	if body, ok := getBody(t, store, victim); ok {
		t.Fatalf("attacker response leaked to reordered duplicate parameters: %q", body)
	}

	victimKey := mustBuildKeyRequest(t, builder, victim)
	victimResponse := testResponse(victim, http.StatusOK, "victim-user-view")
	victimResponse.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(victim, victimResponse, victimKey.Hex()); err != nil {
		t.Fatal(err)
	}

	if body, ok := getBody(t, store, victim); !ok || body != "victim-user-view" {
		t.Fatalf("victim response missing: body=%q hit=%v", body, ok)
	}
	if body, ok := getBody(t, store, attacker); !ok || body != "attacker-admin-view" {
		t.Fatalf("attacker variant was replaced or leaked: body=%q hit=%v", body, ok)
	}
}

func TestStoreSeparatesSameNameDifferentValues(t *testing.T) {
	store := NewStore(NewKeyBuilder(KeyConfig{}))

	admin := mustRequest("/api?role=admin")
	user := mustRequest("/api?role=user")
	key := mustBuildKeyRequest(t, store.KeyBuilder(), admin)

	response := testResponse(admin, http.StatusOK, "admin")
	response.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(admin, response, key.Hex()); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Get(user); ok {
		t.Fatal("different value for the same parameter hit the admin entry")
	}
}

func TestStoreTenantSamePathIsolation(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)
	request := mustRequest("/dashboard")

	tenantA := Identity{Site: "cdn.example", Tenant: "alpha"}
	tenantB := Identity{Site: "cdn.example", Tenant: "beta"}
	keyA, err := builder.BuildFor(request, tenantA)
	if err != nil {
		t.Fatal(err)
	}

	response := testResponse(request, http.StatusOK, "alpha-dashboard")
	response.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.PutFor(request, tenantA, response, keyA.Hex()); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.GetFor(request, tenantB); ok {
		t.Fatal("tenant B hit tenant A's entry for the same path")
	}
	if body, ok := getBodyFor(t, store, request, tenantA); !ok || body != "alpha-dashboard" {
		t.Fatalf("tenant A entry missing: body=%q hit=%v", body, ok)
	}
}

func TestStoreVaryUsesCaseInsensitiveRequestHeaders(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{NegotiationHeaders: []string{}})
	store := NewStore(builder)

	english := mustRequest("/welcome")
	english.Header["accept-language"] = []string{"en-US"}
	french := mustRequest("/welcome")
	french.Header["Accept-Language"] = []string{"fr-FR"}

	key := mustBuildKeyRequest(t, builder, english)
	response := testResponse(english, http.StatusOK, "hello")
	// Use non-canonical response header casing to exercise direct map access.
	response.Header["cache-control"] = []string{"public, max-age=60"}
	response.Header["vary"] = []string{"accept-language"}
	response.Header["x-cache-key-digest"] = []string{key.Hex()}
	if err := store.Put(english, response, ""); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Get(french); ok {
		t.Fatal("French request hit the English Vary bucket")
	}
	hit, ok := store.Lookup(english)
	if !ok {
		t.Fatal("English Vary bucket was not found")
	}
	if got := hit.Response.Header.Get("Vary"); got != "accept-language" {
		t.Fatalf("response Vary header not preserved: %q", got)
	}
	if body, err := io.ReadAll(hit.Response.Body); err != nil || string(body) != "hello" {
		t.Fatalf("unexpected hit body %q, err %v", body, err)
	}
}

func TestStoreAuthorizationIsAlwaysBucketed(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)

	alice := mustRequest("/private/data")
	alice.Header.Set("Authorization", "Bearer alice")
	bob := mustRequest("/private/data")
	bob.Header.Set("Authorization", "Bearer bob")
	anonymous := mustRequest("/private/data")

	aliceKey := mustBuildKeyRequest(t, builder, alice)
	response := testResponse(alice, http.StatusOK, "alice-data")
	response.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(alice, response, aliceKey.Hex()); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Get(bob); ok {
		t.Fatal("Bob hit Alice's Authorization bucket")
	}
	if _, ok := store.Get(anonymous); ok {
		t.Fatal("anonymous request hit an Authorization bucket")
	}
	if body, ok := getBody(t, store, alice); !ok || body != "alice-data" {
		t.Fatalf("Alice's own request missed: body=%q hit=%v", body, ok)
	}
}

func TestStoreRejectsUnsafeSharedResponses(t *testing.T) {
	tests := []struct {
		name   string
		header map[string][]string
	}{
		{
			name:   "private",
			header: map[string][]string{"Cache-Control": {"private, max-age=60"}},
		},
		{
			name:   "no-store lowercase",
			header: map[string][]string{"cache-control": {"no-store"}},
		},
		{
			name:   "set-cookie mixed case",
			header: map[string][]string{"sEt-CoOkIe": {"session=secret"}, "Cache-Control": {"public, max-age=60"}},
		},
		{
			name:   "vary wildcard",
			header: map[string][]string{"Vary": {"*"}, "Cache-Control": {"public, max-age=60"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := NewKeyBuilder(KeyConfig{})
			store := NewStore(builder)
			request := mustRequest("/unsafe")
			key := mustBuildKeyRequest(t, builder, request)
			response := testResponse(request, http.StatusOK, "secret")
			response.Header = test.header

			err := store.Put(request, response, key.Hex())
			if !errors.Is(err, ErrNotCacheable) {
				t.Fatalf("expected ErrNotCacheable, got %v", err)
			}
			if _, ok := store.Get(request); ok {
				t.Fatal("unsafe response was served from shared cache")
			}
		})
	}
}

func TestStoreVerifiesOriginDigest(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)

	attacker := mustRequest("/victim?role=user&role=admin")
	other := mustRequest("/victim?role=admin&role=user")
	otherKey := mustBuildKeyRequest(t, builder, other)

	response := testResponse(attacker, http.StatusOK, "poison")
	response.Header.Set("Cache-Control", "public, max-age=60")
	err := store.Put(attacker, response, otherKey.Hex())
	if !errors.Is(err, ErrKeyDigestMismatch) {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	if _, ok := store.Get(attacker); ok {
		t.Fatal("response stored despite digest mismatch")
	}

	missing := testResponse(attacker, http.StatusOK, "poison")
	missing.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(attacker, missing, ""); !errors.Is(err, ErrMissingKeyDigest) {
		t.Fatalf("expected missing digest error, got %v", err)
	}
}

func TestStoreRejectsNormalizationVersionMismatch(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{NormalizationVersion: "v1"})
	store := NewStore(builder)
	request := mustRequest("/asset")
	request.Header.Set(HeaderNormalizationVersion, "v1")
	key := mustBuildKeyRequest(t, builder, request)

	response := testResponse(request, http.StatusOK, "body")
	response.Header.Set("Cache-Control", "public, max-age=60")
	response.Header.Set(HeaderNormalizationVersion, "v2")
	err := store.Put(request, response, key.Hex())
	if !errors.Is(err, ErrNormalizationVersionMismatch) {
		t.Fatalf("expected version mismatch, got %v", err)
	}
	if _, ok := store.Get(request); ok {
		t.Fatal("version-mismatched response was stored")
	}

	valid := testResponse(request, http.StatusOK, "body")
	valid.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(request, valid, key.Hex()); err != nil {
		t.Fatal(err)
	}

	requestWithNewVersion := mustRequest("/asset")
	requestWithNewVersion.Header.Set(HeaderNormalizationVersion, "v2")
	if _, ok := store.Get(requestWithNewVersion); ok {
		t.Fatal("request with a different normalization version hit the cache")
	}
}

func TestStoreStaticAssetHitAndBodyIsolation(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)
	request := mustRequest("/static/app.js?v=42")
	request.Header.Set("Accept-Encoding", "br, gzip")
	key := mustBuildKeyRequest(t, builder, request)

	response := testResponse(request, http.StatusOK, "console.log('ok');")
	response.Header["cache-control"] = []string{"public, max-age=300"}
	response.Header["content-type"] = []string{"application/javascript"}
	if err := store.Put(request, response, key.Hex()); err != nil {
		t.Fatal(err)
	}

	// Put restores the origin response body for downstream use.
	originalBody, err := io.ReadAll(response.Body)
	if err != nil || string(originalBody) != "console.log('ok');" {
		t.Fatalf("origin body was not restored: %q, %v", originalBody, err)
	}

	first, ok := store.Get(request)
	if !ok {
		t.Fatal("valid static asset missed")
	}
	if first.StatusCode != http.StatusOK || first.Header.Get("Content-Type") != "application/javascript" {
		t.Fatalf("unexpected stored response: %d, %q", first.StatusCode, first.Header.Get("Content-Type"))
	}
	first.Header.Set("Content-Type", "text/plain")
	firstBody, err := io.ReadAll(first.Body)
	if err != nil || string(firstBody) != "console.log('ok');" {
		t.Fatalf("unexpected first body %q, err %v", firstBody, err)
	}

	second, ok := store.Get(request)
	if !ok {
		t.Fatal("second static asset hit missed")
	}
	if second.Header.Get("Content-Type") != "application/javascript" {
		t.Fatal("mutating one hit changed the stored headers")
	}
}

func TestStoreExpiryUsesInjectedClock(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder, StoreOptions{
		Now: func() time.Time { return current },
	})
	request := mustRequest("/ttl")
	key := mustBuildKeyRequest(t, builder, request)

	response := testResponse(request, http.StatusOK, "fresh")
	response.Header.Set("Cache-Control", "public, max-age=10")
	if err := store.Put(request, response, key.Hex()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(request); !ok {
		t.Fatal("fresh entry missed")
	}

	current = current.Add(11 * time.Second)
	if _, ok := store.Get(request); ok {
		t.Fatal("expired entry hit")
	}
}

func TestStoreRejectsRangesAndRequestNoStore(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder)

	rangeRequest := mustRequest("/video.mp4")
	rangeRequest.Header.Set("Range", "bytes=0-10")
	key := mustBuildKeyRequest(t, builder, rangeRequest)
	response := testResponse(rangeRequest, http.StatusOK, "partial")
	response.Header.Set("Cache-Control", "public, max-age=60")
	if err := store.Put(rangeRequest, response, key.Hex()); !errors.Is(err, ErrNotCacheable) {
		t.Fatalf("range request was cacheable: %v", err)
	}

	noStore := mustRequest("/asset")
	noStore.Header.Set("Cache-Control", "no-store")
	if _, ok := store.Get(noStore); ok {
		t.Fatal("request no-store hit")
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	builder := NewKeyBuilder(KeyConfig{})
	store := NewStore(builder, StoreOptions{MaxEntries: 4096})

	var wait sync.WaitGroup
	var failures atomic.Int32
	for worker := 0; worker < 16; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				target := "/worker/" + string(rune('a'+worker)) + "?v=" + string(rune('a'+iteration%26))
				request := mustRequest(target)
				key, err := builder.Build(request)
				if err != nil {
					failures.Add(1)
					continue
				}
				response := testResponse(request, http.StatusOK, key.Hex())
				response.Header.Set("Cache-Control", "public, max-age=60")
				if err := store.Put(request, response, key.Hex()); err != nil {
					failures.Add(1)
					continue
				}
				if hit, ok := store.Get(request); !ok {
					failures.Add(1)
				} else {
					_, _ = io.Copy(io.Discard, hit.Body)
					_ = hit.Body.Close()
				}
			}
		}()
	}
	wait.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent operations failed", failures.Load())
	}
}

func testResponse(request *http.Request, status int, body string) *http.Response {
	header := make(http.Header)
	return &http.Response{
		Status:        http.StatusText(status),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func getBody(t *testing.T, store *Store, request *http.Request) (string, bool) {
	t.Helper()
	response, ok := store.Get(request)
	if !ok {
		return "", false
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), true
}

func getBodyFor(t *testing.T, store *Store, request *http.Request, identity Identity) (string, bool) {
	t.Helper()
	response, ok := store.GetFor(request, identity)
	if !ok {
		return "", false
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), true
}
