package netsec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRequest(remote string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://example.test/login", nil)
	req.RemoteAddr = remote
	return req
}

func TestUntrustedPeerIgnoresForgedForwardingHeaders(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("198.51.100.23:5555")
	req.Header.Set(HeaderXForwardedFor, "203.0.113.1, 10.0.0.1")
	req.Header.Set(HeaderForwarded, `for=203.0.113.2;proto=https`)
	req.Header.Set(HeaderXRealIP, "203.0.113.3")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.23" {
		t.Fatalf("address = %q, want direct peer", identity.Address)
	}
	if identity.TrustedPeer || identity.ChainComplete {
		t.Fatalf("untrusted peer must not use forwarded chain: %+v", identity)
	}
	if identity.Source != SourcePeer {
		t.Fatalf("source = %q, want peer", identity.Source)
	}
}

func TestRightmostNonTrustedHopWins(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8", "192.0.2.0/24")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "6.6.6.6, 198.51.100.10, 192.0.2.50, 10.0.0.9")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.10" {
		t.Fatalf("address = %q, want rightmost untrusted hop", identity.Address)
	}
	if !identity.TrustedPeer || !identity.ChainComplete {
		t.Fatalf("expected a complete trusted chain: %+v", identity)
	}
	if identity.Source != SourceXForwardedFor {
		t.Fatalf("source = %q", identity.Source)
	}
}

func TestTrustedCIDRWithHostBits(t *testing.T) {
	trusted := MustTrustedProxies("10.1.2.3/8", "2001:db8:1::99/32")
	if !trusted.ContainsString("10.200.0.1") {
		t.Fatal("IPv4 CIDR with host bits was not normalized")
	}
	if !trusted.ContainsString("2001:db8:2::1") {
		t.Fatal("IPv6 CIDR with host bits was not normalized")
	}
	if trusted.ContainsString("11.0.0.1") {
		t.Fatal("unrelated address is trusted")
	}
}

func TestDuplicateForwardingHeadersKeepWireOrder(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Add(HeaderXForwardedFor, "6.6.6.6")
	req.Header.Add(HeaderXForwardedFor, "198.51.100.55, 10.0.0.7")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.55" {
		t.Fatalf("address = %q, want 198.51.100.55", identity.Address)
	}
}

func TestForwardedHeaderAndCrossProtocol(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8", "2001:db8::/32")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Proto = "HTTP/2.0"
	req.ProtoMajor = 2
	req.Header.Set(HeaderForwarded, `for="[fd00::2]:443";proto=https, for="[2001:db8:1::1]:8443"`)

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "fd00::2" {
		t.Fatalf("address = %q, want fd00::2", identity.Address)
	}
	if identity.Source != SourceForwarded {
		t.Fatalf("source = %q, want Forwarded", identity.Source)
	}
}

func TestIPv6TrustedProxyChain(t *testing.T) {
	trusted := MustTrustedProxies("2001:db8:1::/48")
	resolver := NewResolver(trusted)

	req := newRequest("[2001:db8:1::2]:8443")
	req.Header.Set(HeaderXForwardedFor, "fd00::2, [2001:db8:1::7]:443")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "fd00::2" || !identity.ChainComplete {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestForwardedAndXFFAgreement(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "198.51.100.4, 10.0.0.7")
	req.Header.Set(HeaderForwarded, "for=198.51.100.4, for=10.0.0.7")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.4" || !identity.ChainComplete {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestConflictingChainsFallBackToPeer(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "198.51.100.4, 10.0.0.7")
	req.Header.Set(HeaderForwarded, "for=198.51.100.5, for=10.0.0.7")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "10.0.0.2" || identity.ChainComplete {
		t.Fatalf("conflicting chains must not be trusted: %+v", identity)
	}
	if identity.HeaderError == "" {
		t.Fatal("expected a header error")
	}
}

func TestMalformedAndIncompleteChainsFallBackToPeer(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	cases := []string{
		"unknown, 10.0.0.7",
		"10.0.0.7, 10.0.0.8",
		", 198.51.100.4, 10.0.0.7",
		"not-an-ip",
	}
	for _, chain := range cases {
		t.Run(chain, func(t *testing.T) {
			req := newRequest("10.0.0.2:443")
			req.Header.Set(HeaderXForwardedFor, chain)
			identity, err := resolver.Resolve(req)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Address != "10.0.0.2" || identity.ChainComplete {
				t.Fatalf("chain %q must fall back to peer: %+v", chain, identity)
			}
		})
	}
}

func TestChainHopLimit(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted, WithMaxForwardedHops(3))

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "198.51.100.1, 198.51.100.2, 198.51.100.3, 10.0.0.7")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "10.0.0.2" || identity.ChainComplete {
		t.Fatalf("oversized chain must fall back: %+v", identity)
	}
}

func TestXRealIPFallback(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted, WithXRealIPFallback())

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXRealIP, "198.51.100.8")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.8" || identity.Source != SourceXRealIP || !identity.ChainComplete {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestXRealIPIsIgnoredByDefault(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXRealIP, "198.51.100.8")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "10.0.0.2" || identity.ChainComplete {
		t.Fatalf("X-Real-IP must not replace a missing chain: %+v", identity)
	}
}

func TestStripForwardingHeadersCaseInsensitive(t *testing.T) {
	header := http.Header{
		"x-forwarded-for":  {"203.0.113.1"},
		"X-Forwarded-For":  {"203.0.113.2"},
		"Forwarded":        {"for=203.0.113.3"},
		"X-Real-IP":        {"203.0.113.4"},
		"CF-Connecting-IP": {"203.0.113.5"},
		"X-Request-ID":     {"keep-me"},
	}
	StripForwardingHeaders(header)
	if header["X-Request-ID"][0] != "keep-me" {
		t.Fatal("unrelated header was removed")
	}
	for key := range header {
		for _, blocked := range forwardingHeaders {
			if strings.EqualFold(key, blocked) {
				t.Fatalf("forwarding header %q was not stripped", key)
			}
		}
	}
}

func TestSanitizeUsesDirectPeerAndRemovesHeaders(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("198.51.100.23:5555")
	req.Header.Set(HeaderXForwardedFor, "203.0.113.1")
	req.Header.Set(HeaderForwarded, "for=203.0.113.2")
	req.Header.Set(HeaderXRealIP, "203.0.113.3")

	identity, err := resolver.Sanitize(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.23" {
		t.Fatalf("address = %q", identity.Address)
	}
	if req.Header.Get(HeaderXForwardedFor) != "" ||
		req.Header.Get(HeaderForwarded) != "" ||
		req.Header.Get(HeaderXRealIP) != "" {
		t.Fatalf("forwarding headers remain: %v", req.Header)
	}
}

func TestSanitizeKeepsResolvedClientForTrustedChain(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "6.6.6.6, 198.51.100.31, 10.0.0.7")

	identity, err := resolver.Sanitize(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "198.51.100.31" {
		t.Fatalf("address = %q", identity.Address)
	}
	if req.Header.Get(HeaderXForwardedFor) != "" {
		t.Fatal("forwarding header was not stripped")
	}
}

func TestMiddlewareStripsHeadersAndPublishesIdentity(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "6.6.6.6, 198.51.100.77, 10.0.0.7")
	req.Header.Set(HeaderXRealIP, "6.6.6.6")

	var got Identity
	handler := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(HeaderXForwardedFor) != "" || req.Header.Get(HeaderXRealIP) != "" {
			t.Fatalf("handler saw forwarding headers: %v", req.Header)
		}
		var ok bool
		got, ok = IdentityFromContext(req.Context())
		if !ok {
			t.Fatal("identity missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got.Address != "198.51.100.77" {
		t.Fatalf("context identity = %+v", got)
	}
}

func TestProxyReconfigurationReevaluates(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "198.51.100.42, 10.0.0.7")

	before, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if before.Address != "198.51.100.42" {
		t.Fatalf("before address = %q", before.Address)
	}

	if err := trusted.Replace("10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	after, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if after.Address != "10.0.0.2" || after.ChainComplete {
		t.Fatalf("after address = %q, chain = %+v", after.Address, after.Chain)
	}
	if resolver.ProxyRevision() < 2 {
		t.Fatalf("revision = %d", resolver.ProxyRevision())
	}

	replacement := MustTrustedProxies("10.0.0.0/8")
	resolver.SetTrustedProxies(replacement)
	again, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Address != "198.51.100.42" {
		t.Fatalf("replacement address = %q", again.Address)
	}
}

func TestFailedProxyReloadKeepsPreviousSet(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	if err := trusted.Replace("not-an-address"); err == nil {
		t.Fatal("expected an error")
	}
	if !trusted.ContainsString("10.1.2.3") {
		t.Fatal("previous proxy set was lost after a failed reload")
	}
	if trusted.Len() != 1 {
		t.Fatalf("entries = %d", trusted.Len())
	}
}

func TestAccountAndDeviceExtraction(t *testing.T) {
	resolver := NewResolver(nil)
	req := newRequest("198.51.100.23:5555")
	req.Header.Set("X-Account-ID", " alice ")
	req.Header.Set("X-Device-ID", "device-a")

	identity, err := resolver.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Account != "alice" || identity.Device != "device-a" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestIPAddressForms(t *testing.T) {
	cases := map[string]string{
		"192.0.2.10":         "192.0.2.10",
		"192.0.2.10:443":     "192.0.2.10",
		"[2001:db8::1]:8443": "2001:db8::1",
		"2001:db8::1":        "2001:db8::1",
		"2001:db8::1%eth0":   "2001:db8::1",
		"::ffff:192.0.2.10":  "192.0.2.10",
		"[2001:db8::1]":      "2001:db8::1",
	}
	for input, want := range cases {
		got, err := CanonicalClientIP(input)
		if err != nil {
			t.Fatalf("CanonicalClientIP(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("CanonicalClientIP(%q) = %q, want %q", input, got, want)
		}
	}
}
