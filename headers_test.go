package netsec

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

// hop simulates one network hop without sockets (the sandbox forbids
// listeners): it serves the request through handler as if it arrived on a
// connection from peer remoteAddr.
type hop struct {
	handler    http.Handler
	remoteAddr string
}

func (h hop) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.RemoteAddr = h.remoteAddr
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, clone)
	return rec.Result(), nil
}

// proxyHandler builds one reverse-proxy hop on the component: headers are
// rebuilt by the Forwarder and the request leaves through transport rt.
func proxyHandler(f *Forwarder, target string, rt http.RoundTripper) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		panic(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: f.ProxyRewrite(u)}
	proxy.Transport = rt
	return proxy
}

func clientRequest(remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://front/", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func serve(h http.Handler, req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// 代理口令泄露: Proxy-Authorization/Proxy-Authenticate are scrubbed before
// forwarding, so the origin never logs the proxy password.
func TestProxyCredentialsNeverReachOrigin(t *testing.T) {
	var seen http.Header
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	})

	front := proxyHandler(&Forwarder{}, "http://origin/", hop{origin, "10.0.0.1:8000"})

	req := clientRequest("203.0.113.9:1234")
	req.Header.Set("Proxy-Authorization", BasicProxyAuth("attacker", "s3cret"))
	req.Header.Set("Proxy-Authenticate", "Basic realm=\"proxy\"")
	req.Header.Set("X-Keep-Me", "yes")

	resp := serve(front, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy chain failed: %s", resp.Status)
	}

	if got := seen.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("origin received proxy password: %q", got)
	}
	if got := seen.Get("Proxy-Authenticate"); got != "" {
		t.Fatalf("origin received Proxy-Authenticate: %q", got)
	}
	if got := seen.Get("X-Keep-Me"); got != "yes" {
		t.Fatalf("end-to-end header lost, got %q", got)
	}
}

// Connection 列头: headers named by Connection tokens are hop-by-hop too.
func TestConnectionListedHeadersRemoved(t *testing.T) {
	var seen http.Header
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	})

	front := proxyHandler(&Forwarder{}, "http://origin/", hop{origin, "10.0.0.1:8000"})

	req := clientRequest("203.0.113.9:1234")
	req.Header.Set("Connection", "X-Hop-Token, X-Proxy-Flag")
	req.Header.Set("X-Hop-Token", "abc")
	req.Header.Set("X-Proxy-Flag", "1")
	req.Header.Set("X-End-To-End", "keep")

	resp := serve(front, req)
	resp.Body.Close()

	for _, name := range []string{"X-Hop-Token", "X-Proxy-Flag"} {
		if got := seen.Get(name); got != "" {
			t.Fatalf("Connection-listed header %s forwarded: %q", name, got)
		}
	}
	if got := seen.Get("X-End-To-End"); got != "keep" {
		t.Fatalf("end-to-end header lost, got %q", got)
	}
}

// 嵌套代理: each hop's credential is valid only on its own connection;
// neither hop's password leaks downstream to the next proxy or the origin.
func TestNestedProxiesIsolateCredentials(t *testing.T) {
	const credFront = "Basic ZnJvbnQ6cHc="
	const credMiddle = "Basic bWlkZGxlOnB3"

	var originSeen http.Header
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originSeen = r.Header.Clone()
	})

	middle := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Proxy-Authorization")
		if got == credFront {
			http.Error(w, "front credential leaked to middle", http.StatusBadGateway)
			return
		}
		if got != credMiddle {
			http.Error(w, "middle auth failed: "+got, http.StatusProxyAuthRequired)
			return
		}
		proxyHandler(&Forwarder{}, "http://origin/", hop{origin, "10.0.0.2:8000"}).ServeHTTP(w, r)
	})

	front := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Proxy-Authorization"); got != credFront {
			http.Error(w, "front auth failed: "+got, http.StatusProxyAuthRequired)
			return
		}
		rt := &ProxyAuthTransport{
			Base:               hop{middle, "10.0.0.1:8000"},
			ProxyAuthorization: credMiddle,
		}
		proxyHandler(&Forwarder{}, "http://middle/", rt).ServeHTTP(w, r)
	})

	req := clientRequest("203.0.113.9:1234")
	req.Header.Set("Proxy-Authorization", credFront)

	resp := serve(front, req)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nested proxy chain failed: %s", resp.Status)
	}
	if got := originSeen.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("origin received proxy credential through nested chain: %q", got)
	}
}

// 伪造转发链: an untrusted client's X-Forwarded-For is stripped and
// replaced with the real peer address.
func TestForgedForwardedChainDropped(t *testing.T) {
	var seen http.Header
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	})

	front := proxyHandler(&Forwarder{}, "http://origin/", hop{origin, "10.0.0.1:8000"})

	req := clientRequest("203.0.113.9:1234")
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8")
	req.Header.Set("X-Real-IP", "9.9.9.9")

	resp := serve(front, req)
	resp.Body.Close()

	chain := seen.Get("X-Forwarded-For")
	if strings.Contains(chain, "9.9.9.9") || strings.Contains(chain, "8.8.8.8") {
		t.Fatalf("forged forwarding chain propagated: %q", chain)
	}
	if chain != "203.0.113.9" {
		t.Fatalf("chain should contain only the real client address, got %q", chain)
	}
	if got := seen.Get("X-Real-IP"); got != "" {
		t.Fatalf("forged X-Real-IP propagated: %q", got)
	}
}

// 合法代理: a trusted proxy's chain is preserved and extended per hop, and
// even with credentials injected toward the next hop the origin sees none.
func TestTrustedProxyChainAppended(t *testing.T) {
	trusted, err := ParseCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	var originSeen http.Header
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originSeen = r.Header.Clone()
	})

	const upstreamCred = "Basic dXBzdHJlYW06cHc="
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Proxy-Authorization"); got != upstreamCred {
			http.Error(w, "upstream auth failed", http.StatusProxyAuthRequired)
			return
		}
		f := &Forwarder{TrustedProxies: trusted}
		proxyHandler(f, "http://origin/", hop{origin, "10.0.0.2:8000"}).ServeHTTP(w, r)
	})

	legit := proxyHandler(
		&Forwarder{TrustedProxies: trusted},
		"http://upstream/",
		&ProxyAuthTransport{
			Base:               hop{upstream, "10.0.0.1:8000"},
			ProxyAuthorization: upstreamCred,
		},
	)

	req := clientRequest("10.0.0.9:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	resp := serve(legit, req)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legitimate proxy chain failed: %s", resp.Status)
	}

	chain := originSeen.Get("X-Forwarded-For")
	if want := "203.0.113.7, 10.0.0.9, 10.0.0.1"; chain != want {
		t.Fatalf("trusted chain not preserved and appended per hop: got %q, want %q", chain, want)
	}
	if got := originSeen.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("origin received proxy credential from legitimate chain: %q", got)
	}
	if got := originSeen.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("X-Forwarded-Proto not rebuilt: %q", got)
	}
}

// 重建而非增量修改: scrubbing builds a new header set and never mutates
// the inbound request's headers.
func TestScrubRebuildsWithoutMutating(t *testing.T) {
	src := http.Header{
		"Proxy-Authorization": {"Basic secret"},
		"Connection":          {"X-Hop"},
		"X-Hop":               {"v"},
		"X-Data":              {"a", "b"},
	}
	out := ScrubHeaders(src)

	if got := src.Get("Proxy-Authorization"); got != "Basic secret" {
		t.Fatalf("source header mutated: %q", got)
	}
	if got := src.Get("X-Hop"); got != "v" {
		t.Fatalf("source header mutated: %q", got)
	}
	if out.Get("Proxy-Authorization") != "" || out.Get("Connection") != "" || out.Get("X-Hop") != "" {
		t.Fatalf("hop-by-hop headers survived scrub: %v", out)
	}
	out["X-Data"][0] = "changed"
	if src["X-Data"][0] != "a" {
		t.Fatal("scrubbed header shares backing storage with source")
	}
}

// 代理凭据只在连接上: the credential is injected on the wire clone, not on
// the request object held by the proxy.
func TestProxyAuthTransportKeepsRequestClean(t *testing.T) {
	var wireSeen string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		wireSeen = req.Header.Get("Proxy-Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	rt := &ProxyAuthTransport{Base: base, ProxyAuthorization: "Basic aG9wOnB3"}
	req, _ := http.NewRequest(http.MethodGet, "http://nexthop/", nil)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if wireSeen != "Basic aG9wOnB3" {
		t.Fatalf("next hop did not receive credential: %q", wireSeen)
	}
	if got := req.Header.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("credential entered the request object: %q", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
