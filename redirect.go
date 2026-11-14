package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// DefaultMaxRedirects matches net/http's default limit.
const DefaultMaxRedirects = 10

// CheckRedirect is suitable for http.Client.CheckRedirect. It validates the
// initial request (which is present in via) and every redirect target,
// re-resolving each host on every hop.
func (r *Resolver) CheckRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= DefaultMaxRedirects {
		return fmt.Errorf("netsec: stopped after %d redirects", DefaultMaxRedirects)
	}
	if request == nil || request.URL == nil {
		return fmt.Errorf("%w: nil redirect target", ErrInvalidAddress)
	}
	if r == nil {
		r = defaultResolver
	}

	ctx := request.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	target := request.URL
	if !target.IsAbs() {
		if len(via) == 0 || via[len(via)-1] == nil || via[len(via)-1].URL == nil {
			return fmt.Errorf("%w: relative redirect without a base URL", ErrInvalidAddress)
		}
		target = via[len(via)-1].URL.ResolveReference(target)
	}

	// via[0] is the initial request and may have been added by a client that
	// did not use this policy for its first hop. Revalidate the whole chain.
	seen := make(map[string]struct{}, len(via)+1)
	for _, hop := range via {
		if hop == nil || hop.URL == nil {
			continue
		}
		key := hop.URL.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if err := r.checkURL(ctx, hop.URL); err != nil {
			return fmt.Errorf("netsec: redirect hop %s: %w", hop.URL, err)
		}
	}

	if err := r.checkURL(ctx, target); err != nil {
		return fmt.Errorf("netsec: redirect hop %s: %w", target, err)
	}
	return nil
}

// CheckRedirect validates an http.Client redirect chain with the default
// resolver.
func CheckRedirect(request *http.Request, via []*http.Request) error {
	return defaultResolver.CheckRedirect(request, via)
}

// ValidateRedirect is an alias for CheckRedirect.
func (r *Resolver) ValidateRedirect(request *http.Request, via []*http.Request) error {
	return r.CheckRedirect(request, via)
}

// ValidateRedirect validates a redirect chain with the default resolver.
func ValidateRedirect(request *http.Request, via []*http.Request) error {
	return defaultResolver.CheckRedirect(request, via)
}

// NewHTTPClient returns an HTTP client that validates the initial URL
// (through the dialer), every redirect hop, and the exact IP that is dialed.
func (r *Resolver) NewHTTPClient() *http.Client {
	if r == nil {
		r = defaultResolver
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = r.DialContext

	return &http.Client{
		Transport:     r.WrapRoundTripper(transport),
		CheckRedirect: r.CheckRedirect,
	}
}

// HTTPClient is an alias for NewHTTPClient.
func (r *Resolver) HTTPClient() *http.Client {
	return r.NewHTTPClient()
}

// NewHTTPClient returns an HTTP client backed by the default resolver.
func NewHTTPClient() *http.Client {
	return defaultResolver.NewHTTPClient()
}

type policyRoundTripper struct {
	resolver *Resolver
	base     http.RoundTripper
}

func (t *policyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: nil request URL", ErrInvalidAddress)
	}
	if err := t.resolver.checkURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

// WrapRoundTripper validates the destination URL before every request is
// handed to base. It is useful when an application supplies its own
// transport. A nil base uses http.DefaultTransport.
func (r *Resolver) WrapRoundTripper(base http.RoundTripper) http.RoundTripper {
	if r == nil {
		r = defaultResolver
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &policyRoundTripper{resolver: r, base: base}
}

// DialContext resolves address, rejects any non-public answer, and dials one
// of the already-validated public addresses. Dialing the resolved address
// directly avoids a second DNS lookup that could return a different answer.
func (r *Resolver) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if r == nil {
		r = defaultResolver
	}
	if ctx == nil {
		ctx = context.Background()
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}

	results, err := r.ResolveAndCheck(ctx, host)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var dialErrors []error
	for _, result := range results {
		if !result.Public || !result.DialAddr.IsValid() {
			continue
		}
		target := net.JoinHostPort(result.DialAddr.String(), port)
		conn, err := dialer.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		dialErrors = append(dialErrors, fmt.Errorf("%s: %w", target, err))
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("%w: no public address for %q", ErrBlocked, host)
	}
	return nil, fmt.Errorf("netsec: dial %q: %w", host, errors.Join(dialErrors...))
}
