package netsec

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// hopByHopHeaders are connection-specific header fields (RFC 9110 §7.6.1)
// that must never be forwarded to the next hop. Proxy-Authorization and
// Proxy-Authenticate (RFC 9110 §11.7) carry hop-scoped proxy credentials and
// are always stripped so they can never leak into an origin access log.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// forwardedHeaders are rebuilt from scratch on every hop. Client-supplied
// values are never trusted and are stripped before the chain is recomputed.
var forwardedHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-IP",
}

// ScrubHeaders returns a freshly allocated header set derived from src with
// all hop-by-hop headers removed: the fixed hop-by-hop list, the Connection
// header itself, and every header named by a Connection token. src is never
// mutated; forwarding must rebuild the header set rather than edit in place.
func ScrubHeaders(src http.Header) http.Header {
	skip := make(map[string]bool, len(hopByHopHeaders)+4)
	for _, name := range hopByHopHeaders {
		skip[http.CanonicalHeaderKey(name)] = true
	}
	for _, value := range src.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				skip[http.CanonicalHeaderKey(token)] = true
			}
		}
	}

	dst := make(http.Header, len(src))
	for name, values := range src {
		if skip[http.CanonicalHeaderKey(name)] {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		dst[name] = copied
	}
	return dst
}

// Forwarder rebuilds outbound request headers for a reverse proxy hop. It
// scrubs hop-by-hop headers, strips client-supplied forwarding headers, and
// appends to the X-Forwarded chain only on behalf of trusted proxies.
type Forwarder struct {
	// TrustedProxies lists CIDR ranges of direct peers whose forwarded
	// chain may be honored. A peer outside these ranges is treated as an
	// untrusted client: any X-Forwarded-* it sent is discarded.
	TrustedProxies []*net.IPNet
}

// ParseCIDRs parses CIDR strings into the TrustedProxies form.
func ParseCIDRs(cidrs []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
}

func (f *Forwarder) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, ipNet := range f.TrustedProxies {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// RewriteRequest rebuilds out.Header from in. The result contains no
// hop-by-hop headers and no proxy credentials; the X-Forwarded chain is
// recomputed: a trusted peer's existing chain is preserved and extended,
// anything an untrusted client supplied is dropped, and the direct peer IP
// is appended. in is left unmodified.
func (f *Forwarder) RewriteRequest(out, in *http.Request) {
	header := ScrubHeaders(in.Header)
	for _, name := range forwardedHeaders {
		header.Del(name)
	}

	clientIP, _, err := net.SplitHostPort(in.RemoteAddr)
	if err != nil {
		clientIP = in.RemoteAddr
	}

	chain := make([]string, 0, 4)
	if f.isTrusted(net.ParseIP(clientIP)) {
		for _, value := range in.Header.Values("X-Forwarded-For") {
			for _, hop := range strings.Split(value, ",") {
				if hop = strings.TrimSpace(hop); hop != "" {
					chain = append(chain, hop)
				}
			}
		}
	}
	if clientIP != "" {
		chain = append(chain, clientIP)
	}
	if len(chain) > 0 {
		header.Set("X-Forwarded-For", strings.Join(chain, ", "))
	}

	proto := "http"
	if in.TLS != nil {
		proto = "https"
	}
	header.Set("X-Forwarded-Proto", proto)
	if in.Host != "" {
		header.Set("X-Forwarded-Host", in.Host)
	}

	out.Header = header
}

// ProxyRewrite returns an httputil.ReverseProxy rewrite function that points
// the outbound request at target and rebuilds its headers via RewriteRequest.
func (f *Forwarder) ProxyRewrite(target *url.URL) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		pr.Out.URL.Scheme = target.Scheme
		pr.Out.URL.Host = target.Host
		pr.Out.URL.Path = singleJoiningSlash(target.Path, pr.In.URL.Path)
		pr.Out.Host = ""
		f.RewriteRequest(pr.Out, pr.In)
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
