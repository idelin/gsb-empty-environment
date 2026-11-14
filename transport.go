package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type pinnedTarget struct {
	host      string
	port      int
	addresses []string
}

type pinnedTargetKey struct{}

// RoundTrip implements http.RoundTripper. It validates and resolves the
// destination on every request, injects credentials only after validation,
// and gives the dialer a fixed set of already-validated IP addresses.
func (p *Policy) RoundTrip(req *http.Request) (*http.Response, error) {
	if p == nil || p.base == nil {
		return nil, fmt.Errorf("%w: nil policy", ErrInvalidTarget)
	}
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("%w: nil request or URL", ErrInvalidTarget)
	}
	if strings.EqualFold(req.Method, http.MethodConnect) {
		return nil, fmt.Errorf("%w: CONNECT requests are not allowed", ErrProxyBypass)
	}
	if !requestHostMatchesURL(req) {
		return nil, fmt.Errorf("%w: request set Host to %q", ErrHostOverride, req.Host)
	}
	if hasHeaderFold(req.Header, "Host") {
		return nil, fmt.Errorf("%w: request header contains Host", ErrHostOverride)
	}

	ctx := req.Context()
	target, addresses, err := p.resolveTarget(ctx, req.URL.String())
	if err != nil {
		return nil, err
	}

	out := req.Clone(ctx)
	if out.URL == nil {
		return nil, fmt.Errorf("%w: nil URL", ErrInvalidTarget)
	}
	out.URL.Scheme = target.Scheme
	out.URL.User = nil
	out.URL.Host = target.Addr()
	out.Host = ""
	if out.Header == nil {
		out.Header = make(http.Header)
	}
	removeUnsafeHeaders(out.Header)
	p.injectCredentials(out, target)

	addressStrings := make([]string, 0, len(addresses))
	for _, address := range addresses {
		addressStrings = append(addressStrings, address.String())
	}
	pinned := &pinnedTarget{
		host:      target.Host,
		port:      target.Port,
		addresses: addressStrings,
	}
	out = out.WithContext(context.WithValue(out.Context(), pinnedTargetKey{}, pinned))

	resp, err := p.base.RoundTrip(out)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Policy.Do's CheckRedirect returns the response without following it.
		// Removing Location also keeps a caller that wraps the policy in a
		// plain http.Client from accidentally following the redirect.
		resp.Header.Del("Location")
	}
	return resp, nil
}

func hasHeaderFold(header http.Header, name string) bool {
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func requestHostMatchesURL(req *http.Request) bool {
	if req.Host == "" || req.URL == nil {
		return true
	}
	urlHost := req.URL.Hostname()
	overrideHost := req.Host
	overridePort := ""
	if host, port, err := net.SplitHostPort(req.Host); err == nil {
		overrideHost = host
		overridePort = port
	}
	if normalizeHost(overrideHost) != normalizeHost(urlHost) {
		return false
	}
	if overridePort == "" {
		return true
	}
	urlPort := req.URL.Port()
	if urlPort == "" {
		switch strings.ToLower(req.URL.Scheme) {
		case "http":
			urlPort = "80"
		case "https":
			urlPort = "443"
		}
	}
	return overridePort == urlPort
}

func (p *Policy) injectCredentials(req *http.Request, target Target) {
	credentials := make(http.Header)
	mergeHeaders(credentials, p.credentials)
	if p.provider != nil {
		provided, ok := p.provider(target)
		if ok {
			replaceHeaders(credentials, provided)
		}
	}
	removeUnsafeHeaders(credentials)
	for key, values := range credentials {
		if len(values) == 0 {
			continue
		}
		for existing := range req.Header {
			if strings.EqualFold(existing, key) {
				delete(req.Header, existing)
			}
		}
		req.Header[key] = append([]string(nil), values...)
	}
}

func (p *Policy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	pinned, ok := ctx.Value(pinnedTargetKey{}).(*pinnedTarget)
	if !ok || pinned == nil {
		// Fail closed if a connection attempt did not originate from a request
		// that completed policy validation.
		return nil, fmt.Errorf("%w: unvalidated dial", ErrBlockedTarget)
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid dial address", ErrInvalidTarget)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port != pinned.port || normalizeHost(host) != pinned.host {
		return nil, fmt.Errorf("%w: dial destination differs from validated target", ErrBlockedTarget)
	}
	if len(pinned.addresses) == 0 {
		return nil, fmt.Errorf("%w: no validated addresses", ErrBlockedTarget)
	}

	var lastErr error
	for _, ipText := range pinned.addresses {
		addr, ok := parseIPLiteral(ipText)
		if !ok {
			return nil, fmt.Errorf("%w: pinned address %q is forbidden", ErrBlockedAddress, ipText)
		}
		if alwaysForbidden(addr) {
			return nil, fmt.Errorf("%w: pinned address %q is forbidden", ErrBlockedAddress, ipText)
		}
		if p.dial == nil {
			return nil, fmt.Errorf("%w: no dialer", ErrInvalidTarget)
		}
		conn, dialErr := p.dial(ctx, network, net.JoinHostPort(ipText, portText))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no address was dialed")
	}
	return nil, lastErr
}
