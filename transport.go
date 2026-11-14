package netsec

import (
	"encoding/base64"
	"net/http"
)

// ProxyAuthTransport attaches this hop's own proxy credential to the
// outbound connection toward an authenticated next-hop proxy. The credential
// lives only on the wire to that next hop: it is injected on a per-request
// clone at send time and never appears on the request object that directors,
// middleware, or ScrubHeaders see, so it cannot be forwarded past the hop
// that consumes it.
type ProxyAuthTransport struct {
	// Base is the underlying transport; http.DefaultTransport if nil.
	Base http.RoundTripper
	// ProxyAuthorization is the full Proxy-Authorization value for the
	// next hop, e.g. BasicProxyAuth("user", "pass").
	ProxyAuthorization string
}

// BasicProxyAuth builds a Basic Proxy-Authorization value.
func BasicProxyAuth(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// RoundTrip implements http.RoundTripper.
func (t *ProxyAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Proxy-Authorization", t.ProxyAuthorization)
	return base.RoundTrip(clone)
}
