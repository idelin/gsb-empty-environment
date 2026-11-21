package redirectplan

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultMaxRedirects = 10

// Options configures a redirect-safe download client.
type Options struct {
	// Credentials is caller-owned configuration.  It is consulted separately
	// for every hop using the exact normalized (origin, purpose) identity.
	Credentials CredentialSource

	// Redirects optionally restricts allowed origin transitions.  Nil permits
	// HTTP(S) redirects up to MaxRedirects; credentials still require an
	// explicit credential entry for each target.
	Redirects RedirectPolicy

	// Transport performs individual round trips.  It must not follow
	// redirects itself.
	Transport http.RoundTripper

	// HTTPClient can supply Transport.  Its redirect policy is not used.
	HTTPClient *http.Client

	// MaxRedirects defaults to 10.
	MaxRedirects int

	// SensitiveHeaders names additional request headers that must never be
	// copied from one hop to the next.  Authorization, Proxy-Authorization,
	// Cookie, and Cookie2 are always stripped.
	SensitiveHeaders []string

	// ForwardHeaders names additional non-credential request headers that may
	// cross an origin boundary.  Unknown headers are not forwarded to a
	// different origin even if they are not one of the sensitive headers.
	ForwardHeaders []string
}

// Client executes a request one hop at a time.  It never delegates redirect
// handling to net/http, whose header copier can retain Authorization across
// same-host scheme or port changes.
type Client struct {
	planner          *Planner
	transport        http.RoundTripper
	maxRedirects     int
	sensitiveHeaders []string
	crossOrigin      map[string]struct{}
}

var defaultSensitiveHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Cookie2",
}

var defaultCrossOriginHeaders = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Cache-Control",
	"Content-Encoding",
	"Content-Type",
	"If-Match",
	"If-Modified-Since",
	"If-None-Match",
	"If-Range",
	"If-Unmodified-Since",
	"Pragma",
	"Range",
	"User-Agent",
}

// New returns a redirect-safe client.
func New(opts Options) *Client {
	transport := opts.Transport
	if transport == nil && opts.HTTPClient != nil {
		transport = opts.HTTPClient.Transport
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	sensitive := canonicalHeaderNames(defaultSensitiveHeaders, opts.SensitiveHeaders)
	crossOrigin := make(map[string]struct{})
	for _, name := range canonicalHeaderNames(defaultCrossOriginHeaders, opts.ForwardHeaders) {
		crossOrigin[name] = struct{}{}
	}
	return &Client{
		planner:          NewPlanner(opts.Credentials, opts.Redirects),
		transport:        transport,
		maxRedirects:     maxRedirects,
		sensitiveHeaders: sensitive,
		crossOrigin:      crossOrigin,
	}
}

// NewClient is an alias for New.
func NewClient(opts Options) *Client {
	return New(opts)
}

// Get performs a GET request with the supplied request purpose.
func (c *Client) Get(ctx context.Context, rawURL string, purpose Purpose) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req, purpose)
}

// Do executes req and follows redirects with per-hop credential selection.
// The request's URL user information, Authorization, Proxy-Authorization,
// Cookie, and Cookie2 headers are ignored; authentication must come from
// Options.Credentials.
func (c *Client) Do(req *http.Request, purpose Purpose) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("redirectplan: nil request")
	}
	if req.URL == nil {
		return nil, fmt.Errorf("redirectplan: request has no URL")
	}
	if c == nil {
		return nil, fmt.Errorf("redirectplan: nil client")
	}

	ctx := req.Context()
	hop, err := c.planner.Begin(req.URL.String(), purpose)
	if err != nil {
		return nil, err
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	baseHeader := req.Header.Clone()
	if baseHeader == nil {
		baseHeader = make(http.Header)
	}
	c.stripSensitive(baseHeader)
	initialOrigin := hop.Origin

	body := req.Body
	getBody := req.GetBody
	contentLength := req.ContentLength
	hasBody := body != nil && body != http.NoBody
	if !hasBody {
		body = nil
		contentLength = 0
	}

	for redirects := 0; ; redirects++ {
		out, err := c.newRequest(ctx, method, hop, baseHeader, body, contentLength, hop.Origin == initialOrigin)
		if err != nil {
			return nil, err
		}
		resp, err := c.transport.RoundTrip(out)
		if err != nil {
			return nil, err
		}

		location := strings.TrimSpace(resp.Header.Get("Location"))
		if !isRedirect(resp.StatusCode) || location == "" {
			return resp, nil
		}
		if redirects >= c.maxRedirects {
			closeResponse(resp)
			return nil, fmt.Errorf("%w (limit %d)", ErrTooManyRedirects, c.maxRedirects)
		}

		next, err := c.planner.Next(hop, location)
		if err != nil {
			closeResponse(resp)
			return nil, err
		}

		status := resp.StatusCode
		closeResponse(resp)

		switch status {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
			if method != http.MethodGet && method != http.MethodHead {
				method = http.MethodGet
				body = nil
				getBody = nil
				hasBody = false
				contentLength = 0
				delete(baseHeader, "Content-Length")
				delete(baseHeader, "Content-Type")
				delete(baseHeader, "Content-Encoding")
			}
		case http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			if hasBody {
				if getBody == nil {
					return nil, ErrBodyNotReplayable
				}
				body, err = getBody()
				if err != nil {
					return nil, fmt.Errorf("redirectplan: replay body: %w", err)
				}
			}
		default:
			return nil, fmt.Errorf("%w: status %d", ErrInvalidRedirect, status)
		}

		hop = next
	}
}

func (c *Client) newRequest(
	ctx context.Context,
	method string,
	hop Hop,
	baseHeader http.Header,
	body io.ReadCloser,
	contentLength int64,
	sameOrigin bool,
) (*http.Request, error) {
	if hop.URL == nil {
		return nil, fmt.Errorf("%w: hop has no URL", ErrInvalidRedirect)
	}
	target := *hop.URL
	// Defense in depth: a URL user-info segment must never become Basic auth.
	target.User = nil

	header := baseHeader.Clone()
	if header == nil {
		header = make(http.Header)
	}
	c.stripSensitive(header)
	if !sameOrigin {
		for name := range header {
			if _, ok := c.crossOrigin[http.CanonicalHeaderKey(name)]; !ok {
				header.Del(name)
			}
		}
	}
	hop.Credential.Apply(header)

	req := &http.Request{
		Method:        method,
		URL:           &target,
		Header:        header,
		Body:          body,
		ContentLength: contentLength,
	}
	if body == nil {
		req.Body = nil
		req.ContentLength = 0
	}
	return req.WithContext(ctx), nil
}

func (c *Client) stripSensitive(header http.Header) {
	for _, name := range c.sensitiveHeaders {
		header.Del(name)
	}
}

func canonicalHeaderNames(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, group := range groups {
		for _, name := range group {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			name = http.CanonicalHeaderKey(name)
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func closeResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

// TrimSensitive may be used by a host that wants to reuse the package's
// header sanitation rules before handing a request to another transport.
func TrimSensitive(header http.Header, extra ...string) {
	for _, name := range defaultSensitiveHeaders {
		header.Del(name)
	}
	for _, name := range extra {
		header.Del(strings.TrimSpace(name))
	}
}
