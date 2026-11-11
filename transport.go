package sessionjar

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// ctxKey is an unexported context key for pin state carried along a redirect
// chain. http.Client preserves the request context across redirects.
type ctxKey struct{}

// chainPins holds the per-site pin state of one logical request and all of
// its redirect hops.
type chainPins struct {
	sites map[string]pinState
}

type pinState struct {
	site string
	gen  uint64
	id   uint64
}

// GenerationTransport is an http.RoundTripper that makes cookie handling
// generation-safe.
//
// Every request is pinned to the generation of each touched site that is
// current when the request (or a redirect hop) starts, and every Set-Cookie
// header is written through the jar with that generation. A Refresh during an
// in-flight request therefore cannot let a late, old-generation response
// pollute the new cookie store: once the old generation loses its last pin
// its store is deleted and the late write is dropped.
//
// Typical wiring, which also keeps Jar usable as a standard http.CookieJar:
//
//	jar := sessionjar.New()
//	client := &http.Client{
//	    Jar: jar,
//	    Transport: &sessionjar.GenerationTransport{
//	        Base: http.DefaultTransport,
//	        Jar:  jar,
//	    },
//	}
//
// The transport overwrites the request's Cookie header with pinned
// generation cookies and strips Set-Cookie from responses, so the client's
// own jar bookkeeping never double-stores or reads a different generation.
type GenerationTransport struct {
	Base http.RoundTripper
	Jar  *Jar

	nextPin atomic.Uint64
}

// RoundTrip implements http.RoundTripper.
func (t *GenerationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	chain, _ := req.Context().Value(ctxKey{}).(*chainPins)
	root := chain == nil
	if root {
		chain = &chainPins{sites: map[string]pinState{}}
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, chain))
	}

	site := registrableDomain(canonicalHost(req.URL), t.Jar.suffixes)
	st, ok := chain.sites[site]
	if !ok {
		id := t.nextPin.Add(1)
		st = pinState{site: site, gen: t.Jar.pinForSite(site, id), id: id}
		chain.sites[site] = st
	}

	// Attach only cookies from the pinned generation of this site.
	if header := t.Jar.cookieHeader(req.URL, st.gen); header != "" {
		req.Header.Set("Cookie", header)
	} else {
		req.Header.Del("Cookie")
	}

	resp, err := base.RoundTrip(req)
	if err != nil {
		// An error aborts the whole redirect chain: release every pin.
		t.release(chain)
		return nil, err
	}

	// Persist Set-Cookie against the pinned generation, then hide it from
	// http.Client so its own jar logic does not double-store the batch.
	t.Jar.StoreResponse(resp.Request.URL, st.gen, resp)
	resp.Header.Del("Set-Cookie")

	if root {
		t.release(chain)
	}
	return resp, nil
}

func (t *GenerationTransport) release(chain *chainPins) {
	for _, p := range chain.sites {
		t.Jar.unpinSite(p.site, p.gen, p.id)
	}
}

// cookieHeader renders the Cookie header for u at an explicit generation.
// An empty string means no matching cookies.
func (j *Jar) cookieHeader(u *url.URL, gen uint64) string {
	cookies := j.CookiesFor(u, gen)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, len(cookies))
	for i, c := range cookies {
		parts[i] = c.Name + "=" + c.Value
	}
	return strings.Join(parts, "; ")
}

func (j *Jar) pinForSite(site string, pinID uint64) uint64 {
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
	p.pins[pinID]++
	return s.gen
}

func (j *Jar) unpinSite(site string, gen, pinID uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.sites[site]
	if s == nil {
		return
	}
	p := s.parts[gen]
	if p == nil {
		return
	}
	if n := p.pins[pinID] - 1; n > 0 {
		p.pins[pinID] = n
		return
	}
	delete(p.pins, pinID)
	if gen != s.gen && len(p.pins) == 0 {
		delete(s.parts, gen)
	}
}
