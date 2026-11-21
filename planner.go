package redirectplan

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	// ErrRedirectDenied reports a target rejected by the caller's policy.
	ErrRedirectDenied = errors.New("redirectplan: redirect denied by policy")
	// ErrInvalidRedirect reports a redirect without a usable HTTP(S) target.
	ErrInvalidRedirect = errors.New("redirectplan: invalid redirect")
	// ErrTooManyRedirects reports that the redirect limit was exceeded.
	ErrTooManyRedirects = errors.New("redirectplan: too many redirects")
	// ErrBodyNotReplayable reports a 307/308 redirect whose body cannot be
	// replayed because the request did not provide GetBody.
	ErrBodyNotReplayable = errors.New("redirectplan: request body cannot be replayed")
)

// RedirectPolicy is caller configuration that may further restrict redirect
// transitions.  It is consulted for every hop, including same-origin hops.
// Returning false aborts the redirect before any credential is attached to
// the target request.
type RedirectPolicy interface {
	AllowRedirect(from Origin, to Identity) bool
}

// RedirectPolicyFunc adapts a function to RedirectPolicy.
type RedirectPolicyFunc func(from Origin, to Identity) bool

// AllowRedirect implements RedirectPolicy.
func (f RedirectPolicyFunc) AllowRedirect(from Origin, to Identity) bool {
	if f == nil {
		return true
	}
	return f(from, to)
}

// RedirectKey identifies one directed origin transition.
type RedirectKey struct {
	From Origin
	To   Origin
}

// RedirectRules is an allow-list of origin transitions.  The request purpose
// is still part of credential selection; this type only authorizes the
// network transition.
type RedirectRules map[RedirectKey]bool

// AllowRedirect implements RedirectPolicy.
func (r RedirectRules) AllowRedirect(from Origin, to Identity) bool {
	return r[RedirectKey{From: from, To: to.Origin}]
}

// Hop is one planned HTTP request.  Credential is the exact credential found
// for this hop; it is zero when the caller configured none.
type Hop struct {
	URL        *url.URL
	Origin     Origin
	Identity   Identity
	Credential Credential
}

// Authorized reports whether this hop has caller-configured credentials.
func (h Hop) Authorized() bool {
	return !h.Credential.IsZero()
}

// Planner computes each hop independently.  It never receives redirect
// response headers other than the Location string, so a response cannot
// supply credentials.
type Planner struct {
	Credentials CredentialSource
	Redirects   RedirectPolicy
}

// NewPlanner returns a per-hop planner backed by the supplied caller
// configuration.
func NewPlanner(credentials CredentialSource, redirects RedirectPolicy) *Planner {
	return &Planner{Credentials: credentials, Redirects: redirects}
}

// Begin plans the first request.
func (p *Planner) Begin(rawURL string, purpose Purpose) (Hop, error) {
	target, origin, err := ParseTarget(rawURL)
	if err != nil {
		return Hop{}, err
	}
	return p.hop(target, origin, purpose), nil
}

// Next plans the hop after prev following location.  location is resolved
// against prev.URL and then normalized again, so relative, absolute,
// protocol-relative, and user-info-bearing targets all select credentials by
// their final canonical origin and the original request purpose.
func (p *Planner) Next(prev Hop, location string) (Hop, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return Hop{}, fmt.Errorf("%w: empty Location", ErrInvalidRedirect)
	}
	if prev.URL == nil || prev.Identity.Origin.IsZero() {
		return Hop{}, fmt.Errorf("%w: missing previous hop", ErrInvalidRedirect)
	}

	ref, err := url.Parse(location)
	if err != nil {
		return Hop{}, fmt.Errorf("%w: %w", ErrInvalidRedirect, err)
	}
	resolved := prev.URL.ResolveReference(ref)
	target, origin, err := normalizeURL(resolved)
	if err != nil {
		return Hop{}, fmt.Errorf("%w: %w", ErrInvalidRedirect, err)
	}

	identity := Identity{Origin: origin, Purpose: prev.Identity.Purpose}
	if p.Redirects != nil && !p.Redirects.AllowRedirect(prev.Origin, identity) {
		return Hop{}, fmt.Errorf("%w: %s -> %s", ErrRedirectDenied, prev.Origin, origin)
	}
	return p.hop(target, origin, prev.Identity.Purpose), nil
}

// Plan is a sequence of hops produced without performing network requests.
type Plan struct {
	Hops []Hop
}

// BuildPlan plans a start URL and a known sequence of Location values.
func (p *Planner) BuildPlan(rawURL string, purpose Purpose, locations []string) (*Plan, error) {
	hop, err := p.Begin(rawURL, purpose)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Hops: []Hop{hop}}
	for _, location := range locations {
		hop, err = p.Next(hop, location)
		if err != nil {
			return nil, err
		}
		plan.Hops = append(plan.Hops, hop)
	}
	return plan, nil
}

func (p *Planner) hop(target *url.URL, origin Origin, purpose Purpose) Hop {
	identity := Identity{Origin: origin, Purpose: purpose}
	hop := Hop{URL: target, Origin: origin, Identity: identity}
	if p.Credentials != nil {
		if credential, ok := p.Credentials.Lookup(identity); ok {
			hop.Credential = credential
		}
	}
	return hop
}
