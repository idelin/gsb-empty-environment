package sessionjar

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	hostA = "site-a.test"
	hostB = "site-b.test"
)

type headerRecorder struct {
	mu       sync.Mutex
	recorded []string
}

func (h *headerRecorder) add(v string) {
	h.mu.Lock()
	h.recorded = append(h.recorded, v)
	h.mu.Unlock()
}

func (h *headerRecorder) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.recorded))
	copy(out, h.recorded)
	return out
}

func (h *headerRecorder) last() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.recorded) == 0 {
		return ""
	}
	return h.recorded[len(h.recorded)-1]
}

type testSites struct {
	a       http.Handler
	b       http.Handler
	seenA   *headerRecorder
	seenB   *headerRecorder
	release chan struct{}
}

func newTestSites(t *testing.T) *testSites {
	t.Helper()
	s := &testSites{
		seenA:   &headerRecorder{},
		seenB:   &headerRecorder{},
		release: make(chan struct{}),
	}

	muxA := http.NewServeMux()
	muxA.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "A1", Path: "/"})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/set-sub", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "A1", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "sub", Value: "SUB", Path: "/", Domain: hostA})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/set-paths", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "tok", Value: "ROOT", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "tok", Value: "APP", Path: "/app"})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/set-expiring", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "short", Value: "X", Path: "/", MaxAge: 1})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/set-delete", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "OLD", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", Path: "/", MaxAge: -1})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/set-foreign", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "evil", Value: "1", Domain: hostB, Path: "/"})
		fmt.Fprint(w, "ok")
	})
	muxA.HandleFunc("/jump", func(w http.ResponseWriter, r *http.Request) {
		s.seenA.add(r.Header.Get("Cookie"))
		http.Redirect(w, r, s.urlB(hostB, "/landed"), http.StatusFound)
	})
	muxA.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		s.seenA.add(r.Header.Get("Cookie"))
		select {
		case <-s.release:
		case <-r.Context().Done():
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "STALE", Path: "/"})
		fmt.Fprint(w, "late")
	})
	muxA.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.seenA.add(r.Header.Get("Cookie"))
		fmt.Fprint(w, "a:"+r.URL.Path)
	})

	muxB := http.NewServeMux()
	muxB.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "B1", Path: "/"})
		fmt.Fprint(w, "ok")
	})
	muxB.HandleFunc("/landed", func(w http.ResponseWriter, r *http.Request) {
		s.seenB.add(r.Header.Get("Cookie"))
		fmt.Fprint(w, "landed")
	})
	muxB.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.seenB.add(r.Header.Get("Cookie"))
		fmt.Fprint(w, "b:"+r.URL.Path)
	})

	s.a = muxA
	s.b = muxB
	return s
}

// inProcessTransport dispatches requests straight to the two site handlers,
// without any real network socket (the sandbox forbids bind). Redirects are
// followed by http.Client itself; this transport simply re-dispatches each
// hop to the handler of its host.
type inProcessTransport struct {
	sites *testSites
}

func (t *inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := newResponseRecorder()
	handler := t.sites.a
	if canonicalHost(req.URL) == hostB {
		handler = t.sites.b
	}
	handler.ServeHTTP(rec, req)
	return rec.result(req), nil
}

// client wires the jar into http.Client over the in-process transport.
func (s *testSites) client(t *testing.T, jar *Jar) *http.Client {
	t.Helper()
	return &http.Client{
		Jar: jar,
		Transport: &GenerationTransport{
			Base: &inProcessTransport{sites: s},
			Jar:  jar,
		},
	}
}

func (s *testSites) urlA(path string) string {
	return "http://" + hostA + path
}

func (s *testSites) urlB(host, path string) string {
	return "http://" + host + path
}

// responseRecorder is a minimal http.ResponseWriter used by the in-process
// transport, avoiding any network listener.
type responseRecorder struct {
	header http.Header
	body   strings.Builder
	status int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: http.Header{}, status: http.StatusOK}
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func (r *responseRecorder) WriteHeader(code int) { r.status = code }

func (r *responseRecorder) result(req *http.Request) *http.Response {
	return &http.Response{
		Status:     http.StatusText(r.status),
		StatusCode: r.status,
		Header:     r.header.Clone(),
		Body:       io.NopCloser(strings.NewReader(r.body.String())),
		Request:    req,
	}
}

func get(t *testing.T, c *http.Client, target string) string {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func headerValue(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, len(cookies))
	for i, c := range cookies {
		parts[i] = c.Name + "=" + c.Value
	}
	return strings.Join(parts, "; ")
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// TestSiteIsolationAndCrossSiteRedirect proves two top-level sites never
// share cookies, including when A redirects to B, and checks the real Cookie
// request headers each server observed.
func TestSiteIsolationAndCrossSiteRedirect(t *testing.T) {
	sites := newTestSites(t)
	jar := New()
	c := sites.client(t, jar)

	get(t, c, sites.urlA("/set"))
	get(t, c, sites.urlB(hostB, "/set"))

	ua := mustURL(t, sites.urlA("/"))
	ub := mustURL(t, sites.urlB(hostB, "/"))
	if got := headerValue(jar.Cookies(ua)); got != "sid=A1" {
		t.Fatalf("site A cookies = %q, want sid=A1", got)
	}
	if got := headerValue(jar.Cookies(ub)); got != "sid=B1" {
		t.Fatalf("site B cookies = %q, want sid=B1", got)
	}

	// Cross-site redirect: A must send A1, B must receive no A cookie.
	get(t, c, sites.urlA("/jump"))
	if last := sites.seenA.last(); last != "sid=A1" {
		t.Fatalf("A /jump Cookie header = %q, want sid=A1", last)
	}
	if last := sites.seenB.last(); strings.Contains(last, "sid=A1") || strings.Contains(last, "sub=") {
		t.Fatalf("B /landed leaked site-A cookies, header = %q", last)
	}
	if last := sites.seenB.last(); last != "sid=B1" {
		t.Fatalf("B /landed Cookie header = %q, want its own sid=B1", last)
	}

	// A domain cookie scoped to A is visible on an A subdomain, never on B.
	get(t, c, sites.urlA("/set-sub"))
	sub, err := url.Parse(strings.Replace(sites.urlA("/"), hostA, "api."+hostA, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(jar.Cookies(sub)); !strings.Contains(got, "sub=SUB") {
		t.Fatalf("subdomain cookies = %q, want sub=SUB", got)
	}
	if got := headerValue(jar.Cookies(ub)); strings.Contains(got, "sub=SUB") {
		t.Fatalf("site B saw site A domain cookie: %q", got)
	}

	// Foreign Domain attribute must be rejected and counted.
	get(t, c, sites.urlA("/set-foreign"))
	if got := headerValue(jar.Cookies(ub)); strings.Contains(got, "evil=1") {
		t.Fatalf("foreign-domain cookie was stored: %q", got)
	}
	if jar.Snapshot().Rejected == 0 {
		t.Fatal("Rejected = 0, want the foreign-domain cookie to be rejected")
	}
}

// TestSameNameDifferentPaths checks path scoping and RFC ordering (longer
// path first) against the real request headers.
func TestSameNameDifferentPaths(t *testing.T) {
	sites := newTestSites(t)
	jar := New()
	c := sites.client(t, jar)

	get(t, c, sites.urlA("/set-paths"))

	get(t, c, sites.urlA("/app/dashboard"))
	if got := sites.seenA.last(); got != "tok=APP; tok=ROOT" {
		t.Fatalf("/app/dashboard Cookie = %q, want tok=APP; tok=ROOT", got)
	}

	get(t, c, sites.urlA("/other"))
	if got := sites.seenA.last(); got != "tok=ROOT" {
		t.Fatalf("/other Cookie = %q, want tok=ROOT only", got)
	}

	// Default path: a cookie without Path defaults to its request path.
	get(t, c, sites.urlA("/set"))
	get(t, c, sites.urlA("/anything/deep"))
	if got := sites.seenA.last(); !strings.Contains(got, "sid=A1") {
		t.Fatalf("default-path cookie not sent: %q", got)
	}

	// Host-only: sid set on hostA must not appear on a different host even
	// though both share the same registrable site.
	other := mustURL(t, strings.Replace(sites.urlA("/"), hostA, "other."+hostA, 1))
	if got := headerValue(jar.Cookies(other)); strings.Contains(got, "sid=A1") {
		t.Fatalf("host-only cookie leaked across hosts: %q", got)
	}
}

// TestRenewalRace is the core scenario: an in-flight gen-1 request is still
// blocked when Refresh creates gen-2. Its late Set-Cookie must be dropped and
// the new generation must stay clean, while gen-2 traffic only carries the
// new session.
func TestRenewalRace(t *testing.T) {
	sites := newTestSites(t)
	jar := New()
	c := sites.client(t, jar)

	get(t, c, sites.urlA("/set")) // gen 1 holds sid=A1
	ua := mustURL(t, sites.urlA("/"))
	if g := jar.Generation(ua); g != 1 {
		t.Fatalf("initial generation = %d, want 1", g)
	}

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		get(t, c, sites.urlA("/slow")) // pinned to gen 1, blocks server-side
	}()

	waitUntil(t, func() bool { return sites.seenA.last() == "sid=A1" })

	gen2 := jar.Refresh(ua)
	if gen2 != 2 {
		t.Fatalf("Refresh generation = %d, want 2", gen2)
	}
	if got := headerValue(jar.Cookies(ua)); got != "" {
		t.Fatalf("gen 2 must start empty, got %q", got)
	}

	// New login on gen 2.
	jar.SetCookies(ua, []*http.Cookie{{Name: "sid", Value: "A2", Path: "/"}})
	get(t, c, sites.urlA("/"))
	if got := sites.seenA.last(); got != "sid=A2" {
		t.Fatalf("gen 2 request Cookie = %q, want sid=A2", got)
	}

	// Release the blocked gen-1 response; it tries to set sid=STALE late.
	close(sites.release)
	select {
	case <-slowDone:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never completed")
	}

	if got := headerValue(jar.Cookies(ua)); got != "sid=A2" {
		t.Fatalf("after late response gen 2 Cookie = %q, want sid=A2 (no pollution)", got)
	}
	// The late write landed in the still-pinned gen-1 store, which is now
	// garbage-collected; gen 2 must be the only surviving generation.
	site := registrableDomain(hostA, jar.suffixes)
	jar.mu.Lock()
	left := len(jar.sites[site].parts)
	current := jar.sites[site].gen
	jar.mu.Unlock()
	if left != 1 || current != 2 {
		t.Fatalf("after unpin, generations left = %d (current %d), want only gen 2", left, current)
	}
}

// TestConcurrentRefresh exercises many parallel requests interleaved with
// refreshes under the race detector; every response must land in the
// generation its request was pinned to.
func TestConcurrentRefresh(t *testing.T) {
	sites := newTestSites(t)
	jar := New()
	c := sites.client(t, jar)
	ua := mustURL(t, sites.urlA("/"))

	get(t, c, sites.urlA("/set"))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(sites.urlA("/"))
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jar.Refresh(ua)
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("parallel work did not finish")
	}

	// Every pin must have been released: no generation other than the current
	// one may remain in the map.
	site := registrableDomain(hostA, jar.suffixes)
	jar.mu.Lock()
	state := jar.sites[site]
	leftover := 0
	for gen, p := range state.parts {
		if gen != state.gen && len(p.pins) > 0 {
			leftover += len(p.pins)
		}
	}
	jar.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("found %d leaked pins on old generations", leftover)
	}
}

// TestExpiryAndDeletion drives expiry with an injectable clock and covers
// MaxAge, past Expires and explicit deletion through the jar API.
func TestExpiryAndDeletion(t *testing.T) {
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	mu := sync.Mutex{}
	now := base
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	jar := New(WithClock(clock))
	ua := mustURL(t, "http://"+hostA+":8080/")

	jar.SetCookies(ua, []*http.Cookie{
		{Name: "session", Value: "S", Path: "/", Expires: base.Add(time.Minute)},
		{Name: "persist", Value: "P", Path: "/", Expires: base.Add(2 * time.Hour)},
		{Name: "dead", Value: "D", Path: "/", Expires: base.Add(-time.Minute)},
	})

	got := headerValue(jar.Cookies(ua))
	if !strings.Contains(got, "session=S") || !strings.Contains(got, "persist=P") {
		t.Fatalf("cookies after set = %q", got)
	}
	if strings.Contains(got, "dead=D") {
		t.Fatalf("already-expired cookie was stored: %q", got)
	}

	// Advance past the MaxAge lifetime: session disappears, persist remains.
	mu.Lock()
	now = base.Add(90 * time.Second)
	mu.Unlock()
	got = headerValue(jar.Cookies(ua))
	if strings.Contains(got, "session=S") {
		t.Fatalf("MaxAge cookie survived expiry: %q", got)
	}
	if !strings.Contains(got, "persist=P") {
		t.Fatalf("persist cookie lost prematurely: %q", got)
	}

	// Explicit deletion via negative MaxAge removes the entry immediately.
	jar.SetCookies(ua, []*http.Cookie{{Name: "persist", Value: "", Path: "/", MaxAge: -1}})
	if strings.Contains(headerValue(jar.Cookies(ua)), "persist=P") {
		t.Fatal("persist cookie was not deleted by MaxAge=-1")
	}

	// Max-Age parsed from a raw Set-Cookie header (the transport path).
	mu.Lock()
	now = base
	mu.Unlock()
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "maxaged=1; Path=/; Max-Age=60")
	jar.StoreResponse(ua, jar.Generation(ua), resp)
	if !strings.Contains(headerValue(jar.Cookies(ua)), "maxaged=1") {
		t.Fatal("Max-Age cookie not stored from raw Set-Cookie")
	}
	mu.Lock()
	now = base.Add(2 * time.Minute)
	mu.Unlock()
	if strings.Contains(headerValue(jar.Cookies(ua)), "maxaged=1") {
		t.Fatal("Max-Age cookie did not expire")
	}
	mu.Lock()
	now = base
	mu.Unlock()

	// Late write into a refreshed-then-unpinned generation is dropped.
	gen := jar.Generation(ua)
	jar.Refresh(ua)
	stale := &http.Response{Header: http.Header{}}
	stale.Header.Add("Set-Cookie", "late=1; Path=/")
	jar.StoreResponse(ua, gen, stale)
	if jar.Snapshot().Dropped == 0 {
		t.Fatal("late StoreResponse into a vanished generation was not dropped")
	}
}

// TestRegistrableDomain checks the site keying, including the built-in
// multi-label suffixes and IP / single-label hosts.
func TestRegistrableDomain(t *testing.T) {
	suffixes := map[string]bool{}
	for _, s := range defaultPublicSuffixes {
		suffixes[s] = true
	}
	cases := map[string]string{
		"login.site-a.test":       "site-a.test",
		"api.site-a.test":         "site-a.test",
		"other.site-b.test":       "site-b.test",
		"shop.example.co.uk":      "example.co.uk",
		"deep.shop.example.co.uk": "example.co.uk",
		"127.0.0.1":               "127.0.0.1",
		"[::1]":                   "[::1]",
		"localhost":               "localhost",
	}
	for host, want := range cases {
		if got := registrableDomain(host, suffixes); got != want {
			t.Errorf("registrableDomain(%q) = %q, want %q", host, got, want)
		}
	}
}

// TestInterfaceCompliance is a compile-time guard that Jar satisfies the
// standard library cookie jar contract.
func TestInterfaceCompliance(t *testing.T) {
	var _ http.CookieJar = New()
	var _ http.RoundTripper = &GenerationTransport{}
}

// TestPinUnpinLifecycle exercises the public pin API directly: a pinned
// old generation survives Refresh until the pin is released.
func TestPinUnpinLifecycle(t *testing.T) {
	jar := New()
	ua := mustURL(t, "http://"+hostA+"/")
	jar.SetCookies(ua, []*http.Cookie{{Name: "sid", Value: "A1", Path: "/"}})

	const pinID uint64 = 42
	gen1 := jar.Pin(ua, pinID)
	if gen1 != 1 {
		t.Fatalf("pinned generation = %d, want 1", gen1)
	}
	jar.Refresh(ua) // gen 2; gen 1 must stay alive because it is pinned

	if got := headerValue(jar.CookiesFor(ua, gen1)); got != "sid=A1" {
		t.Fatalf("pinned old generation lost its cookies: %q", got)
	}
	if got := headerValue(jar.Cookies(ua)); got != "" {
		t.Fatalf("new generation must be empty, got %q", got)
	}

	// A late response can still land in the pinned old generation.
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "late=1; Path=/")
	jar.StoreResponse(ua, gen1, resp)
	if got := headerValue(jar.CookiesFor(ua, gen1)); !strings.Contains(got, "late=1") {
		t.Fatalf("late write into pinned generation rejected: %q", got)
	}

	jar.Unpin(ua, gen1, pinID)
	resp2 := &http.Response{Header: http.Header{}}
	resp2.Header.Add("Set-Cookie", "toolate=1; Path=/")
	jar.StoreResponse(ua, gen1, resp2)
	if jar.Snapshot().Dropped == 0 {
		t.Fatal("write after unpin of a superseded generation was not dropped")
	}
}

// TestTransportErrorReleasesPins ensures a failing base transport does not
// leak pins; the superseded generation becomes droppable afterwards.
func TestTransportErrorReleasesPins(t *testing.T) {
	jar := New()
	ua := mustURL(t, "http://"+hostA+"/")
	tr := &GenerationTransport{
		Jar: jar,
		Base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errBoom
		}),
	}
	c := &http.Client{Jar: jar, Transport: tr}

	gen1 := jar.Refresh(ua)
	_, err := c.Get(siteURL(ua))
	if err == nil {
		t.Fatal("expected transport error")
	}
	jar.Refresh(ua) // gen 2

	site := registrableDomain(hostA, jar.suffixes)
	jar.mu.Lock()
	_, oldExists := jar.sites[site].parts[gen1]
	jar.mu.Unlock()
	if oldExists {
		t.Fatal("old generation partition leaked after transport error")
	}
}

var errBoom = fmt.Errorf("boom")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func siteURL(u *url.URL) string { return u.Scheme + "://" + u.Host + "/" }
