package netsec

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
)

// DefaultMaxHops bounds the number of redirects a single download may
// follow, preventing redirect loops.
const DefaultMaxHops = 10

// RedirectError reports a redirect that the planner refuses to follow.
type RedirectError struct {
	Reason string
}

func (e *RedirectError) Error() string {
	return "redirect rejected: " + e.Reason
}

// Hop records what was actually sent on one leg of a download: the
// normalized origin, the purpose and the exact authentication headers
// attached. It provides an auditable trail for verifying credential
// isolation across every hop.
type Hop struct {
	Origin        Origin
	Purpose       Purpose
	URL           string
	Status        int
	Location      string
	AuthHeaders   http.Header
	Authenticated bool
}

// Plan is the recorded redirect plan executed for one download.
type Plan struct {
	Purpose Purpose
	Hops    []Hop
}

// Planner performs downloads while isolating credentials across
// redirects. Credentials are bound to a precise normalized origin and
// request purpose and are re-selected for every hop from the caller's
// CredentialStore. The planner never copies credentials from one
// request to the next and never derives them from server-controlled
// data such as Location userinfo or WWW-Authenticate.
type Planner struct {
	Store     *CredentialStore
	Transport http.RoundTripper
	MaxHops   int
}

// NewPlanner creates a Planner using the given caller-owned store.
func NewPlanner(store *CredentialStore) *Planner {
	return &Planner{Store: store, MaxHops: DefaultMaxHops}
}

func (p *Planner) transport() http.RoundTripper {
	if p.Transport != nil {
		return p.Transport
	}
	return http.DefaultTransport
}

func (p *Planner) maxHops() int {
	if p.MaxHops > 0 {
		return p.MaxHops
	}
	return DefaultMaxHops
}

// Response pairs the final HTTP response with the plan that led to it.
type Response struct {
	*http.Response
	Plan *Plan
}

// Do executes a request for the given purpose, following redirects and
// re-selecting credentials at each hop from the configured store. The
// initial request also starts with no implicit identity: only an
// explicit store rule for its origin can authenticate it.
func (p *Planner) Do(method, rawURL string, purpose Purpose, body io.Reader) (*Response, error) {
	origin, err := CanonicalOrigin(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	// Userinfo in the caller's URL is also untrusted input; the store
	// is the only place credentials may come from.
	req.URL.User = nil

	plan := &Plan{Purpose: purpose}
	client := &http.Client{Transport: p.transport(), CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	for hopCount := 0; ; hopCount++ {
		if hopCount > p.maxHops() {
			return nil, &RedirectError{Reason: "too many redirects"}
		}

		origin, err = originFromURL(req.URL)
		if err != nil {
			return nil, err
		}

		// Strip every identity-bearing header before re-selection so a
		// previous hop's token can never leak into this hop.
		stripAuthHeaders(req.Header)
		req.URL.User = nil

		hop := Hop{
			Origin:  origin,
			Purpose: purpose,
			URL:     req.URL.String(),
		}
		if cred, ok := p.Store.CredentialFor(origin, purpose); ok {
			applyCredential(req.Header, cred)
			hop.Authenticated = true
		}
		hop.AuthHeaders = captureAuthHeaders(req.Header)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		hop.Status = resp.StatusCode

		if !isRedirect(resp.StatusCode) {
			hop.Location = ""
			plan.Hops = append(plan.Hops, hop)
			return &Response{Response: resp, Plan: plan}, nil
		}

		location := resp.Header.Get("Location")
		hop.Location = location
		plan.Hops = append(plan.Hops, hop)
		resp.Body.Close()

		next, err := redirectRequest(req, resp)
		if err != nil {
			return nil, err
		}
		req = next
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	}
	return false
}

func redirectRequest(prev *http.Request, resp *http.Response) (*http.Request, error) {
	location := resp.Header.Get("Location")
	if location == "" {
		return nil, &RedirectError{Reason: "redirect response missing Location header"}
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, &RedirectError{Reason: "invalid Location header: " + err.Error()}
	}
	target := prev.URL.ResolveReference(ref)

	// Validate the next hop's origin up front; userinfo is dropped.
	if _, err := originFromURL(target); err != nil {
		return nil, err
	}
	target.User = nil

	method := prev.Method
	var body io.Reader
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
		method = http.MethodGet
	case http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		method = prev.Method
		if prev.GetBody != nil {
			body, err = prev.GetBody()
			if err != nil {
				return nil, err
			}
		}
	}

	next, err := http.NewRequest(method, target.String(), body)
	if err != nil {
		return nil, err
	}
	next.Header = prev.Header.Clone()
	return next, nil
}

var authHeaderNames = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
}

func stripAuthHeaders(headers http.Header) {
	for _, name := range authHeaderNames {
		headers.Del(name)
	}
}

func captureAuthHeaders(headers http.Header) http.Header {
	captured := make(http.Header)
	for _, name := range authHeaderNames {
		if values := headers.Values(name); len(values) > 0 {
			captured[name] = append([]string(nil), values...)
		}
	}
	return captured
}

func applyCredential(headers http.Header, cred Credential) {
	for name, values := range cred.headers {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
}

func basicAuthHeader(username, password string) string {
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + token
}
