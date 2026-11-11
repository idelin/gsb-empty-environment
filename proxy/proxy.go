package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

// hopByHop headers are removed before storing or forwarding responses.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func hopByHop(name string) bool {
	return hopByHopHeaders[http.CanonicalHeaderKey(name)]
}

// ReverseProxy is a caching reverse proxy. Responses are stored in a bounded
// shared cache keyed by request URI with one entry per Vary variant.
type ReverseProxy struct {
	Transport http.RoundTripper
	Origin    *url.URL
	Cache     *Cache
	Clock     Clock

	// backgroundRoot drives stale-while-revalidate revalidations so they
	// outlive the client request that triggered them.
	backgroundRoot context.Context
	backgroundWG   sync.WaitGroup
}

// New builds a ReverseProxy targeting origin.
func New(origin *url.URL, cache *Cache, clock Clock) *ReverseProxy {
	transport := http.DefaultTransport
	return &ReverseProxy{
		Transport:      transport,
		Origin:         origin,
		Cache:          cache,
		Clock:          clock,
		backgroundRoot: context.Background(),
	}
}

// WaitBackground blocks until every background revalidation has finished.
// Tests use it to make asynchronous work deterministic.
func (p *ReverseProxy) WaitBackground() {
	p.backgroundWG.Wait()
}

// ServeHTTP implements http.Handler.
func (p *ReverseProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		p.passthrough(rw, req)
		return
	}

	requestDirectives := parseCacheControl(req.Header.Get("Cache-Control"))
	if requestDirectives.has("no-store") {
		p.passthrough(rw, req)
		return
	}

	entry := p.Cache.Lookup(req)
	if entry == nil {
		if requestDirectives.has("only-if-cached") {
			rw.Header().Set("Cache-Control", "no-store")
			rw.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		p.fetchAndStore(rw, req)
		return
	}

	now := p.Clock.Now()
	if entry.isFresh(now, requestDirectives) {
		p.writeCached(rw, entry, req)
		return
	}

	// Expired: revalidate. Stale variants keep serving inside the
	// stale-while-revalidate window while revalidation runs in the background.
	mustRevalidate := requestDirectives.has("no-cache") ||
		requestDirectives.has("max-age") ||
		entry.mustRevalidate || entry.noCache
	if !mustRevalidate && entry.serveStale(now, requestDirectives) {
		p.writeCached(rw, entry, req)
		p.startBackgroundRevalidation(entry, req)
		return
	}

	p.revalidateSynchronously(rw, req, entry, requestDirectives)
}

func (p *ReverseProxy) writeCached(rw http.ResponseWriter, entry *cachedEntry, req *http.Request) {
	header := rw.Header()
	for key, values := range entry.header {
		if hopByHop(key) {
			continue
		}
		header[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	header.Set("Age", p.ageHeader(entry))
	rw.WriteHeader(entry.status)
	if req.Method != http.MethodHead {
		_, _ = io.Copy(rw, bytes.NewReader(entry.body))
	}
}

func (p *ReverseProxy) ageHeader(entry *cachedEntry) string {
	age := p.Clock.Now().Sub(entry.date).Seconds()
	if age < 0 {
		age = 0
	}
	return strconv.Itoa(int(age))
}

func (p *ReverseProxy) passthrough(rw http.ResponseWriter, req *http.Request) {
	resp, err := p.forward(req, req.Context(), nil)
	if err != nil {
		http.Error(rw, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyResponse(rw, resp, req)
}

func copyResponse(rw http.ResponseWriter, resp *http.Response, req *http.Request) {
	for key, values := range resp.Header {
		if hopByHop(key) {
			continue
		}
		rw.Header()[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	rw.WriteHeader(resp.StatusCode)
	if req.Method != http.MethodHead {
		_, _ = io.Copy(rw, resp.Body)
	}
}

// forward performs one origin request. When entry is non-nil, conditional
// request headers are attached so the origin may answer 304.
func (p *ReverseProxy) forward(req *http.Request, ctx context.Context, entry *cachedEntry) (*http.Response, error) {
	out := req.Clone(ctx)
	out.RequestURI = ""
	out.URL = p.Origin.ResolveReference(&url.URL{
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	})
	out.Host = p.Origin.Host
	if entry != nil {
		if etag := entry.header.Get("ETag"); etag != "" {
			out.Header.Set("If-None-Match", etag)
		}
		if lastModified := entry.header.Get("Last-Modified"); lastModified != "" {
			out.Header.Set("If-Modified-Since", lastModified)
		}
	}
	transport := p.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(out)
}

func readBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// fetchAndStore handles a cache miss by fetching the origin and, when
// cacheable, storing the response before serving it.
func (p *ReverseProxy) fetchAndStore(rw http.ResponseWriter, req *http.Request) {
	resp, err := p.forward(req, req.Context(), nil)
	if err != nil {
		http.Error(rw, "upstream error", http.StatusBadGateway)
		return
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		http.Error(rw, "upstream read error", http.StatusBadGateway)
		return
	}

	if entry, _ := p.Cache.BuildEntry(req, resp.StatusCode, resp.Header, body); entry != nil {
		p.Cache.Store(entry)
		p.writeCached(rw, entry, req)
		return
	}

	for key, values := range resp.Header {
		if hopByHop(key) {
			continue
		}
		rw.Header()[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	rw.WriteHeader(resp.StatusCode)
	if req.Method != http.MethodHead {
		_, _ = rw.Write(body)
	}
}

// startBackgroundRevalidation coalesces with any in-flight revalidation and
// otherwise runs one without being tied to the client's lifecycle.
func (p *ReverseProxy) startBackgroundRevalidation(entry *cachedEntry, req *http.Request) {
	reval, leader := p.Cache.beginRevalidation(entry)
	if !leader {
		return
	}
	backgroundReq := req.Clone(p.backgroundRoot)
	p.backgroundWG.Add(1)
	go func() {
		defer p.backgroundWG.Done()
		err := p.runRevalidation(backgroundReq, entry)
		p.Cache.finishRevalidation(entry, reval, err)
	}()
}

// revalidateSynchronously coalesces concurrent revalidations. The leader
// performs the origin call tied to the client context; waiters block on the
// shared revalidation and re-read the index once it completes.
func (p *ReverseProxy) revalidateSynchronously(rw http.ResponseWriter, req *http.Request, entry *cachedEntry, requestDirectives cacheDirectives) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		reval, leader := p.Cache.beginRevalidation(entry)
		if leader {
			lastErr = p.runRevalidation(req, entry)
			p.Cache.finishRevalidation(entry, reval, lastErr)
			if lastErr != nil {
				if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
					p.Cache.abortRevalidation(entry, reval)
				}
				p.serveStaleOrError(rw, req, entry, requestDirectives, lastErr)
				return
			}
			break
		}
		select {
		case <-reval.ready:
			// A canceled leader aborts the shared revalidation; retry once as
			// a new leader instead of hanging or failing the waiting client.
			if errors.Is(reval.err, errRevalidationAborted) {
				continue
			}
		case <-req.Context().Done():
			http.Error(rw, "revalidation canceled", http.StatusRequestTimeout)
			return
		}
		break
	}

	if refreshed := p.Cache.Lookup(req); refreshed != nil {
		if refreshed.isFresh(p.Clock.Now(), requestDirectives) {
			p.writeCached(rw, refreshed, req)
			return
		}
		if refreshed.serveStale(p.Clock.Now(), requestDirectives) {
			p.writeCached(rw, refreshed, req)
			return
		}
	}
	http.Error(rw, "revalidation failed", http.StatusBadGateway)
}

func (p *ReverseProxy) serveStaleOrError(rw http.ResponseWriter, req *http.Request, entry *cachedEntry, requestDirectives cacheDirectives, cause error) {
	now := p.Clock.Now()
	if cause != nil && requestDirectives.has("no-cache") == false {
		if staleIfError, ok := requestDirectives.seconds("stale-if-error"); ok {
			if now.Before(entry.freshUntil.Add(staleIfError)) {
				p.writeCached(rw, entry, req)
				return
			}
		}
		if entry.serveStale(now, requestDirectives) {
			p.writeCached(rw, entry, req)
			return
		}
	}
	http.Error(rw, "upstream revalidation error", http.StatusBadGateway)
}

// runRevalidation performs one conditional origin request and applies the
// result to the cache. A 200 replaces the entry; a 304 is merged atomically.
func (p *ReverseProxy) runRevalidation(req *http.Request, entry *cachedEntry) error {
	resp, err := p.forward(req, req.Context(), entry)
	if err != nil {
		return err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return readErr
	}

	switch resp.StatusCode {
	case http.StatusNotModified:
		refreshed := p.Cache.RevalidateWith304(entry, req, resp.Header)
		if refreshed == nil {
			p.invalidateEntry(entry)
		}
		return nil
	case http.StatusOK:
		newEntry, _ := p.Cache.BuildEntry(req, resp.StatusCode, resp.Header, body)
		if newEntry == nil {
			p.invalidateEntry(entry)
			return nil
		}
		p.Cache.Store(newEntry)
		return nil
	default:
		// Other statuses are not stored; keep the stale entry untouched so it
		// can still be served within its allowance window.
		return nil
	}
}

func (p *ReverseProxy) invalidateEntry(entry *cachedEntry) {
	p.Cache.mu.Lock()
	defer p.Cache.mu.Unlock()
	if resource := p.Cache.resources[entry.requestPath]; resource != nil {
		if old, ok := resource.variants[entry.variantKey]; ok && old.lruElem != nil {
			p.Cache.lru.Remove(old.lruElem)
		}
		delete(resource.variants, entry.variantKey)
		resource.order = removeString(resource.order, entry.variantKey)
		if len(resource.variants) == 0 {
			delete(p.Cache.resources, entry.requestPath)
		}
	}
}
