package navigator

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// cookieEntry is one stored cookie together with the bookkeeping needed to
// decide whether it may be attached to a request.
type cookieEntry struct {
	cookie    http.Cookie
	host      string // canonical lower-case host that set the cookie
	hostOnly  bool
	path      string
	secure    bool
	sameSite  http.SameSite
	createdAt time.Time
}

// Jar is a SameSite-aware cookie store. It deliberately does not implement
// net/http.CookieJar: that interface cannot express same-site information,
// so cookies are selected through SelectCookies which receives the full
// navigation context.
type Jar struct {
	entries map[string]map[string]*cookieEntry // host -> path/name key -> entry
	now     func() time.Time
}

// NewJar creates an empty cookie jar.
func NewJar() *Jar {
	return &Jar{entries: map[string]map[string]*cookieEntry{}, now: time.Now}
}

// SetCookies stores the cookies from a Set-Cookie style list for the given
// request URL, applying domain matching, secure context and expiry rules.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return
	}
	for _, c := range cookies {
		j.setCookie(u, host, c)
	}
}

func (j *Jar) setCookie(u *url.URL, host string, c *http.Cookie) {
	if c.Name == "" {
		return
	}
	if c.MaxAge < 0 {
		j.delete(host, c)
		return
	}
	domain := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	hostOnly := domain == ""
	if domain == "" {
		domain = host
	}
	if !domainMatch(host, domain) {
		// RFC 6265: reject cookies whose Domain attribute does not match
		// the origin that set them.
		return
	}
	if c.Secure && !isPotentiallyTrustworthy(u) {
		return
	}
	path := c.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		path = defaultPath(u)
	}
	sameSite := c.SameSite
	if sameSite == 0 || sameSite == http.SameSiteDefaultMode {
		// Lax-by-default: cookies without an explicit SameSite attribute
		// behave as SameSite=Lax.
		sameSite = http.SameSiteLaxMode
	}
	entry := &cookieEntry{
		cookie:    *c,
		host:      domain,
		hostOnly:  hostOnly,
		path:      path,
		secure:    c.Secure,
		sameSite:  sameSite,
		createdAt: j.now(),
	}
	bucket, ok := j.entries[domain]
	if !ok {
		bucket = map[string]*cookieEntry{}
		j.entries[domain] = bucket
	}
	bucket[path+"|"+c.Name] = entry
}

func (j *Jar) delete(host string, c *http.Cookie) {
	for domain, bucket := range j.entries {
		if !domainMatch(host, domain) {
			continue
		}
		for key, entry := range bucket {
			if entry.cookie.Name == c.Name {
				delete(bucket, key)
			}
		}
	}
}

// CookieRequest describes everything the jar needs to know about an
// outbound request to select the right cookies.
type CookieRequest struct {
	URL       *url.URL
	Method    string
	CrossSite bool
	// TopLevel is true for top-level navigations; embedded subresource
	// requests pass false.
	TopLevel bool
	// LaxPostWindowEnd is the two-minute "Lax+POST" mitigation window for
	// recently created Lax cookies.
	LaxPostWindow time.Duration
}

// SelectCookies returns the cookies eligible for the described request,
// ordered per RFC 6265 (longer paths first, then creation time).
func (j *Jar) SelectCookies(req CookieRequest) []*http.Cookie {
	host := strings.ToLower(req.URL.Hostname())
	now := j.now()
	var selected []*cookieEntry
	for domain, bucket := range j.entries {
		if !domainMatch(host, domain) {
			continue
		}
		for _, e := range bucket {
			if !e.alive(now) {
				continue
			}
			if e.hostOnly && host != domain {
				continue
			}
			if !pathMatch(req.URL.EscapedPath(), e.path) {
				continue
			}
			if e.secure && !isPotentiallyTrustworthy(req.URL) {
				continue
			}
			if !e.allowedBySameSite(req, now) {
				continue
			}
			selected = append(selected, e)
		}
	}
	sort.SliceStable(selected, func(i, k int) bool {
		if len(selected[i].path) != len(selected[k].path) {
			return len(selected[i].path) > len(selected[k].path)
		}
		return selected[i].createdAt.Before(selected[k].createdAt)
	})
	out := make([]*http.Cookie, 0, len(selected))
	for _, e := range selected {
		c := e.cookie
		out = append(out, &c)
	}
	return out
}

func (e *cookieEntry) alive(now time.Time) bool {
	if !e.cookie.Expires.IsZero() && !now.Before(e.cookie.Expires) {
		return false
	}
	return true
}

// allowedBySameSite implements the RFC 6265bis SameSite table, including the
// Lax-by-default and Lax+POST interventions.
func (e *cookieEntry) allowedBySameSite(req CookieRequest, now time.Time) bool {
	if !req.CrossSite {
		return true
	}
	switch e.sameSite {
	case http.SameSiteStrictMode:
		return false
	case http.SameSiteNoneMode:
		return true
	case http.SameSiteLaxMode:
		if !req.TopLevel {
			return false
		}
		if isSafeMethod(req.Method) {
			return true
		}
		// Lax+POST intervention: a Lax cookie minted within the
		// mitigation window may ride an unsafe cross-site top-level
		// navigation; older cookies must not.
		return req.LaxPostWindow > 0 && now.Sub(e.createdAt) < req.LaxPostWindow
	case http.SameSiteDefaultMode:
		// Unset cookies were normalized to Lax at storage time.
		return req.TopLevel && isSafeMethod(req.Method)
	default:
		return false
	}
}

// domainMatch reports whether host matches a cookie domain per RFC 6265
// section 5.1.3.
func domainMatch(host, domain string) bool {
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

// pathMatch implements RFC 6265 section 5.1.4.
func pathMatch(requestPath, cookiePath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	if strings.HasSuffix(cookiePath, "/") {
		return true
	}
	return requestPath[len(cookiePath)] == '/'
}

// defaultPath computes the default cookie path (RFC 6265 5.1.4).
func defaultPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" || p[0] != '/' {
		return "/"
	}
	if i := strings.LastIndex(p[1:], "/"); i >= 0 {
		return p[:i+1]
	}
	return "/"
}

// isPotentiallyTrustworthy mirrors the browser "secure context" rule needed
// for the Secure attribute: https origins and loopback hosts qualify even
// over plain http.
func isPotentiallyTrustworthy(u *url.URL) bool {
	if strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "wss") {
		return true
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasPrefix(host, "127.") || host == "::1"
}
