package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrTooManyRedirects is returned when MaxRedirects would be exceeded.
var ErrTooManyRedirects = errors.New("netsec: too many redirects")

type purposeContextKey struct{}

// Plan is the result of planning one redirect hop. Request is a sanitized
// clone: URL userinfo is removed and all credential-bearing headers are first
// stripped, then only the caller-configured credential for To is applied.
type Plan struct {
	From       Origin
	To         Origin
	Purpose    Purpose
	Request    *http.Request
	Authorized bool
	Identity   string
}

// RedirectPlan and RedirectPlanner are descriptive aliases for Plan and
// Planner.
type RedirectPlan = Plan
type RedirectPlanner = Planner

// NewRequest builds a request and immediately binds it to purpose and to the
// caller-configured credentials for the initial URL.
func (p *Planner) NewRequest(
	ctx context.Context,
	method string,
	rawURL string,
	purpose Purpose,
) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return p.Prepare(req, purpose)
}

// Prepare returns a sanitized clone of req with its purpose bound and only the
// caller-configured credential for the initial destination applied.
//
// Any Authorization, Proxy-Authorization, Cookie, or other credential-bearing
// header already on req is removed first. This makes caller configuration the
// sole source of authorization.
func (p *Planner) Prepare(req *http.Request, purpose Purpose) (*http.Request, error) {
	return p.prepare(req, purpose)
}

func (p *Planner) prepare(req *http.Request, purpose Purpose) (*http.Request, error) {
	if req == nil {
		return nil, fmt.Errorf("netsec: nil request")
	}
	if purpose == "" {
		purpose = p.defaultPurpose
	}

	clone := req.Clone(req.Context())
	target, err := sanitizeURL(clone.URL)
	if err != nil {
		return nil, err
	}
	clone.URL = target
	clone.Host = ""
	clone.Header = clone.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	p.stripManagedHeaders(clone.Header)
	clone = clone.WithContext(withPurpose(clone.Context(), purpose))

	if credential, ok := p.CredentialFor(originOfURL(target), purpose); ok {
		applyHeaders(clone.Header, credential.Headers)
	}
	return clone, nil
}

// BuildRedirectPlan computes the credential decision for req without mutating
// it. via is the same redirect history that http.Client passes to
// CheckRedirect.
func (p *Planner) BuildRedirectPlan(req *http.Request, via []*http.Request) (*Plan, error) {
	return p.buildRedirectPlan(req, via, true)
}

// PlanRedirect is an alias for BuildRedirectPlan.
func (p *Planner) PlanRedirect(req *http.Request, via []*http.Request) (*Plan, error) {
	return p.BuildRedirectPlan(req, via)
}

func (p *Planner) buildRedirectPlan(req *http.Request, via []*http.Request, clone bool) (*Plan, error) {
	if req == nil {
		return nil, fmt.Errorf("netsec: nil redirect request")
	}
	if len(via) > p.maxRedirects {
		return nil, fmt.Errorf("%w: stopped after %d", ErrTooManyRedirects, p.maxRedirects)
	}

	purpose := p.purposeFor(req, via)
	target := req
	if clone {
		target = req.Clone(req.Context())
	}

	from := Origin{}
	if len(via) > 0 {
		previous := via[len(via)-1]
		if previous == nil {
			return nil, fmt.Errorf("netsec: nil request in redirect history")
		}
		origin, err := OriginFromURL(previous.URL)
		if err != nil {
			return nil, fmt.Errorf("netsec: previous redirect origin: %w", err)
		}
		from = origin
	}

	targetURL, err := sanitizeURL(target.URL)
	if err != nil {
		return nil, err
	}
	target.URL = targetURL
	target.Host = ""
	target.Header = target.Header.Clone()
	if target.Header == nil {
		target.Header = make(http.Header)
	}
	p.stripManagedHeaders(target.Header)

	origin, err := OriginFromURL(targetURL)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		From:    from,
		To:      origin,
		Purpose: purpose,
		Request: target,
	}

	if credential, ok := p.CredentialFor(origin, purpose); ok {
		applyHeaders(target.Header, credential.Headers)
		plan.Authorized = true
		plan.Identity = credential.Identity
	}
	target = target.WithContext(withPurpose(target.Context(), purpose))
	plan.Request = target
	return plan, nil
}

// CheckRedirect implements http.Client.CheckRedirect. It rebuilds
// authorization from caller configuration for every destination and never
// copies credentials from the source response or the previous request.
func (p *Planner) CheckRedirect(req *http.Request, via []*http.Request) error {
	plan, err := p.buildRedirectPlan(req, via, false)
	if err != nil {
		return err
	}
	// Replace the entire request value so the purpose bound in the plan also
	// propagates to any later hop.
	*req = *plan.Request

	if p.onRedirect != nil {
		p.onRedirect(RedirectEvent{
			From:       plan.From,
			To:         plan.To,
			Purpose:    plan.Purpose,
			Authorized: plan.Authorized,
			Identity:   plan.Identity,
		})
	}
	return nil
}

// Do prepares req for purpose and executes it with a client that uses this
// planner for every redirect.
func (p *Planner) Do(req *http.Request, purpose Purpose) (*http.Response, error) {
	prepared, err := p.Prepare(req, purpose)
	if err != nil {
		return nil, err
	}
	return p.Client().Do(prepared)
}

// Client returns a new client whose transport and redirect handling are owned
// by p. Its cookie jar is nil so ambient cookies cannot bypass the
// caller-configured policy. Requests built with NewRequest or Prepare carry an
// explicit purpose; requests without one use Config.DefaultPurpose.
func (p *Planner) Client() *http.Client {
	return &http.Client{
		Transport:     p.Transport(http.DefaultTransport),
		CheckRedirect: p.CheckRedirect,
		Jar:           nil,
	}
}

// Transport wraps base so every outbound request is sanitized and authorized
// according to p. A nil base uses http.DefaultTransport.
func (p *Planner) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &policyTransport{base: base, planner: p}
}

// WrapClient clones base, wraps its transport with p's per-request
// sanitization, installs p as its redirect policy, and disables its cookie jar.
// If base already has a CheckRedirect function, it runs first and this planner
// has the final say over the request that is sent.
func (p *Planner) WrapClient(base *http.Client) *http.Client {
	if base == nil {
		return p.Client()
	}
	clone := *base
	previous := clone.CheckRedirect
	baseTransport := clone.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	clone.Transport = p.Transport(baseTransport)
	clone.Jar = nil
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if previous != nil {
			if err := previous(req, via); err != nil {
				return err
			}
		}
		return p.CheckRedirect(req, via)
	}
	return &clone
}

type policyTransport struct {
	base    http.RoundTripper
	planner *Planner
}

func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	purpose := t.planner.purposeFor(req, nil)
	prepared, err := t.planner.prepare(req, purpose)
	if err != nil {
		return nil, err
	}
	return t.base.RoundTrip(prepared)
}

func (t *policyTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (p *Planner) purposeFor(req *http.Request, via []*http.Request) Purpose {
	if purpose, ok := purposeFromContext(req.Context()); ok {
		return purpose
	}
	for i := len(via) - 1; i >= 0; i-- {
		if via[i] == nil {
			continue
		}
		if purpose, ok := purposeFromContext(via[i].Context()); ok {
			return purpose
		}
	}
	return p.defaultPurpose
}

func withPurpose(ctx context.Context, purpose Purpose) context.Context {
	return context.WithValue(ctx, purposeContextKey{}, purpose)
}

func purposeFromContext(ctx context.Context) (Purpose, bool) {
	if ctx == nil {
		return "", false
	}
	purpose, ok := ctx.Value(purposeContextKey{}).(Purpose)
	return purpose, ok
}

func sanitizeURL(u *url.URL) (*url.URL, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: nil URL", ErrInvalidOrigin)
	}
	if _, err := OriginFromURL(u); err != nil {
		return nil, err
	}
	clone := *u
	clone.Scheme = strings.ToLower(clone.Scheme)
	// net/http automatically turns URL userinfo into a Basic Authorization
	// header when none is present. Drop it before the request leaves.
	clone.User = nil
	return &clone, nil
}

func originOfURL(u *url.URL) Origin {
	origin, _ := OriginFromURL(u)
	return origin
}
