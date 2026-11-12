package varycache_test

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"example.org/netsec/q0134/varycache"
)

func requestHeader(auth string) http.Header {
	h := http.Header{}
	if auth != "" {
		h.Set("Authorization", auth)
	}
	return h
}

func responseHeader(vary string) http.Header {
	h := http.Header{}
	if vary != "" {
		h.Set("Vary", vary)
	}
	return h
}

func TestAdmissionRejectedWhenVaryHeaderNotInKey(t *testing.T) {
	cache := varycache.New([]string{"X-Route"})

	if _, err := cache.Admit("/resource", requestHeader("Bearer a"),
		responseHeader("Authorization"), []byte("secret")); !errors.Is(err, varycache.ErrNotCovered) {
		t.Fatalf("Admit with uncovered Vary: want ErrNotCovered, got %v", err)
	}
	if got := cache.Lookup("/resource", requestHeader("Bearer a")); got != nil {
		t.Fatalf("Lookup after rejected admission: want miss, got %q", got.Payload)
	}

	if _, err := cache.Admit("/resource", requestHeader("Bearer a"),
		responseHeader("*"), nil); !errors.Is(err, varycache.ErrNotCovered) {
		t.Fatalf("Admit with Vary: *: want ErrNotCovered, got %v", err)
	}
}

func TestSamePathDifferentAuthContextsNeverLeak(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization"})

	if _, err := cache.Admit(path, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("alice-body")); err != nil {
		t.Fatalf("Admit alice: %v", err)
	}
	if _, err := cache.Admit(path, requestHeader("Bearer bob"),
		responseHeader("Authorization"), []byte("bob-body")); err != nil {
		t.Fatalf("Admit bob: %v", err)
	}

	cases := []struct {
		name string
		auth string
		want []byte
	}{
		{"alice gets alice", "Bearer alice", []byte("alice-body")},
		{"bob gets bob", "Bearer bob", []byte("bob-body")},
		{"unknown context misses", "Bearer mallory", nil},
		{"unauthenticated request misses", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cache.Lookup(path, requestHeader(tc.auth))
			if tc.want == nil {
				if got != nil {
					t.Fatalf("Lookup %q: want miss, got %q", tc.auth, got.Payload)
				}
				return
			}
			if got == nil || string(got.Payload) != string(tc.want) {
				t.Fatalf("Lookup %q: want %q, got %v", tc.auth, tc.want, got)
			}
		})
	}
}

func TestIdentitySetChangeAtomicallyInvalidatesOldVariants(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization", "X-Tenant"})

	gen1, err := cache.Admit(path, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("v1"))
	if err != nil {
		t.Fatalf("Admit gen1: %v", err)
	}

	tenantReq := http.Header{}
	tenantReq.Set("Authorization", "Bearer alice")
	tenantReq.Set("X-Tenant", "t1")
	gen2, err := cache.Admit(path, tenantReq,
		responseHeader("Authorization, X-Tenant"), []byte("v2"))
	if err != nil {
		t.Fatalf("Admit gen2: %v", err)
	}
	if gen2 <= gen1 {
		t.Fatalf("identity set change must start a new generation: gen1=%d gen2=%d", gen1, gen2)
	}

	if got := cache.Lookup(path, requestHeader("Bearer alice")); got != nil {
		t.Fatalf("old variant must be invalidated, got %q", got.Payload)
	}
	if got := cache.Lookup(path, tenantReq); got == nil || string(got.Payload) != "v2" {
		t.Fatalf("new variant lookup: want v2, got %v", got)
	}

	if current, ok := cache.Generation(path); !ok || current != gen2 {
		t.Fatalf("Generation: want %d, got %d (ok=%v)", gen2, current, ok)
	}

	if err := cache.Revalidate(path, gen1, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("stale")); !errors.Is(err, varycache.ErrStaleGeneration) {
		t.Fatalf("late revalidation of gen1: want ErrStaleGeneration, got %v", err)
	}
	if got := cache.Lookup(path, requestHeader("Bearer alice")); got != nil {
		t.Fatalf("stale revalidation leaked a variant: %q", got.Payload)
	}
}

func TestLateRevalidationDoesNotOverwriteCurrentGeneration(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization", "X-Tenant"})

	gen1, err := cache.Admit(path, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("first"))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}

	// Origin now varies on an additional header; the old in-flight revalidation
	// becomes late.
	newReq := http.Header{}
	newReq.Set("Authorization", "Bearer alice")
	newReq.Set("X-Tenant", "t1")
	if _, err := cache.Admit(path, newReq,
		responseHeader("Authorization, X-Tenant"), []byte("current")); err != nil {
		t.Fatalf("Admit new identity set: %v", err)
	}

	err = cache.Revalidate(path, gen1, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("late"))
	if !errors.Is(err, varycache.ErrStaleGeneration) {
		t.Fatalf("late Revalidate: want ErrStaleGeneration, got %v", err)
	}

	if got := cache.Lookup(path, requestHeader("Bearer alice")); got != nil {
		t.Fatalf("late response served to a request outside its context: %q", got.Payload)
	}
	if got := cache.Lookup(path, newReq); got == nil || string(got.Payload) != "current" {
		t.Fatalf("current variant lookup: want current, got %v", got)
	}
}

func TestRevalidationWithinGenerationUpdatesVariant(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization"})

	gen, err := cache.Admit(path, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("old"))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if err := cache.Revalidate(path, gen, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("fresh")); err != nil {
		t.Fatalf("Revalidate current generation: %v", err)
	}
	got := cache.Lookup(path, requestHeader("Bearer alice"))
	if got == nil || string(got.Payload) != "fresh" {
		t.Fatalf("revalidated lookup: want fresh, got %v", got)
	}
}

func TestStoredSnapshotsAreIndependentOfCaller(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization"})

	req := requestHeader("Bearer alice")
	resp := responseHeader("Authorization")
	payload := []byte("body")
	if _, err := cache.Admit(path, req, resp, payload); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	req.Set("Authorization", "tampered")
	resp.Set("Vary", "X-Evil")
	payload[0] = 'X'

	got := cache.Lookup(path, requestHeader("Bearer alice"))
	if got == nil {
		t.Fatal("snapshot must remain selectable after caller mutates its maps")
	}
	if got.Header.Get("Vary") != "Authorization" || string(got.Payload) != "body" {
		t.Fatalf("stored snapshot was mutated by caller: header=%q body=%q",
			got.Header.Get("Vary"), got.Payload)
	}

	got.Header.Set("Vary", "hijacked")
	got.Payload[0] = 'Y'
	again := cache.Lookup(path, requestHeader("Bearer alice"))
	if again.Header.Get("Vary") != "Authorization" || string(again.Payload) != "body" {
		t.Fatalf("returned copy must not share storage with cache: header=%q body=%q",
			again.Header.Get("Vary"), again.Payload)
	}
}

func TestMultiValuedIdentityHeaderMatchesInOrder(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"X-Scope"})

	storedReq := http.Header{}
	storedReq.Add("X-Scope", "a")
	storedReq.Add("X-Scope", "b")
	if _, err := cache.Admit(path, storedReq, responseHeader("X-Scope"),
		[]byte("scoped")); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	sameOrder := storedReq.Clone()
	if got := cache.Lookup(path, sameOrder); got == nil {
		t.Fatal("identical multi-valued header must match")
	}

	reversed := http.Header{}
	reversed.Add("X-Scope", "b")
	reversed.Add("X-Scope", "a")
	if got := cache.Lookup(path, reversed); got != nil {
		t.Fatalf("reordered field values must not match, got %q", got.Payload)
	}
}

func TestParseVary(t *testing.T) {
	h := http.Header{}
	h.Add("Vary", "authorization, X-Tenant")
	h.Add("Vary", "Authorization")
	got, ok := varycache.ParseVary(h)
	if !ok {
		t.Fatal("non-wildcard Vary must be parseable")
	}
	want := []string{"Authorization", "X-Tenant"}
	if len(got) != len(want) {
		t.Fatalf("ParseVary: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseVary: want %v, got %v", want, got)
		}
	}

	if _, ok := varycache.ParseVary(responseHeader("*")); ok {
		t.Fatal("Vary: * must report wildcard")
	}
}

func TestConcurrentAdmitLookupAndRevalidate(t *testing.T) {
	const path = "/resource"
	cache := varycache.New([]string{"Authorization"})

	gen, err := cache.Admit(path, requestHeader("Bearer alice"),
		responseHeader("Authorization"), []byte("seed"))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			cache.Lookup(path, requestHeader("Bearer alice"))
		}()
		go func() {
			defer wg.Done()
			_, _ = cache.Admit(path, requestHeader("Bearer alice"),
				responseHeader("Authorization"), []byte("refresh"))
		}()
		go func() {
			defer wg.Done()
			_ = cache.Revalidate(path, gen, requestHeader("Bearer alice"),
				responseHeader("Authorization"), []byte("refresh"))
		}()
	}
	wg.Wait()

	got := cache.Lookup(path, requestHeader("Bearer bob"))
	if got != nil {
		t.Fatalf("alice's variant leaked to bob under concurrency: %q", got.Payload)
	}
}
