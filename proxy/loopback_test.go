package proxy

import (
	"net/http"
	"net/url"
)

// loopbackTransport dispatches requests to an in-process origin handler
// without opening sockets, which keeps tests deterministic and works in
// environments where listening is restricted. The full net/http request and
// response machinery (round tripping, headers, body streaming, contexts) is
// exercised exactly as with a real origin.
type loopbackTransport struct {
	origin http.Handler
}

func (t *loopbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := newResponseRecorder()
	done := make(chan struct{})
	var result *http.Response
	go func() {
		originReq := req.Clone(req.Context())
		originReq.RequestURI = ""
		t.origin.ServeHTTP(recorder, originReq)
		result = recorder.result(req)
		close(done)
	}()
	select {
	case <-done:
		return result, nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func newLoopbackProxy(tb interface{ Helper() }, origin http.Handler, capacity int) (*ReverseProxy, *FakeClock, *originHits) {
	tb.Helper()
	hits := &originHits{}
	wrapped := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		hits.incr()
		origin.ServeHTTP(rw, req)
	})
	base := timeBase()
	clock := NewFakeClock(base)
	cache := NewCache(capacity, clock)
	originURL, _ := url.Parse("http://origin.example")
	proxy := New(originURL, cache, clock)
	proxy.Transport = &loopbackTransport{origin: wrapped}
	return proxy, clock, hits
}
