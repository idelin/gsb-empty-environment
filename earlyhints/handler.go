package earlyhints

import (
	"io"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
)

// CachedResponse is the only data a Cache ever stores. It is produced
// exclusively from final (non-1xx) upstream responses. Early hints never
// produce or mutate a CachedResponse.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Cache is the storage boundary of the reverse proxy. Because this interface
// exposes no update/metadata operation, 103 Early Hints are structurally
// incapable of modifying an existing cache entry: the hint path never calls
// any Cache method.
type Cache interface {
	// Lookup returns a cached final response for key, if one exists.
	Lookup(key string) (CachedResponse, bool)
	// Store saves a final response. Only complete, non-informational
	// responses reach this method.
	Store(key string, entry CachedResponse)
}

// CachePolicy decides whether a final response is cacheable and under which
// key. A zero key with cacheable=false means "do not store".
type CachePolicy func(req *http.Request, resp *http.Response, body []byte) (key string, cacheable bool)

// Handler is a reverse proxy front end that relays 103 Early Hints under the
// package policy while keeping them fully isolated from the cache.
//
// The zero value is not usable; construct with NewHandler.
type Handler struct {
	transport    http.RoundTripper
	cache        Cache
	policy       CachePolicy
	allowedHosts map[string]bool
	maxHints     int
}

// NewHandler builds a Handler. cache may be nil, in which case responses are
// never stored. allowedHosts is the preload/preconnect host allow list
// (lower-cased host names). A non-positive maxHints selects DefaultMaxHints.
func NewHandler(cache Cache, allowedHosts map[string]bool, maxHints int) *Handler {
	if maxHints <= 0 {
		maxHints = DefaultMaxHints
	}
	return &Handler{
		transport:    http.DefaultTransport,
		cache:        cache,
		policy:       DefaultCachePolicy,
		allowedHosts: allowedHosts,
		maxHints:     maxHints,
	}
}

// WithTransport overrides the upstream transport. Passing nil resets to
// http.DefaultTransport.
func (h *Handler) WithTransport(transport http.RoundTripper) *Handler {
	if transport == nil {
		transport = http.DefaultTransport
	}
	h.transport = transport
	return h
}

// WithCachePolicy overrides the cacheability decision for final responses.
func (h *Handler) WithCachePolicy(policy CachePolicy) *Handler {
	if policy != nil {
		h.policy = policy
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	key := h.policyKey(req)
	if h.cache != nil && key != "" {
		if cached, ok := h.cache.Lookup(key); ok {
			writeCachedResponse(w, cached)
			return
		}
	}

	outReq := req.Clone(req.Context())
	outReq.RequestURI = ""
	processor := NewProcessor(h.allowedHosts, h.maxHints)
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(statusCode int, header textproto.MIMEHeader) error {
			hint := processor.HandleInformational(statusCode, http.Header(header), req.ProtoMajor, req.ProtoMinor)
			if hint == nil {
				return nil
			}
			// An error means the downstream connection cannot accept interim
			// responses; dropping the hint is safe because the final response
			// is still in flight.
			_ = writeEarlyHint(w, hint)
			return nil
		},
	}
	outReq = outReq.WithContext(httptrace.WithClientTrace(outReq.Context(), trace))

	resp, err := h.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// A 1xx status at this point means the upstream treated an interim status
	// as the terminal response. It must never be cached or relayed as a final
	// response.
	if resp.StatusCode >= 100 && resp.StatusCode < 200 {
		io.Copy(io.Discard, resp.Body)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	// The cache is touched only here, for the final response. The hint
	// processing path above never reaches the Cache interface.
	if h.cache != nil {
		if cacheKey, cacheable := h.policy(req, resp, body); cacheable && cacheKey != "" {
			h.cache.Store(cacheKey, CachedResponse{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       append([]byte(nil), body...),
			})
		}
	}

	writeFinalResponse(w, resp, body)
}

// policyKey returns the lookup key for a request without buffering a
// response. It mirrors the key produced by DefaultCachePolicy.
func (h *Handler) policyKey(req *http.Request) string {
	if key, ok := defaultKey(req); ok {
		return key
	}
	return ""
}

// DefaultCachePolicy caches successful GET responses keyed by method and
// absolute URL. All other requests and all non-2xx responses bypass the
// cache.
func DefaultCachePolicy(req *http.Request, resp *http.Response, _ []byte) (string, bool) {
	key, ok := defaultKey(req)
	if !ok {
		return "", false
	}
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	return key, true
}

func defaultKey(req *http.Request) (string, bool) {
	if req.Method != http.MethodGet {
		return "", false
	}
	if req.URL == nil || req.URL.Scheme == "" || req.URL.Host == "" {
		return "", false
	}
	return req.Method + " " + req.URL.Scheme + "://" + req.URL.Host + req.URL.RequestURI(), true
}

func writeFinalResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	header := w.Header()
	for name, values := range resp.Header {
		copied := append([]string(nil), values...)
		header[http.CanonicalHeaderKey(name)] = copied
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func writeCachedResponse(w http.ResponseWriter, cached CachedResponse) {
	header := w.Header()
	for name, values := range cached.Header {
		copied := append([]string(nil), values...)
		header[http.CanonicalHeaderKey(name)] = copied
	}
	w.WriteHeader(cached.StatusCode)
	_, _ = w.Write(cached.Body)
}

// writeEarlyHint emits one 103 interim response. The header map is reset
// afterwards so hint headers can never bleed into the final response, which
// is the same discipline net/http/httputil.ReverseProxy applies.
func writeEarlyHint(w http.ResponseWriter, hint http.Header) error {
	header := w.Header()
	for name, values := range hint {
		copied := append([]string(nil), values...)
		header[http.CanonicalHeaderKey(name)] = copied
	}
	w.WriteHeader(http.StatusEarlyHints)
	clear(header)
	return nil
}
