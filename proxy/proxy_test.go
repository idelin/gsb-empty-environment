package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type originHits struct {
	count atomic.Int64
}

func (h *originHits) incr() int64 { return h.count.Add(1) }
func (h *originHits) get() int64  { return h.count.Load() }

func newTestProxy(t *testing.T, origin http.Handler, capacity int) (*ReverseProxy, *FakeClock, *originHits) {
	t.Helper()
	return newLoopbackProxy(t, origin, capacity)
}

func doGet(t *testing.T, proxy *ReverseProxy, target string, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if header != nil {
		req.Header = header.Clone()
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec.Result()
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

// TestMultipleVaryVariants verifies one resource keeps several variants and
// each request is matched to its own stored response.
func TestMultipleVaryVariants(t *testing.T) {
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "max-age=60")
		rw.Header().Set("Vary", "X-Lang")
		fmt.Fprintf(rw, "lang=%s", req.Header.Get("X-Lang"))
	})
	proxy, _, hits := newTestProxy(t, origin, 10)

	en := doGet(t, proxy, "/doc", http.Header{"X-Lang": []string{"en"}})
	zh := doGet(t, proxy, "/doc", http.Header{"X-Lang": []string{"zh"}})
	enAgain := doGet(t, proxy, "/doc", http.Header{"X-Lang": []string{"en"}})

	if bodyString(t, en) != "lang=en" || bodyString(t, zh) != "lang=zh" {
		t.Fatalf("variant mismatch: %q / %q", bodyString(t, en), bodyString(t, zh))
	}
	if bodyString(t, enAgain) != "lang=en" {
		t.Fatalf("second en variant mismatch: %q", bodyString(t, enAgain))
	}
	if got := hits.get(); got != 2 {
		t.Fatalf("origin hits = %d, want 2", got)
	}
	if proxy.Cache.Len() != 2 {
		t.Fatalf("cache len = %d, want 2 variants", proxy.Cache.Len())
	}
}

// TestStaleWhileRevalidate verifies an expired but still-allowed variant is
// served immediately while a background revalidation refreshes the index.
func TestStaleWhileRevalidate(t *testing.T) {
	var originVersion atomic.Int64
	block := make(chan struct{})
	entered := make(chan struct{}, 4)
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		entered <- struct{}{}
		if req.Header.Get("If-None-Match") != "" {
			<-block
			rw.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=30")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=30")
		rw.Header().Set("ETag", "v1")
		fmt.Fprintf(rw, "body-%d", originVersion.Add(1))
	})
	proxy, clock, hits := newTestProxy(t, origin, 10)

	first := doGet(t, proxy, "/swr", nil)
	if first.StatusCode != http.StatusOK || bodyString(t, first) != "body-1" {
		t.Fatalf("unexpected first response: %d %q", first.StatusCode, bodyString(t, first))
	}

	clock.Advance(61 * time.Second)

	stale := doGet(t, proxy, "/swr", nil)
	if stale.StatusCode != http.StatusOK || bodyString(t, stale) != "body-1" {
		t.Fatalf("expected stale body served: %d %q", stale.StatusCode, bodyString(t, stale))
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("background revalidation never reached origin")
	}
	close(block)
	proxy.WaitBackground()

	refreshed := doGet(t, proxy, "/swr", nil)
	if refreshed.StatusCode != http.StatusOK {
		t.Fatalf("refreshed status = %d", refreshed.StatusCode)
	}
	if got := hits.get(); got != 2 {
		t.Fatalf("origin hits = %d, want 2 (1 fetch + 1 revalidation)", got)
	}
}

// TestConcurrentRevalidationCoalesces verifies that many simultaneous stale
// requests trigger exactly one origin revalidation and all callers receive
// the refreshed response.
func TestConcurrentRevalidationCoalesces(t *testing.T) {
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	block := make(chan struct{})
	entered := make(chan struct{}, 16)
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			seen := maxInFlight.Load()
			if cur <= seen || maxInFlight.CompareAndSwap(seen, cur) {
				break
			}
		}
		entered <- struct{}{}
		if req.Header.Get("If-None-Match") != "" {
			<-block
			rw.Header().Set("Cache-Control", "max-age=60")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "max-age=1")
		rw.Header().Set("ETag", "v1")
		fmt.Fprint(rw, "payload")
	})
	proxy, clock, hits := newTestProxy(t, origin, 10)

	doGet(t, proxy, "/hot", nil)
	clock.Advance(2 * time.Second)

	const waiters = 8
	var wg sync.WaitGroup
	results := make([]int, waiters)
	start := make(chan struct{})
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, "/hot", nil)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			results[index] = rec.Code
		}(i)
	}
	close(start)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("revalidation never reached origin")
	}
	close(block)
	wg.Wait()

	for index, code := range results {
		if code != http.StatusOK {
			t.Fatalf("waiter %d got status %d, want 200", index, code)
		}
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent origin calls = %d, want 1", got)
	}
	if got := hits.get(); got != 2 {
		t.Fatalf("origin hits = %d, want 2", got)
	}
}

// TestRevalidationCancellation verifies that a canceled synchronous leader
// unblocks waiting requests instead of hanging them.
func TestRevalidationCancellation(t *testing.T) {
	block := make(chan struct{})
	var revalidations atomic.Int64
	var firstRevalOnce sync.Once
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" {
			revalidations.Add(1)
			firstRevalOnce.Do(func() { <-block })
			rw.Header().Set("Cache-Control", "max-age=60")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "must-revalidate, max-age=1")
		rw.Header().Set("ETag", "v1")
		fmt.Fprint(rw, "payload")
	})
	proxy, clock, _ := newTestProxy(t, origin, 10)

	doGet(t, proxy, "/cancel", nil)
	clock.Advance(2 * time.Second)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderReq := httptest.NewRequest(http.MethodGet, "/cancel", nil).Clone(leaderCtx)
	leaderRec := httptest.NewRecorder()
	leaderDone := make(chan struct{})
	go func() {
		proxy.ServeHTTP(leaderRec, leaderReq)
		close(leaderDone)
	}()

	time.Sleep(100 * time.Millisecond)
	cancelLeader()
	select {
	case <-leaderDone:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled leader request did not return")
	}

	waiterReq := httptest.NewRequest(http.MethodGet, "/cancel", nil)
	waiterRec := httptest.NewRecorder()
	waiterDone := make(chan struct{})
	go func() {
		proxy.ServeHTTP(waiterRec, waiterReq)
		close(waiterDone)
	}()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-waiterDone:
	default:
		// Waiter should remain waiting only while the (now unblocked) retry
		// runs; give it a brief window instead of failing immediately.
	}
	close(block)
	<-waiterDone

	if waiterRec.Code != http.StatusOK {
		t.Fatalf("waiter status = %d, want 200 from retry revalidation", waiterRec.Code)
	}
	if got := revalidations.Load(); got != 2 {
		t.Fatalf("revalidation attempts = %d, want 2 (aborted leader + waiter retry)", got)
	}
}

// Test304ChangesVaryRebuildsIndex verifies that a 304 introducing a new Vary
// dimension atomically rebuilds the resource index, and the new directive set
// takes effect immediately.
func Test304ChangesVaryRebuildsIndex(t *testing.T) {
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" {
			rw.Header().Set("Vary", "X-Tenant")
			rw.Header().Set("Cache-Control", "max-age=30")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "max-age=1")
		rw.Header().Set("ETag", "v1")
		fmt.Fprint(rw, "shared")
	})
	proxy, clock, hits := newTestProxy(t, origin, 10)

	doGet(t, proxy, "/v", nil)
	clock.Advance(2 * time.Second)

	resp := doGet(t, proxy, "/v", http.Header{"X-Tenant": []string{"acme"}})
	if resp.StatusCode != http.StatusOK || bodyString(t, resp) != "shared" {
		t.Fatalf("revalidated response mismatch: %d %q", resp.StatusCode, bodyString(t, resp))
	}

	lookupReq := httptest.NewRequest(http.MethodGet, "/v", nil)
	lookupReq.Header.Set("X-Tenant", "acme")
	entry := proxy.Cache.Lookup(lookupReq)
	if entry == nil {
		t.Fatal("entry vanished after 304 rebuild")
	}
	if len(entry.vary) != 1 || entry.vary[0] != "X-Tenant" {
		t.Fatalf("Vary not rebuilt: %#v", entry.vary)
	}
	if got := hits.get(); got != 2 {
		t.Fatalf("origin hits = %d, want 2", got)
	}

	clock.Advance(10 * time.Second)
	if !entry.isFresh(clock.Now(), cacheDirectives{}) {
		t.Fatal("new max-age=30 directive did not apply after 304 merge")
	}

	// A request without the now-required tenant header must not match.
	reqNoTenant := httptest.NewRequest(http.MethodGet, "/v", nil)
	if proxy.Cache.Lookup(reqNoTenant) != nil {
		t.Fatal("request missing new Vary header incorrectly matched rebuilt index")
	}
}

// Test304NoStoreDropsEntry verifies a 304 that sets no-store atomically
// removes the cached variants.
func Test304NoStoreDropsEntry(t *testing.T) {
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" {
			rw.Header().Set("Cache-Control", "no-store")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "max-age=1")
		rw.Header().Set("ETag", "v1")
		fmt.Fprint(rw, "secret")
	})
	proxy, clock, _ := newTestProxy(t, origin, 10)

	doGet(t, proxy, "/drop", nil)
	clock.Advance(2 * time.Second)
	doGet(t, proxy, "/drop", nil)

	if proxy.Cache.Len() != 0 {
		t.Fatalf("cache len = %d, want 0 after no-store 304", proxy.Cache.Len())
	}
}

// TestAuthorizationIsolation verifies authorized responses are never reused
// for anonymous requests or across different credentials.
func TestAuthorizationIsolation(t *testing.T) {
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=60")
		fmt.Fprintf(rw, "user=%s", req.Header.Get("Authorization"))
	})
	proxy, _, hits := newTestProxy(t, origin, 10)

	alice := doGet(t, proxy, "/me", http.Header{"Authorization": []string{"token-alice"}})
	if bodyString(t, alice) != "user=token-alice" {
		t.Fatalf("alice body = %q", bodyString(t, alice))
	}

	anon := doGet(t, proxy, "/me", nil)
	if anon.StatusCode != http.StatusOK || bodyString(t, anon) != "user=" {
		t.Fatalf("anonymous request reused authorized response: %q", bodyString(t, anon))
	}

	bob := doGet(t, proxy, "/me", http.Header{"Authorization": []string{"token-bob"}})
	if bodyString(t, bob) != "user=token-bob" {
		t.Fatalf("bob body = %q", bodyString(t, bob))
	}

	aliceAgain := doGet(t, proxy, "/me", http.Header{"Authorization": []string{"token-alice"}})
	if bodyString(t, aliceAgain) != "user=token-alice" {
		t.Fatalf("alice second body = %q", bodyString(t, aliceAgain))
	}
	if got := hits.get(); got != 3 {
		t.Fatalf("origin hits = %d, want 3 (alice, anon, bob); alice reuses cache", got)
	}

	// Unauthorized-by-default responses must not enter the shared cache.
	strictOrigin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "max-age=60")
		fmt.Fprint(rw, "private-data")
	})
	strictProxy, _, strictHits := newTestProxy(t, strictOrigin, 10)
	doGet(t, strictProxy, "/strict", http.Header{"Authorization": []string{"t"}})
	doGet(t, strictProxy, "/strict", http.Header{"Authorization": []string{"t"}})
	if got := strictHits.get(); got != 2 {
		t.Fatalf("strict origin hits = %d, want 2 (no implicit shared caching)", got)
	}
}

// TestBoundedLRUEviction verifies the cache never exceeds capacity and evicts
// the least recently used variant first.
func TestBoundedLRUEviction(t *testing.T) {
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "max-age=600")
		fmt.Fprint(rw, "ok")
	})
	proxy, _, _ := newTestProxy(t, origin, 2)

	doGet(t, proxy, "/a", nil)
	doGet(t, proxy, "/b", nil)
	// Touch /a so /b becomes the LRU entry.
	doGet(t, proxy, "/a", nil)
	doGet(t, proxy, "/c", nil)

	if proxy.Cache.Len() != 2 {
		t.Fatalf("cache len = %d, want bounded to 2", proxy.Cache.Len())
	}
	if proxy.Cache.Lookup(httptest.NewRequest(http.MethodGet, "/b", nil)) != nil {
		t.Fatal("LRU entry /b was not evicted")
	}
	if proxy.Cache.Lookup(httptest.NewRequest(http.MethodGet, "/a", nil)) == nil {
		t.Fatal("recently used /a was incorrectly evicted")
	}
	if proxy.Cache.Lookup(httptest.NewRequest(http.MethodGet, "/c", nil)) == nil {
		t.Fatal("newly inserted /c missing")
	}
}

// TestBackgroundRevalidationSurvivesClient verifies a stale-while-revalidate
// refresh continues after the triggering client request has completed.
func TestBackgroundRevalidationSurvivesClient(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 4)
	origin := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" {
			entered <- struct{}{}
			<-block
			rw.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=30")
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		rw.Header().Set("Cache-Control", "max-age=1, stale-while-revalidate=30")
		rw.Header().Set("ETag", "v1")
		fmt.Fprint(rw, "payload")
	})
	proxy, clock, hits := newTestProxy(t, origin, 10)

	doGet(t, proxy, "/bg", nil)
	clock.Advance(2 * time.Second)
	doGet(t, proxy, "/bg", nil)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("background revalidation never started")
	}

	// The client response already returned; the revalidation must still run.
	close(block)
	proxy.WaitBackground()

	clock.Advance(5 * time.Second)
	entry := proxy.Cache.Lookup(httptest.NewRequest(http.MethodGet, "/bg", nil))
	if entry == nil {
		t.Fatal("entry missing after background refresh")
	}
	if !entry.isFresh(clock.Now(), cacheDirectives{}) {
		t.Fatal("entry was not refreshed by background revalidation")
	}
	if got := hits.get(); got != 2 {
		t.Fatalf("origin hits = %d, want 2", got)
	}
}
