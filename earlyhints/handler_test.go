package earlyhints

import (
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
)

// memoryCache records every Store call so tests can prove 103 responses never
// reach the cache layer.
type memoryCache struct {
	entries map[string]CachedResponse
	stores  int
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[string]CachedResponse{}}
}

func (c *memoryCache) Lookup(key string) (CachedResponse, bool) {
	entry, ok := c.entries[key]
	return entry, ok
}

func (c *memoryCache) Store(key string, entry CachedResponse) {
	c.stores++
	c.entries[key] = entry
}

func (c *memoryCache) snapshot() map[string]CachedResponse {
	out := make(map[string]CachedResponse, len(c.entries))
	for key, entry := range c.entries {
		out[key] = entry
	}
	return out
}

// hintResponseWriter captures each interim 103 separately instead of folding
// it into the final status the way httptest.ResponseRecorder does.
type hintResponseWriter struct {
	header     http.Header
	hints      []http.Header
	statusCode int
	body       strings.Builder
}

func newHintResponseWriter() *hintResponseWriter {
	return &hintResponseWriter{header: http.Header{}, statusCode: http.StatusOK}
}

func (w *hintResponseWriter) Header() http.Header { return w.header }

func (w *hintResponseWriter) WriteHeader(statusCode int) {
	if statusCode == http.StatusEarlyHints {
		captured := http.Header{}
		for name, values := range w.header {
			captured[name] = append([]string(nil), values...)
		}
		w.hints = append(w.hints, captured)
		return
	}
	w.statusCode = statusCode
}

func (w *hintResponseWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

// upstreamScript drives one simulated upstream exchange.
type upstreamScript struct {
	// hints are the raw 103 responses "received" from upstream, including any
	// attacker-controlled headers.
	hints []http.Header
	// finalStatus / finalHeader / finalBody describe the terminal response.
	// When finalStatus is 103 the transport returns a 103 as the terminal
	// RoundTrip result.
	finalStatus int
	finalHeader http.Header
	finalBody   string
}

// scriptTransport delivers scripted 1xx responses through the request's
// ClientTrace, exactly the hook net/http uses for real interim responses.
type scriptTransport struct {
	script upstreamScript
}

func (t *scriptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.Got1xxResponse != nil {
		for _, hint := range t.script.hints {
			if err := trace.Got1xxResponse(http.StatusEarlyHints, textprotoHeader(hint)); err != nil {
				return nil, err
			}
		}
	}
	status := t.script.finalStatus
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     t.script.finalHeader.Clone(),
		Body:       io.NopCloser(strings.NewReader(t.script.finalBody)),
		Request:    req,
	}, nil
}

func textprotoHeader(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for name, values := range h {
		out[name] = append([]string(nil), values...)
	}
	return out
}

// poisonedHint builds a 103 response that tries every documented injection:
// allow-listed and foreign Link targets, Set-Cookie, Content-Length and a
// custom header.
func poisonedHint() http.Header {
	hint := http.Header{}
	hint.Add("Link", `<https://cdn.example.com/app.js>; rel=preload; as=script`)
	hint.Add("Link", `<https://evil.example.org/steal.js>; rel=preload`)
	hint.Add("Link", `<https://api.example.com>; rel=preconnect`)
	hint.Set("Set-Cookie", "session=hijack")
	hint.Set("Content-Length", "999")
	hint.Set("X-Evil", "owned")
	return hint
}

func newTestHandler(cache Cache, transport http.RoundTripper) *Handler {
	allowed := map[string]bool{"cdn.example.com": true, "api.example.com": true}
	return NewHandler(cache, allowed, 0).WithTransport(transport)
}

func newProxyRequest(major, minor int) *http.Request {
	req, err := http.NewRequest(http.MethodGet, "https://example.test/index.html", nil)
	if err != nil {
		panic(err)
	}
	req.ProtoMajor = major
	req.ProtoMinor = minor
	return req
}

func TestForwardsOnlySafeHintsAndStripsOtherHeaders(t *testing.T) {
	cache := newMemoryCache()
	transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{poisonedHint()},
		finalHeader: http.Header{"Content-Type": []string{"text/plain"}, "Set-Cookie": []string{"final=ok"}},
		finalBody:   "final-body",
	}}
	handler := newTestHandler(cache, transport)
	writer := newHintResponseWriter()

	handler.ServeHTTP(writer, newProxyRequest(1, 1))

	if writer.statusCode != http.StatusOK || writer.body.String() != "final-body" {
		t.Fatalf("unexpected final response: %d %q", writer.statusCode, writer.body.String())
	}
	if len(writer.hints) != 1 {
		t.Fatalf("expected exactly 1 forwarded hint, got %d", len(writer.hints))
	}
	hint := writer.hints[0]
	if len(hint) != 1 {
		t.Fatalf("hint must contain only Link, got: %v", hint)
	}
	links := hint.Values("Link")
	wantLink := `<https://cdn.example.com/app.js>; rel=preload; as=script, <https://api.example.com>; rel=preconnect`
	if len(links) != 1 || links[0] != wantLink {
		t.Fatalf("unexpected Link header: %v", links)
	}
	for _, forbidden := range []string{"Set-Cookie", "Content-Length", "X-Evil"} {
		if hint.Get(forbidden) != "" {
			t.Fatalf("%s must be stripped from hints: %v", forbidden, hint)
		}
	}
	if writer.Header().Get("Link") != "" || writer.Header().Get("X-Evil") != "" {
		t.Fatalf("hint headers must not leak into the final response: %v", writer.Header())
	}

	cached := cache.snapshot()
	if len(cached) != 1 || cache.stores != 1 {
		t.Fatalf("only the final response must be cached, got: stores=%d entries=%v", cache.stores, cached)
	}
	for _, entry := range cached {
		if entry.StatusCode != http.StatusOK || string(entry.Body) != "final-body" {
			t.Fatalf("cached entry must be the final response: %+v", entry)
		}
		if entry.Header.Get("X-Evil") != "" || entry.Header.Get("Link") != "" {
			t.Fatalf("hint headers must not influence cached metadata: %+v", entry.Header)
		}
		if entry.Header.Get("Set-Cookie") != "final=ok" {
			t.Fatalf("cached metadata must come from the final response only: %+v", entry.Header)
		}
	}
}

func TestAtMostThreeHintsPerRequest(t *testing.T) {
	cache := newMemoryCache()
	transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{poisonedHint(), poisonedHint(), poisonedHint(), poisonedHint(), poisonedHint()},
		finalHeader: http.Header{"Content-Type": []string{"text/plain"}},
		finalBody:   "final-body",
	}}
	handler := newTestHandler(cache, transport)

	firstWriter := newHintResponseWriter()
	handler.ServeHTTP(firstWriter, newProxyRequest(1, 1))
	if len(firstWriter.hints) != 3 {
		t.Fatalf("expected at most 3 hints, got %d", len(firstWriter.hints))
	}
	for i, hint := range firstWriter.hints {
		if hint.Get("Set-Cookie") != "" || hint.Get("Content-Length") != "" {
			t.Fatalf("hint %d contains stripped headers: %v", i, hint)
		}
	}

	// Second request: cache hit. The transport is never exercised and no hints
	// may be emitted, proving hints cannot ride along a cached response.
	secondWriter := newHintResponseWriter()
	handler.ServeHTTP(secondWriter, newProxyRequest(1, 1))
	if len(secondWriter.hints) != 0 {
		t.Fatalf("cache hit must not emit any hints, got %d", len(secondWriter.hints))
	}
	if secondWriter.statusCode != http.StatusOK || secondWriter.body.String() != "final-body" {
		t.Fatalf("cache hit must serve the stored final response: %d %q", secondWriter.statusCode, secondWriter.body.String())
	}
	if cache.stores != 1 {
		t.Fatalf("expected exactly one cache store, got %d", cache.stores)
	}
}

func TestHTTP10ClientReceivesNoHints(t *testing.T) {
	cache := newMemoryCache()
	transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{poisonedHint(), poisonedHint()},
		finalHeader: http.Header{"Set-Cookie": []string{"final=ok"}},
		finalBody:   "final-body",
	}}
	handler := newTestHandler(cache, transport)

	writer := newHintResponseWriter()
	handler.ServeHTTP(writer, newProxyRequest(1, 0))

	if len(writer.hints) != 0 {
		t.Fatalf("HTTP/1.0 client must not receive any 103, got: %v", writer.hints)
	}
	if writer.statusCode != http.StatusOK || writer.body.String() != "final-body" {
		t.Fatalf("final response must still be delivered: %d %q", writer.statusCode, writer.body.String())
	}
	if writer.Header().Get("Set-Cookie") != "final=ok" {
		t.Fatalf("final response headers must be preserved: %v", writer.Header())
	}

	// HTTP/0.9 is rejected the same way, while HTTP/2 is allowed.
	oldWriter := newHintResponseWriter()
	handler.ServeHTTP(oldWriter, newProxyRequest(0, 9))
	if len(oldWriter.hints) != 0 {
		t.Fatalf("HTTP/0.9 client must not receive any 103, got: %v", oldWriter.hints)
	}

	h2Transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{poisonedHint()},
		finalHeader: http.Header{},
		finalBody:   "h2-body",
	}}
	h2Handler := newTestHandler(newMemoryCache(), h2Transport)
	h2Writer := newHintResponseWriter()
	h2Handler.ServeHTTP(h2Writer, newProxyRequest(2, 0))
	if len(h2Writer.hints) != 1 {
		t.Fatalf("HTTP/2 client should receive the hint, got %d", len(h2Writer.hints))
	}
}

func TestTerminal103IsRejectedAndNeverCached(t *testing.T) {
	cache := newMemoryCache()
	transport := &scriptTransport{script: upstreamScript{
		finalStatus: http.StatusEarlyHints,
		finalHeader: poisonedHint(),
		finalBody:   "",
	}}
	handler := newTestHandler(cache, transport)
	writer := newHintResponseWriter()

	handler.ServeHTTP(writer, newProxyRequest(1, 1))

	if writer.statusCode != http.StatusBadGateway {
		t.Fatalf("terminal 103 must be rejected with 502, got %d", writer.statusCode)
	}
	if !strings.Contains(strings.ToLower(writer.body.String()), "bad gateway") {
		t.Fatalf("expected bad gateway body, got %q", writer.body.String())
	}
	if len(writer.hints) != 0 {
		t.Fatalf("terminal 103 must not be forwarded as an interim hint, got: %v", writer.hints)
	}
	if cache.stores != 0 || len(cache.snapshot()) != 0 {
		t.Fatalf("103 must never be written to the cache: stores=%d entries=%v", cache.stores, cache.snapshot())
	}
}

func TestSetCookieAndContentLengthNeverReachHints(t *testing.T) {
	cache := newMemoryCache()
	hint := http.Header{}
	hint.Set("Link", `<https://cdn.example.com/a.js>; rel=preload`)
	hint.Set("Set-Cookie", "evil=1")
	hint.Add("Set-Cookie", "evil=2")
	hint.Set("Content-Length", "1234")
	transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{hint},
		finalHeader: http.Header{},
		finalBody:   "ok",
	}}
	handler := newTestHandler(cache, transport)
	writer := newHintResponseWriter()

	handler.ServeHTTP(writer, newProxyRequest(1, 1))

	if len(writer.hints) != 1 {
		t.Fatalf("expected one hint, got %d", len(writer.hints))
	}
	got := writer.hints[0]
	if got.Get("Set-Cookie") != "" {
		t.Fatalf("Set-Cookie smuggled into hint: %v", got)
	}
	if got.Get("Content-Length") != "" {
		t.Fatalf("Content-Length smuggled into hint: %v", got)
	}
	if len(got.Values("Link")) != 1 || got.Values("Link")[0] != `<https://cdn.example.com/a.js>; rel=preload` {
		t.Fatalf("only the safe Link should remain: %v", got)
	}
}

func TestCrossHostAndInsecurePreloadAreDropped(t *testing.T) {
	cache := newMemoryCache()
	hint := http.Header{}
	hint.Add("Link", `<https://evil.example.org/steal.js>; rel=preload`)
	hint.Add("Link", `<http://cdn.example.com/insecure.js>; rel=preload`)
	hint.Add("Link", `<https://cdn.example.com.evil.org/impersonation.js>; rel=preload`)
	hint.Add("Link", `<https://cdn.example.com/safe.js>; rel=preload; as=script`)
	hint.Add("Link", `<https://api.example.com>; rel=preconnect`)
	transport := &scriptTransport{script: upstreamScript{
		hints:       []http.Header{hint},
		finalHeader: http.Header{},
		finalBody:   "ok",
	}}
	handler := newTestHandler(cache, transport)
	writer := newHintResponseWriter()

	handler.ServeHTTP(writer, newProxyRequest(1, 1))

	if len(writer.hints) != 1 {
		t.Fatalf("expected one hint, got %d", len(writer.hints))
	}
	links := writer.hints[0].Values("Link")
	want := `<https://cdn.example.com/safe.js>; rel=preload; as=script, <https://api.example.com>; rel=preconnect`
	if len(links) != 1 || links[0] != want {
		t.Fatalf("unexpected Link header: %v", links)
	}
}
