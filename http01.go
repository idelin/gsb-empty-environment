package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrHTTP01Redirect is returned when an http-01 redirect leaves the IP
	// address named by the identifier, or otherwise has an unsafe target.
	ErrHTTP01Redirect = errors.New("netsec: http-01 redirect left the identifier address")
	// ErrHTTP01Token is returned for malformed challenge tokens.
	ErrHTTP01Token = errors.New("netsec: invalid http-01 challenge token")
	// ErrHTTP01Status is returned when the final http-01 response is not 200.
	ErrHTTP01Status = errors.New("netsec: unexpected http-01 response status")
	// ErrHTTP01KeyAuthorization is returned when the response body does not
	// contain the expected key authorization.
	ErrHTTP01KeyAuthorization = errors.New("netsec: http-01 key authorization mismatch")
)

const (
	http01MaxRedirects = 10
	http01MaxBodyBytes = 1 << 20
)

// HTTP01ChallengeURL returns the URL for an http-01 challenge. The URL host is
// always the literal IP address from the identifier, never a DNS name.
func (id Identifier) HTTP01ChallengeURL(token string) (string, error) {
	addr, err := id.CanonicalIPAddr()
	if err != nil {
		return "", err
	}
	if err := validateHTTP01Token(token); err != nil {
		return "", err
	}
	return "http://" + httpHost(addr) + HTTP01ChallengePath + token, nil
}

// HTTP01HostHeader returns the Host header value required by RFC 8738. IPv6
// literals are bracketed because the value is used in an HTTP authority.
func (id Identifier) HTTP01HostHeader() (string, error) {
	addr, err := id.CanonicalIPAddr()
	if err != nil {
		return "", err
	}
	return httpHost(addr), nil
}

// FetchHTTP01 performs the http-01 GET request and returns the response body.
//
// The request is sent directly to the identifier's address. A supplied client
// may override the transport (for tests or custom networking), but its
// redirect policy is replaced and every request is pinned to the identifier
// address. Redirects to a different IP address or to a DNS name are rejected.
func FetchHTTP01(ctx context.Context, id Identifier, token string, clients ...*http.Client) ([]byte, error) {
	var base *http.Client
	if len(clients) > 0 {
		base = clients[0]
	}
	if err := id.ValidateChallenge(ChallengeHTTP01); err != nil {
		return nil, err
	}
	addr, err := id.IPAddr()
	if err != nil {
		return nil, err
	}
	challengeURL, err := id.HTTP01ChallengeURL(token)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, challengeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidIPIdentifier, err)
	}
	req.Header.Set("Accept", "*/*")

	client := http01Client(addr, base)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%w: empty HTTP response", ErrHTTP01Status)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: got %s", ErrHTTP01Status, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, http01MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("netsec: reading http-01 response: %w", err)
	}
	if len(body) > http01MaxBodyBytes {
		return nil, fmt.Errorf("%w: response body exceeds %d bytes", ErrHTTP01Status, http01MaxBodyBytes)
	}
	return body, nil
}

// ValidateHTTP01 fetches the challenge and verifies that the response body is
// the expected key authorization.
func ValidateHTTP01(ctx context.Context, id Identifier, token, keyAuthorization string, clients ...*http.Client) error {
	if keyAuthorization == "" {
		return fmt.Errorf("%w: empty key authorization", ErrHTTP01KeyAuthorization)
	}
	body, err := FetchHTTP01(ctx, id, token, clients...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != keyAuthorization {
		return ErrHTTP01KeyAuthorization
	}
	return nil
}

func validateHTTP01Token(token string) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", ErrHTTP01Token)
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return fmt.Errorf("%w: %q", ErrHTTP01Token, token)
		}
	}
	return nil
}

func httpHost(addr netip.Addr) string {
	if addr.Is4() {
		return addr.String()
	}
	return "[" + addr.String() + "]"
}

func http01Client(addr netip.Addr, base *http.Client) *http.Client {
	var client http.Client
	if base != nil {
		client = *base
	}
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}

	var transport http.RoundTripper
	if base != nil && base.Transport != nil {
		transport = base.Transport
	} else {
		transport = newDirectHTTPTransport(addr)
	}
	client.Transport = &hostPinnedTransport{
		base: transport,
		addr: addr,
		host: httpHost(addr),
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= http01MaxRedirects {
			return fmt.Errorf("%w: stopped after %d redirects", ErrHTTP01Redirect, http01MaxRedirects)
		}
		return checkHTTP01URL(req.URL, addr)
	}
	return &client
}

func checkHTTP01URL(u *url.URL, addr netip.Addr) error {
	if u == nil {
		return fmt.Errorf("%w: nil redirect target", ErrHTTP01Redirect)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported redirect scheme %q", ErrHTTP01Redirect, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: redirect target contains user information", ErrHTTP01Redirect)
	}
	target, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return fmt.Errorf("%w: redirect target %q is not an IP address", ErrHTTP01Redirect, u.Hostname())
	}
	if target.Zone() != "" || target.Is4In6() || target != addr {
		return fmt.Errorf("%w: got %s, want %s", ErrHTTP01Redirect, target, addr)
	}
	return nil
}

// hostPinnedTransport is the last line of defence for redirects. Every
// request, including one produced by a custom RoundTripper chain, must target
// the pinned address, and the Host header is always the identifier's address.
type hostPinnedTransport struct {
	base http.RoundTripper
	addr netip.Addr
	host string
}

func (t *hostPinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkHTTP01URL(req.URL, t.addr); err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Host = t.host
	return t.base.RoundTrip(clone)
}

func newDirectHTTPTransport(addr netip.Addr) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	// Never route challenge traffic through an environment HTTP proxy.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			port = http01DefaultPort
		}
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
	}
	return transport
}
