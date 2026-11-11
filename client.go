package robotstxt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// errTooManyRedirects mirrors the 5-redirect cap from RFC 9309 section 2.6.
var errTooManyRedirects = errors.New("robotstxt: stopped after 5 redirects")

// MaxBodySize is the 500 KiB robots.txt size cap from RFC 9309 section 2.6:
// received content beyond this limit is discarded before parsing.
const MaxBodySize = 500 * 1024

// DefaultCacheTTL is the cache lifetime applied when Client.TTL is not set.
const DefaultCacheTTL = 24 * time.Hour

// defaultUserAgent is sent when fetching robots.txt and used for group
// selection when no explicit crawler identifier is given.
const defaultUserAgent = "robotstxt-client"

// Client fetches, caches and evaluates robots.txt files. A zero-value Client
// is ready to use. A Client must not be copied after first use.
type Client struct {
	// UserAgent is the crawler identifier used for group selection and, when
	// empty, the HTTP User-Agent header sent while fetching robots.txt.
	UserAgent string

	// HTTPClient performs the fetches. nil uses http.DefaultClient.
	HTTPClient *http.Client

	// doer overrides HTTPClient/http.DefaultClient with a custom transport
	// (mainly for testing). When set, requests are handed to it directly.
	doer doer

	// TTL is how long a fetched result (including deny-by-error results) is
	// cached. Non-positive values use DefaultCacheTTL.
	TTL time.Duration

	// now may be overridden in tests; it defaults to time.Now.
	now func() time.Time

	mu       sync.Mutex
	cache    map[string]cacheEntry
	inflight map[string]*fetchCall
}

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type cacheEntry struct {
	rules     *robots
	allowAll  bool // 4xx: no usable robots.txt, everything is allowed
	denyAll   bool // 5xx/transport error: everything is denied
	expiresAt time.Time
}

type fetchCall struct {
	done  chan struct{}
	entry cacheEntry
}

// Allowed reports whether requesting targetURL is permitted. targetURL may be
// a *url.URL or a string. A non-HTTP(S) URL is always allowed.
//
// Fetch failures and 5xx responses deny all paths (and are cached); 4xx
// responses allow all paths. A non-nil error accompanies a denied decision
// caused by a transport-level failure.
func (c *Client) Allowed(ctx context.Context, targetURL any) (bool, error) {
	u, err := toURL(targetURL)
	if err != nil {
		return false, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return true, nil
	}

	entry, err := c.lookup(ctx, originKey(u))
	if err != nil {
		return false, err
	}
	if entry.denyAll {
		return false, nil
	}
	if entry.allowAll {
		return true, nil
	}

	rawPath := u.EscapedPath()
	if rawPath == "" {
		rawPath = "/"
	}
	decodedPath := u.Path
	if decodedPath == "" {
		decodedPath = "/"
	}

	return entry.rules.allowed(c.identifier(), rawPath, decodedPath), nil
}

// identifier returns the crawler's product token (leading label only).
func (c *Client) identifier() string {
	ua := c.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	token := ua
	if i := strings.IndexAny(ua, " \t/"); i >= 0 {
		token = ua[:i]
	}
	return token
}

// lookup returns a cached, non-expired entry or fetches and caches a fresh one,
// coalescing concurrent requests for the same origin.
func (c *Client) lookup(ctx context.Context, key string) (cacheEntry, error) {
	now := c.clock()

	c.mu.Lock()
	if c.cache == nil {
		c.cache = make(map[string]cacheEntry)
		c.inflight = make(map[string]*fetchCall)
	}
	if entry, ok := c.cache[key]; ok && now.Before(entry.expiresAt) {
		c.mu.Unlock()
		return entry, nil
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.entry, nil
		case <-ctx.Done():
			return cacheEntry{}, ctx.Err()
		}
	}
	call := &fetchCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	entry, err := c.fetch(ctx, key)

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		entry.expiresAt = now.Add(c.ttl())
		c.cache[key] = entry
	}
	call.entry = entry
	close(call.done)
	c.mu.Unlock()

	return entry, err
}

// fetch retrieves the robots.txt for key and maps the outcome to a decision:
// 2xx parses the (truncated) body; 4xx allows everything; 5xx and transport
// errors deny everything.
func (c *Client) fetch(ctx context.Context, key string) (cacheEntry, error) {
	robotsURL := key + "/robots.txt"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return denyEntry(), err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.transport().Do(req)
	if err != nil {
		return denyEntry(), err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodySize))
		if err != nil {
			return denyEntry(), err
		}
		return cacheEntry{rules: parse(body)}, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// RFC 9309: treat any 4xx as "no robots.txt", i.e. allow all.
		drainBody(resp.Body)
		return cacheEntry{allowAll: true}, nil
	default:
		// 5xx, leftover 3xx and any other unusable status deny all paths.
		drainBody(resp.Body)
		return denyEntry(), nil
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{CheckRedirect: defaultRedirectPolicy}
}

// defaultRedirectPolicy caps redirect chains at 5 per RFC 9309 section 2.6;
// exceeding the cap is treated as an unusable robots.txt (deny all).
func defaultRedirectPolicy(_ *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errTooManyRedirects
	}
	return nil
}

func (c *Client) transport() doer {
	if c.doer != nil {
		return c.doer
	}
	return c.httpClient()
}

func (c *Client) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultCacheTTL
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func denyEntry() cacheEntry {
	return cacheEntry{denyAll: true}
}

func drainBody(body io.Reader) {
	_, _ = io.Copy(io.Discard, body)
}

// originKey builds the scheme://host[:port] key under which a robots.txt is
// cached, since rules apply per origin.
func originKey(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func toURL(target any) (*url.URL, error) {
	switch v := target.(type) {
	case *url.URL:
		if v == nil {
			return nil, errors.New("robotstxt: nil url")
		}
		return v, nil
	case string:
		u, err := url.Parse(v)
		if err != nil {
			return nil, err
		}
		return u, nil
	default:
		return nil, errors.New("robotstxt: target must be *url.URL or string")
	}
}
