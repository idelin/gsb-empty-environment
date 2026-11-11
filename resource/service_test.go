package resource

import (
	"bytes"
	"errors"
	"testing"
)

func newService() *Service {
	return NewService(NewSigner([]byte("test-secret")), NewCache(), NewStore())
}

func (s *Service) mustStore(t *testing.T, raw string, data []byte) ID {
	t.Helper()
	id := MustParse(raw)
	s.store.Put(id, data)
	return id
}

// TestSignThenEquivalentRewrite: a URL signed in one spelling must still
// authorize, hit the same cache entry, and open the same object when the
// request arrives rewritten with duplicate slashes or re-encoded bytes.
func TestSignThenEquivalentRewrite(t *testing.T) {
	svc := newService()
	stored := svc.mustStore(t, "/dl/report/final.csv", []byte("csv-bytes"))

	sig := svc.Sign(stored)

	rewrites := []string{
		"/dl//report/final.csv",
		"/dl/report/%66inal.csv",
		"/dl///report//%66%69%6Eal.csv",
	}
	for _, raw := range rewrites {
		id := MustParse(raw)
		data, err := svc.Fetch(id, sig)
		if err != nil {
			t.Fatalf("Fetch(%q) with signature over %q failed: %v", raw, stored, err)
		}
		if !bytes.Equal(data, []byte("csv-bytes")) {
			t.Errorf("Fetch(%q) = %q, want %q", raw, data, "csv-bytes")
		}
	}
}

// TestSignThenTamperedRewrite: rewriting the request so it denotes a
// different resource must invalidate the signature, even when the object
// exists in the store.
func TestSignThenTamperedRewrite(t *testing.T) {
	svc := newService()
	public := svc.mustStore(t, "/dl/report/public.csv", []byte("public"))
	svc.mustStore(t, "/dl/report/secret.csv", []byte("secret"))

	sig := svc.Sign(public)

	tampered := []string{
		"/dl/report/secret.csv",          // different object
		"/dl/report/public.csv?admin=1",  // added parameter
		"/dl/report/public.csv%20",       // trailing space in name
		"/dl//report//..//report/public.csv", // ".." is data, not traversal
	}
	for _, raw := range tampered {
		id := MustParse(raw)
		if _, err := svc.Fetch(id, sig); !errors.Is(err, ErrForbidden) {
			t.Errorf("Fetch(%q) with public.csv signature = %v, want ErrForbidden", raw, err)
		}
	}
}

// TestCacheAndStoreShareKeyWithAuth: after a signed fetch populates the
// cache, an equivalent spelling is served from the cache (store object
// removed would still work), while a merely similar resource misses.
func TestCacheAndStoreShareKeyWithAuth(t *testing.T) {
	svc := newService()
	stored := svc.mustStore(t, "/dl/a//b.txt?q=a+b", []byte("plus-data"))
	sig := svc.Sign(stored)

	if _, err := svc.Fetch(stored, sig); err != nil {
		t.Fatalf("priming fetch failed: %v", err)
	}

	// Equivalent spelling hits the same cache entry.
	equiv := MustParse("/dl/a/b.txt?q=a%2Bb")
	if _, ok := svc.cache.Get(equiv); !ok {
		t.Errorf("equivalent ID %q did not share cache entry with %q", equiv, stored)
	}

	// The space variant is a different resource: no cache entry, no object.
	spaceID := MustParse("/dl/a/b.txt?q=a%20b")
	if _, ok := svc.cache.Get(spaceID); ok {
		t.Errorf("space variant %q must not share the plus variant's cache entry", spaceID)
	}
	spaceSig := svc.Sign(spaceID)
	if _, err := svc.Fetch(spaceID, spaceSig); !errors.Is(err, ErrNotFound) {
		t.Errorf("Fetch(%q) = %v, want ErrNotFound", spaceID, err)
	}
}

// TestDistinctResourcesServedFromSameStore: two resources that the legacy
// keying scheme conflates coexist in one store and are each served
// correctly through the full sign/fetch path.
func TestDistinctResourcesServedFromSameStore(t *testing.T) {
	svc := newService()
	plus := svc.mustStore(t, "/dl?q=a+b", []byte("literal-plus"))
	space := svc.mustStore(t, "/dl?q=a%20b", []byte("space"))
	forward := svc.mustStore(t, "/dl?a=1&a=2", []byte("one-two"))
	reversed := svc.mustStore(t, "/dl?a=2&a=1", []byte("two-one"))

	cases := []struct {
		id   ID
		want string
	}{
		{plus, "literal-plus"},
		{space, "space"},
		{forward, "one-two"},
		{reversed, "two-one"},
	}
	for _, tc := range cases {
		data, err := svc.Fetch(tc.id, svc.Sign(tc.id))
		if err != nil {
			t.Fatalf("Fetch(%q) failed: %v", tc.id, err)
		}
		if string(data) != tc.want {
			t.Errorf("Fetch(%q) = %q, want %q", tc.id, data, tc.want)
		}
	}
}

// TestFetchRejectsBadSignatureShapes: malformed signatures are forbidden,
// not crashes.
func TestFetchRejectsBadSignatureShapes(t *testing.T) {
	svc := newService()
	id := svc.mustStore(t, "/dl/x", []byte("x"))
	for _, sig := range []string{"", "not-hex", "zz", "abcd"} {
		if _, err := svc.Fetch(id, sig); !errors.Is(err, ErrForbidden) {
			t.Errorf("Fetch with signature %q = %v, want ErrForbidden", sig, err)
		}
	}
}
