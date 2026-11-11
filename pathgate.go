// Package pathgate is a reverse proxy gateway whose route matching and
// upstream forwarding share one canonical view of the request path,
// provided by pathx.Normalize.
package pathgate

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"example.org/pathgate/pathx"
)

// Route maps a canonical decoded path prefix to an upstream target.
type Route struct {
	// Prefix is matched against the decoded canonical path on segment
	// boundaries, e.g. "/api" matches "/api" and "/api/users" only.
	Prefix string
	Target *url.URL
}

// Gateway dispatches requests to the first matching route.
type Gateway struct {
	Routes []Route
	// Transport reaches the upstreams; nil means http.DefaultTransport.
	Transport http.RoundTripper
}

func matchRoute(routes []Route, decoded string) *Route {
	for i := range routes {
		p := routes[i].Prefix
		if decoded == p || strings.HasPrefix(decoded, strings.TrimSuffix(p, "/")+"/") {
			return &routes[i]
		}
	}
	return nil
}

// ServeHTTP normalizes the raw path once, uses the decoded form for route
// matching, and forwards the escaped form so the upstream sees exactly the
// segments the gateway authorized.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	decoded, escaped, err := pathx.Normalize(r.URL.EscapedPath())
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	route := matchRoute(g.Routes, decoded)
	if route == nil {
		http.Error(w, "no route", http.StatusNotFound)
		return
	}
	target := route.Target
	proxy := &httputil.ReverseProxy{
		Transport: g.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = decoded
			pr.Out.URL.RawPath = escaped
			// The raw query is forwarded untouched: duplicate parameters
			// and their order are preserved.
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
		},
	}
	proxy.ServeHTTP(w, r)
}
