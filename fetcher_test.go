package depseal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	goodBlob = "legit-internal-package-blob"
	badBlob  = "tampered-public-package-blob"
)

// responseRecorder is a minimal http.ResponseWriter used by testTransport.
type responseRecorder struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header)}
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) WriteHeader(code int) { r.statusCode = code }

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *responseRecorder) response(req *http.Request) *http.Response {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", r.statusCode, http.StatusText(r.statusCode)),
		StatusCode:    r.statusCode,
		Header:        r.header.Clone(),
		Request:       req,
		Body:          io.NopCloser(bytes.NewReader(r.body.Bytes())),
		ContentLength: int64(r.body.Len()),
	}
}

// testTransport routes synthetic depseal-test:// URLs straight into an
// in-process http.Handler. No network socket is opened. The standard http
// client still performs redirect bookkeeping and calls the fetcher's
// CheckRedirect before each hop, so cross-source enforcement is real.
type testTransport struct {
	servers map[string]http.Handler
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	handler, ok := t.servers[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("test transport: no test source registered for host %q", req.URL.Host)
	}
	rec := newResponseRecorder()
	clone := req.Clone(req.Context())
	clone.RequestURI = ""
	handler.ServeHTTP(rec, clone)
	return rec.response(req), nil
}

func sourceURL(host string) string { return testScheme + "://" + host }

// testHarness bundles the in-process transport and test helpers.
type testHarness struct {
	t         *testing.T
	transport *testTransport
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	return &testHarness{t: t, transport: &testTransport{servers: map[string]http.Handler{}}}
}

func (h *testHarness) serve(host string, handler http.HandlerFunc) {
	h.t.Helper()
	h.transport.servers[host] = handler
}

func (h *testHarness) client() *http.Client {
	return &http.Client{Transport: h.transport}
}

func (h *testHarness) lock(host string, allowedHosts ...string) *Lockfile {
	return mustParse(h.t, validLockfileJSON(h.t, sourceURL(host), allowedHosts...))
}

func (h *testHarness) fetcher(lock *Lockfile, authz Authorizer, opts ...Option) *Fetcher {
	all := append([]Option{WithHTTPClient(h.client())}, opts...)
	return newTestFetcher(h.t, lock, authz, all...)
}

func staticAuthz() Authorizer {
	return NewStaticAuthorizer(map[string][]string{"tenant-a": {"private"}})
}

func blobHandler(blob string, hits *int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		fmt.Fprint(w, blob)
	}
}

// validLockfileJSON builds a lock file whose private source points at
// privateURL and whose package digest matches goodBlob.
func validLockfileJSON(t *testing.T, privateURL string, allowedHosts ...string) string {
	t.Helper()
	good := SHA256Digest([]byte(goodBlob))
	doc := map[string]any{
		"version": 1,
		"sources": map[string]any{
			"private": map[string]any{
				"id":            "private",
				"base_url":      privateURL,
				"allowed_hosts": allowedHosts,
			},
		},
		"packages": []map[string]any{
			{"name": "app/root", "version": "3.0.0", "source": "private", "digest": string(good)},
			{"name": "lib/internal", "version": "1.2.0", "source": "private", "digest": string(good), "parent": "app/root@3.0.0"},
			{"name": "lib/leaf", "version": "0.9.1", "source": "private", "digest": string(good), "parent": "lib/internal@1.2.0"},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func newTestFetcher(t *testing.T, lock *Lockfile, authz Authorizer, opts ...Option) *Fetcher {
	t.Helper()
	defaults := []Option{WithRetries(2), WithBackoff(0)}
	return NewFetcher(lock, authz, append(defaults, opts...)...)
}

// TestCrossSourceRedirectBlocked reproduces the production failure: the
// private source redirects to a public registry that hosts a same-named
// package. The redirect is blocked because the public host is not part of
// the locked source identity.
func TestCrossSourceRedirectBlocked(t *testing.T) {
	h := newTestHarness(t)
	var publicHits int32
	h.serve("public.example", blobHandler(badBlob, &publicHits))
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sourceURL("public.example")+r.URL.Path, http.StatusFound)
	})

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz(), WithRetries(0))

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	_, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)

	var cross *CrossSourceError
	if !errors.As(err, &cross) {
		t.Fatalf("expected CrossSourceError, got %v", err)
	}
	if cross.SourceID != "private" || cross.ToHost == cross.FromHost {
		t.Fatalf("unexpected cross-source detail: %+v", cross)
	}
	if got := atomic.LoadInt32(&publicHits); got != 0 {
		t.Fatalf("public source must never be reached, hits = %d", got)
	}
}

// TestAllowedMirrorRedirect shows that a redirect inside the explicitly
// allowed host set still works when the content matches its digest.
func TestAllowedMirrorRedirect(t *testing.T) {
	h := newTestHarness(t)
	var mirrorHits int32
	h.serve("mirror.example", blobHandler(goodBlob, &mirrorHits))
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sourceURL("mirror.example")+r.URL.Path, http.StatusFound)
	})

	lock := h.lock("private.example", "mirror.example")
	fetcher := h.fetcher(lock, staticAuthz())

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	result, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)
	if err != nil {
		t.Fatalf("allowed mirror fetch failed: %v", err)
	}
	if string(result.Content) != goodBlob || result.Attempts != 1 {
		t.Fatalf("unexpected result: attempts=%d content=%q", result.Attempts, result.Content)
	}
}

// TestDigestMismatchRetriesThenSucceeds serves a bad blob twice and the
// correct blob on the third attempt. The digest check rejects the bad
// attempts and retries until verification succeeds.
func TestDigestMismatchRetriesThenSucceeds(t *testing.T) {
	h := newTestHarness(t)
	var hits int32
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			fmt.Fprint(w, badBlob)
			return
		}
		fmt.Fprint(w, goodBlob)
	})

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz())

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	result, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", result.Attempts)
	}
	if string(result.Content) != goodBlob {
		t.Fatalf("content = %q, want %q", result.Content, goodBlob)
	}

	cached, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)
	if err != nil {
		t.Fatalf("cached fetch: %v", err)
	}
	if !cached.Cached || atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected cache hit, cached=%v hits=%d", cached.Cached, atomic.LoadInt32(&hits))
	}
}

// TestDigestMismatchExhaustsRetries ensures persistent bad content is never
// cached or returned, and the wrapped error preserves the mismatch.
func TestDigestMismatchExhaustsRetries(t *testing.T) {
	h := newTestHarness(t)
	var hits int32
	h.serve("private.example", blobHandler(badBlob, &hits))

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz(), WithRetries(1))

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	_, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)

	var mismatch *DigestMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected DigestMismatchError, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if fetcher.cache.len() != 0 {
		t.Fatal("unverified content must not be cached")
	}
}

// TestTransientStatusRetry covers retry of a 500 followed by a good blob.
func TestTransientStatusRetry(t *testing.T) {
	h := newTestHarness(t)
	var hits int32
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, goodBlob)
	})

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz())

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	result, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
}

// TestTenantCannotBorrowCache verifies that an unauthorized tenant cannot
// reuse a verified blob cached by an authorized tenant. The cache key
// includes the tenant, so the second tenant is denied and no cross-tenant
// key exists.
func TestTenantCannotBorrowCache(t *testing.T) {
	h := newTestHarness(t)
	var hits int32
	h.serve("private.example", blobHandler(goodBlob, &hits))

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz())

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	if _, err := fetcher.FetchPackage(context.Background(), "tenant-a", pkg); err != nil {
		t.Fatalf("authorized fetch: %v", err)
	}
	if fetcher.cache.len() != 1 {
		t.Fatalf("cache entries = %d, want 1", fetcher.cache.len())
	}

	_, err := fetcher.FetchPackage(context.Background(), "tenant-b", pkg)
	var denied *TenantDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected TenantDeniedError, got %v", err)
	}
	if denied.Tenant != "tenant-b" || denied.SourceID != "private" {
		t.Fatalf("unexpected denial detail: %+v", denied)
	}
	if fetcher.cache.len() != 1 {
		t.Fatalf("cache entries = %d, want 1", fetcher.cache.len())
	}
}

// TestParallelFetchSharesDownload ensures concurrent requests for the same
// blob collapse onto one in-flight download and share the verified content.
func TestParallelFetchSharesDownload(t *testing.T) {
	h := newTestHarness(t)
	release := make(chan struct{})
	var hits int32
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// Hold the response until all goroutines have started, so they all
		// observe the same in-flight call.
		<-release
		fmt.Fprint(w, goodBlob)
	})

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz())

	pkg, _ := lock.Lookup("app/root", "3.0.0")
	const waiters = 8
	var (
		wg      sync.WaitGroup
		results = make([]*Result, waiters)
		errs    = make([]error, waiters)
		started = make(chan struct{}, waiters)
	)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started <- struct{}{}
			results[i], errs[i] = fetcher.FetchPackage(context.Background(), "tenant-a", pkg)
		}(i)
	}
	for i := 0; i < waiters; i++ {
		<-started
	}
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("source hits = %d, want 1 shared download", got)
	}
	if fetcher.cache.len() != 1 {
		t.Fatalf("cache entries = %d, want 1", fetcher.cache.len())
	}
	for i, res := range results {
		if string(res.Content) != goodBlob {
			t.Fatalf("waiter %d got wrong content", i)
		}
		if i > 0 && &res.Content[0] == &results[0].Content[0] {
			t.Fatalf("waiter %d shares the underlying byte slice with waiter 0", i)
		}
	}
}

// TestFetchAllParallelAndCached runs the installer-style batch fetch and
// confirms every locked package is resolved with its source binding intact.
func TestFetchAllParallelAndCached(t *testing.T) {
	h := newTestHarness(t)
	var hits int32
	h.serve("private.example", blobHandler(goodBlob, &hits))

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz())

	results, err := fetcher.FetchAll(context.Background(), "tenant-a", 4)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(results) != len(lock.Packages) {
		t.Fatalf("results = %d, want %d", len(results), len(lock.Packages))
	}
	for identity, res := range results {
		if string(res.Content) != goodBlob {
			t.Fatalf("%s has wrong content", identity)
		}
		if res.Package.Source != "private" {
			t.Fatalf("%s lost source binding", identity)
		}
	}
}

// TestFetchErrorCarriesParent ensures failures retain the parent
// provenance so installers can report which dependency chain broke.
func TestFetchErrorCarriesParent(t *testing.T) {
	h := newTestHarness(t)
	h.serve("private.example", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	lock := h.lock("private.example")
	fetcher := h.fetcher(lock, staticAuthz(), WithRetries(0))

	leaf, _ := lock.Lookup("lib/leaf", "0.9.1")
	_, err := fetcher.FetchPackage(context.Background(), "tenant-a", leaf)

	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) {
		t.Fatalf("expected FetchError, got %v", err)
	}
	if fetchErr.Parent != "lib/internal@1.2.0" || fetchErr.Package.Identity() != "lib/leaf@0.9.1" {
		t.Fatalf("provenance lost: %+v", fetchErr)
	}
}
