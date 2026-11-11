package sessionjar

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Clock returns the current time. It is an option so tests can drive expiry
// deterministically without sleeping.
type Clock func() time.Time

// cookieEntry is one stored cookie.
type cookieEntry struct {
	Name     string
	Value    string
	Domain   string // canonical lower-case; no leading dot
	HostOnly bool
	Path     string
	Secure   bool
	Expires  time.Time // zero means session cookie
	Created  time.Time
}

// storeKey identifies a cookie within a partition. Host-only and domain
// cookies with the same name/path are distinct entries.
type storeKey struct {
	Name     string
	Domain   string
	Path     string
	HostOnly bool
}

// partition is the cookie store for one site at one generation.
type partition struct {
	site string
	gen  uint64
	data map[storeKey]*cookieEntry
	// pins counts in-flight requests attached to this partition, keyed by the
	// pin id allocated by GenerationTransport.
	pins map[uint64]int
}

// siteState holds every generation of one registrable site.
type siteState struct {
	gen   uint64
	parts map[uint64]*partition
}

// Jar is a partitioned cookie jar. It implements http.CookieJar.
//
// Partitions are keyed by (registrable site, generation). A site groups all
// hosts under the same registrable domain; unrelated sites never share
// cookies.
type Jar struct {
	mu       sync.Mutex
	sites    map[string]*siteState
	clock    Clock
	suffixes map[string]bool
	dropped  atomic.Int64
	rejected atomic.Int64
}

// Option configures a Jar.
type Option func(*Jar)

// WithClock overrides the time source used for expiry checks.
func WithClock(c Clock) Option {
	return func(j *Jar) { j.clock = c }
}

// WithPublicSuffixes registers additional public suffixes (without leading
// dot, lower-case). The built-in list covers common multi-part suffixes such
// as co.uk; unknown suffixes are treated as single labels.
func WithPublicSuffixes(suffixes ...string) Option {
	return func(j *Jar) {
		for _, s := range suffixes {
			j.suffixes[strings.ToLower(strings.TrimPrefix(s, "."))] = true
		}
	}
}

// New creates an empty Jar. Generation numbering starts at 1.
func New(opts ...Option) *Jar {
	j := &Jar{
		sites:    make(map[string]*siteState),
		clock:    time.Now,
		suffixes: map[string]bool{},
	}
	for _, s := range defaultPublicSuffixes {
		j.suffixes[s] = true
	}
	for _, o := range opts {
		o(j)
	}
	return j
}

// Generation reports the current generation of the site that owns u.
// Returns 0 if the site has never been used.
func (j *Jar) Generation(u *url.URL) uint64 {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	j.mu.Lock()
	defer j.mu.Unlock()
	if s := j.sites[site]; s != nil {
		return s.gen
	}
	return 0
}

// Refresh starts a new generation for the site that owns u, discarding its
// current cookie store. Requests already pinned to the previous generation
// keep reading the old store until they finish; when the last pin is released
// the stale store is deleted and any later Set-Cookie targeting it is dropped.
// The new generation number is returned.
func (j *Jar) Refresh(u *url.URL) uint64 {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.sites[site]
	if s == nil {
		s = &siteState{gen: 0, parts: map[uint64]*partition{}}
		j.sites[site] = s
	}
	s.gen++
	s.parts[s.gen] = &partition{
		site: site,
		gen:  s.gen,
		data: make(map[storeKey]*cookieEntry),
		pins: make(map[uint64]int),
	}
	// Generations with no in-flight requests can never receive another
	// write (the transport pins before round-tripping), so drop them now.
	// Pinned old generations are retained until Unpin.
	for gen, old := range s.parts {
		if gen != s.gen && len(old.pins) == 0 {
			delete(s.parts, gen)
		}
	}
	return s.gen
}

// Pin attaches the caller to the current generation of u's site, creating the
// partition on first use. The returned generation must be handed back to
// Unpin once the request (including its redirect chain) has finished.
func (j *Jar) Pin(u *url.URL, pinID uint64) uint64 {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	return j.pinForSite(site, pinID)
}

// Unpin releases a pin obtained from Pin. When the last pin of a superseded
// generation is released, its store is deleted; subsequent Set-Cookie batches
// for that generation are counted as dropped.
func (j *Jar) Unpin(u *url.URL, gen, pinID uint64) {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	j.unpinSite(site, gen, pinID)
}

// Stats reports jar counters since creation.
type Stats struct {
	// Rejected is the number of individual cookies refused while storing
	// (illegal domain, secure mismatch, malformed).
	Rejected int64
	// Dropped is the number of Set-Cookie batches discarded because they
	// targeted a generation that no longer exists (a late old-generation
	// response after Refresh).
	Dropped int64
}

// Snapshot returns the current counters.
func (j *Jar) Snapshot() Stats {
	return Stats{Rejected: j.rejected.Load(), Dropped: j.dropped.Load()}
}

// Cookies implements http.CookieJar. It returns the cookies of u's site at
// the current generation that match the request URL.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	return j.CookiesFor(u, j.Generation(u))
}

// CookiesFor returns cookies matching u at an explicit generation. The
// generation transport uses this to keep in-flight requests on their pinned
// generation. A non-existent generation yields no cookies.
func (j *Jar) CookiesFor(u *url.URL, gen uint64) []*http.Cookie {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	now := j.clock()
	j.mu.Lock()
	p := j.lookupLocked(site, gen)
	if p == nil {
		j.mu.Unlock()
		return nil
	}
	entries := make([]*cookieEntry, 0, len(p.data))
	for _, e := range p.data {
		if !e.expired(now) && e.matches(u) {
			entries = append(entries, e)
		}
	}
	j.mu.Unlock()

	sort.Slice(entries, func(a, b int) bool {
		// Longer paths first (RFC 6265 5.4), then earlier creation.
		if len(entries[a].Path) != len(entries[b].Path) {
			return len(entries[a].Path) > len(entries[b].Path)
		}
		return entries[a].Created.Before(entries[b].Created)
	})

	out := make([]*http.Cookie, len(entries))
	for i, e := range entries {
		out[i] = &http.Cookie{Name: e.Name, Value: e.Value}
	}
	return out
}

// SetCookies implements http.CookieJar. Cookies are written into the current
// generation of u's site. Through GenerationTransport, Set-Cookie headers are
// consumed by the transport and this method is not part of request handling.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.store(u, j.ensureGeneration(u), cookies)
}

// ensureGeneration returns the current generation of u's site, creating the
// gen-1 partition on first use so the standalone http.CookieJar API works
// without an explicit Pin.
func (j *Jar) ensureGeneration(u *url.URL) uint64 {
	site := registrableDomain(canonicalHost(u), j.suffixes)
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.sites[site]
	if s == nil {
		s = &siteState{gen: 1, parts: map[uint64]*partition{}}
		j.sites[site] = s
	}
	p, ok := s.parts[s.gen]
	if !ok {
		p = &partition{
			site: site,
			gen:  s.gen,
			data: make(map[storeKey]*cookieEntry),
			pins: make(map[uint64]int),
		}
		s.parts[s.gen] = p
	}
	return s.gen
}

// StoreResponse writes the cookies carried by resp into the partition of the
// generation given by gen (the request's pinned generation). If that
// generation has been superseded and fully unpinned, the whole batch is
// counted as dropped and nothing is stored.
func (j *Jar) StoreResponse(u *url.URL, gen uint64, resp *http.Response) {
	if resp == nil {
		return
	}
	raw := resp.Header["Set-Cookie"]
	cookies := resp.Cookies()
	for i, c := range cookies {
		if i < len(raw) {
			if maxAge, ok := parseMaxAge(raw[i]); ok {
				c.MaxAge = maxAge
				c.Expires = time.Time{}
			}
		}
	}
	j.store(u, gen, cookies)
}

// parseMaxAge extracts the Max-Age directive from a raw Set-Cookie header.
// net/http otherwise converts it against the real wall clock, which breaks
// jar-injected clocks.
func parseMaxAge(raw string) (int, bool) {
	parts := strings.Split(raw, ";")
	if len(parts) < 2 {
		return 0, false
	}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if len(p) >= 8 && strings.EqualFold(p[:8], "max-age=") {
			n, err := strconv.Atoi(strings.TrimSpace(p[8:]))
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

func (j *Jar) store(u *url.URL, gen uint64, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	site := registrableDomain(canonicalHost(u), j.suffixes)
	now := j.clock()
	j.mu.Lock()
	defer j.mu.Unlock()
	p := j.lookupLocked(site, gen)
	if p == nil {
		j.dropped.Add(1)
		return
	}
	if n := p.ingest(u, cookies, now, j.effectiveExpiry); n > 0 {
		j.rejected.Add(int64(n))
	}
}

func (j *Jar) lookupLocked(site string, gen uint64) *partition {
	s := j.sites[site]
	if s == nil {
		return nil
	}
	return s.parts[gen]
}

// effectiveExpiry resolves a cookie's expiry against the jar clock. Explicit
// MaxAge is always measured from the jar clock; an absolute Expires supplied
// by the caller is trusted as given.
func (j *Jar) effectiveExpiry(c *http.Cookie, now time.Time) time.Time {
	if c.MaxAge > 0 {
		return now.Add(time.Duration(c.MaxAge) * time.Second)
	}
	return c.Expires
}

func (p *partition) ingest(u *url.URL, cookies []*http.Cookie, now time.Time, expiryOf func(*http.Cookie, time.Time) time.Time) int {
	host := canonicalHost(u)
	isSecure := strings.EqualFold(u.Scheme, "https")
	rejected := 0
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			rejected++
			continue
		}
		domain := host
		hostOnly := true
		if c.Domain != "" {
			d := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
			if !domainAllowed(host, d) {
				// RFC 6265: ignore cookies whose Domain attribute does not
				// match the request host.
				rejected++
				continue
			}
			domain = d
			hostOnly = false
		}
		path := c.Path
		if path == "" || !strings.HasPrefix(path, "/") {
			path = defaultPath(u)
		}

		key := storeKey{Name: c.Name, Domain: domain, Path: path, HostOnly: hostOnly}

		// Expiry: a fixed Expires in the past, or negative MaxAge, deletes.
		if c.MaxAge < 0 || (!c.Expires.IsZero() && !c.Expires.After(now)) {
			delete(p.data, key)
			continue
		}

		expires := expiryOf(c, now)
		if !expires.IsZero() && !expires.After(now) {
			delete(p.data, key)
			continue
		}
		if c.Secure && !isSecure {
			// A Secure cookie may only be set over a secure channel.
			rejected++
			continue
		}

		p.data[key] = &cookieEntry{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   domain,
			HostOnly: hostOnly,
			Path:     path,
			Secure:   c.Secure,
			Expires:  expires,
			Created:  now,
		}
	}
	return rejected
}

func (e *cookieEntry) expired(now time.Time) bool {
	return !e.Expires.IsZero() && !e.Expires.After(now)
}

// matches implements RFC 6265 5.4 domain and path matching.
func (e *cookieEntry) matches(u *url.URL) bool {
	host := canonicalHost(u)
	if e.HostOnly {
		if host != e.Domain {
			return false
		}
	} else if !domainMatches(host, e.Domain) {
		return false
	}
	if e.Secure && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	return pathMatch(u.EscapedPath(), e.Path)
}

func pathMatch(requestPath, cookiePath string) bool {
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	if strings.HasSuffix(cookiePath, "/") {
		return true
	}
	return len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/'
}

// defaultPath implements RFC 6265 5.1.4.
func defaultPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" || p[0] != '/' {
		return "/"
	}
	if p == "/" {
		return "/"
	}
	idx := strings.LastIndex(p[1:], "/")
	if idx < 0 {
		return "/"
	}
	return p[:idx+1]
}

func canonicalHost(u *url.URL) string {
	return strings.ToLower(u.Hostname())
}

// domainAllowed reports whether host is permitted to set a Domain cookie for
// cookieDomain (RFC 6265 5.3).
func domainAllowed(host, cookieDomain string) bool {
	if host == cookieDomain {
		return true
	}
	return strings.HasSuffix(host, "."+cookieDomain)
}

// domainMatches reports whether host is in the scope of a domain cookie.
func domainMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// registrableDomain returns the top-level site key for host: the
// eTLD+1-like registrable domain. IP literals and single-label hosts are
// their own sites.
func registrableDomain(host string, suffixes map[string]bool) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return ""
	}
	if isIPLiteral(host) || !strings.Contains(host, ".") {
		return host
	}
	labels := strings.Split(host, ".")
	// Find the longest matching public suffix.
	suffixLen := 1
	for i := 1; i < len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		if suffixes[candidate] {
			suffixLen = len(labels) - i
		}
	}
	// Site = suffix plus one label, or the whole host if not enough labels.
	keep := suffixLen + 1
	if keep > len(labels) {
		keep = len(labels)
	}
	return strings.Join(labels[len(labels)-keep:], ".")
}

func isIPLiteral(host string) bool {
	// Strip brackets of an IPv6 literal.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return true
	}
	// A dotted name whose labels are all numbers is an IPv4 address.
	if !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// defaultPublicSuffixes is a small built-in list of common multi-label
// suffixes, sufficient for tests and typical crawler targets. Unknown
// suffixes default to a single label; extend via WithPublicSuffixes.
var defaultPublicSuffixes = []string{
	"co.uk", "org.uk", "gov.uk", "ac.uk",
	"co.jp", "or.jp", "ne.jp",
	"com.au", "net.au", "org.au",
	"co.nz", "com.br", "com.cn", "com.hk", "com.sg",
}
