package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeResolver struct {
	leaf *x509.Certificate
	err  error
}

func (f fakeResolver) LeafForConnection(*tls.ConnectionState) (*x509.Certificate, error) {
	return f.leaf, f.err
}

func newRequest(host, sni string, state *tls.ConnectionState) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://placeholder/", nil)
	r.Host = host
	if state == nil {
		state = &tls.ConnectionState{}
	}
	if sni != "" {
		state.ServerName = sni
	}
	r.TLS = state
	return r
}

func expectReason(t *testing.T, b *Binder, r *http.Request, want string) {
	t.Helper()
	err := b.Check(r)
	if err == nil {
		t.Fatalf("expected rejection %q, request was allowed", want)
	}
	if err.Reason != want {
		t.Fatalf("reason = %q, want %q", err.Reason, want)
	}
}

func expectAllowed(t *testing.T, b *Binder, r *http.Request) {
	t.Helper()
	if err := b.Check(r); err != nil {
		t.Fatalf("expected allow, got %s", err)
	}
}

func TestBinderNonTLSAndMalformed(t *testing.T) {
	b := New(WithLeafResolver(fakeResolver{leaf: mustLeaf(t, []string{"example.com"})}))

	plain := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	expectReason(t, b, plain, ReasonNonTLS)

	badAuthority := newRequest("a..b:443", "example.com", nil)
	expectReason(t, b, badAuthority, ReasonInvalidAuthority)

	missingSNI := newRequest("example.com", "", nil)
	expectReason(t, b, missingSNI, ReasonMissingSNI)

	badSNI := newRequest("example.com", "a..b", nil)
	expectReason(t, b, badSNI, ReasonInvalidSNI)

	noLeaf := New()
	expectReason(t, noLeaf, newRequest("example.com", "example.com", nil), ReasonNoLeaf)
}

func TestBinderSameHostVariants(t *testing.T) {
	leaf := mustLeaf(t, []string{"example.com", "*.example.com"})
	b := New(WithLeafResolver(fakeResolver{leaf: leaf}))

	// Case, trailing dot and port are normalized; same logical host passes even
	// without a coalesce policy.
	for _, host := range []string{
		"example.com",
		"EXAMPLE.COM",
		"example.com.",
		"example.com:443",
	} {
		expectAllowed(t, b, newRequest(host, "example.com", nil))
	}

	// A distinct host covered only by a wildcard SAN still needs an explicit
	// coalesce policy; without one it is refused.
	expectReason(t, b,
		newRequest("www.example.com:8443", "example.com", nil),
		ReasonCoalesceDenied)

	withPolicy := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
	)
	expectAllowed(t, withPolicy, newRequest("www.example.com:8443", "example.com", nil))
}

func TestBinderSANMismatch(t *testing.T) {
	leaf := mustLeaf(t, []string{"a.example"})
	b := New(WithLeafResolver(fakeResolver{leaf: leaf}))

	// Distinct host with no coalesce policy is denied at the policy stage.
	expectReason(t, b,
		newRequest("b.example", "a.example", nil),
		ReasonCoalesceDenied)

	// With a permissive coalesce policy, the missing SAN now drives the reject.
	permissive := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
	)
	expectReason(t, permissive,
		newRequest("b.example", "a.example", nil),
		ReasonSANMismatch)

	// Wildcard depth: a.b.example needs two labels, not covered by one-label.
	star := New(
		WithLeafResolver(fakeResolver{leaf: mustLeaf(t, []string{"*.example"})}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
	)
	expectReason(t, star,
		newRequest("a.b.example", "a.example", nil),
		ReasonSANMismatch)
}

func TestBinderCoalescePolicy(t *testing.T) {
	leaf := mustLeaf(t, []string{"a.example", "b.example"})

	denied := New(WithLeafResolver(fakeResolver{leaf: leaf}))
	expectReason(t, denied,
		newRequest("b.example", "a.example", nil),
		ReasonCoalesceDenied)

	allowed := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(handshake, request string) bool {
			return handshake == "a.example" && request == "b.example"
		}),
	)
	expectAllowed(t, allowed, newRequest("b.example", "a.example", nil))

	// A policy that rejects the pair still fails closed, SANs notwithstanding.
	blocked := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return false }),
	)
	expectReason(t, blocked,
		newRequest("b.example", "a.example", nil),
		ReasonCoalesceDenied)
}

func TestBinderClientCertMustOwnHandshake(t *testing.T) {
	leaf := mustLeaf(t, []string{"a.example", "b.example"})

	// Policy marks b as a client-cert vhost. A cross-host request is rejected
	// regardless of the coalesce policy.
	b := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
		WithClientCertPolicy(func(host string) bool { return host == "b.example" }),
	)
	expectReason(t, b,
		newRequest("b.example", "a.example", nil),
		ReasonClientCertHandshake)

	// A connection that already carries a verified client certificate cannot be
	// reused for any other virtual host.
	mtlsState := &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{mustLeaf(t, []string{"client"})},
		VerifiedChains:   [][]*x509.Certificate{{mustLeaf(t, []string{"client"})}},
	}
	plain := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
	)
	expectReason(t, plain,
		newRequest("b.example", "a.example", mtlsState),
		ReasonClientCertHandshake)

	// Same-host traffic on its own mTLS handshake remains allowed.
	sameHost := newRequest("a.example", "a.example", mtlsState)
	expectAllowed(t, plain, sameHost)
}

func TestBinderIDNEquivalence(t *testing.T) {
	leaf := mustLeaf(t, []string{"xn--bcher-kva.de"})
	b := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
	)

	// U-label authority, A-label SNI: both canonicalize to the same A-label and
	// the SAN covers it.
	expectAllowed(t, b, newRequest("bücher.de", "xn--bcher-kva.de", nil))
	expectAllowed(t, b, newRequest("BÜCHER.DE.", "XN--BCHER-KVA.DE.", nil))

	// A different IDN host is not covered.
	expectReason(t, b,
		newRequest("日本語.jp", "xn--bcher-kva.de", nil),
		ReasonSANMismatch)
}

func TestBinderWrapWrites421(t *testing.T) {
	leaf := mustLeaf(t, []string{"a.example"})
	var observed *IdentityError
	b := New(
		WithLeafResolver(fakeResolver{leaf: leaf}),
		WithRejectObserver(func(_ *http.Request, e *IdentityError) { observed = e }),
	)

	handler := b.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	good := httptest.NewRecorder()
	handler.ServeHTTP(good, newRequest("a.example", "a.example", nil))
	if good.Code != http.StatusOK {
		t.Fatalf("good request status = %d, want 200", good.Code)
	}

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, newRequest("b.example", "a.example", nil))
	if bad.Code != http.StatusMisdirectedRequest {
		t.Fatalf("bad request status = %d, want 421", bad.Code)
	}
	if observed == nil || observed.Reason != ReasonCoalesceDenied {
		t.Fatalf("observer reason = %v, want %q", observed, ReasonCoalesceDenied)
	}
}
