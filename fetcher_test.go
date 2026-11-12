package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	publicHost   = "crl.example.test"
	publicHostV6 = "crl-v6.example.test"
	rebindHost   = "rebind.example.test"
	metadataHost = "metadata.example.test"
	redirectHost = "redirect.example.test"
)

type testEnv struct {
	server   *httptest.Server
	listener *memListener
	fetcher  *Fetcher
	dialed   []string
	resolves map[string][]netip.Addr
}

func newTestEnv(t *testing.T, mux *http.ServeMux) *testEnv {
	t.Helper()
	listener, server := newMemoryHTTPServer(mux)
	t.Cleanup(server.Close)

	env := &testEnv{
		server:   server,
		listener: listener,
		resolves: map[string][]netip.Addr{
			publicHost:   {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")},
			publicHostV6: {netip.MustParseAddr("2606:4700:4700::1111")},
			redirectHost: {netip.MustParseAddr("9.9.9.9")},
			rebindHost: {
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("169.254.169.254"),
			},
			metadataHost: {netip.MustParseAddr("169.254.169.254")},
		},
	}

	fetcher := NewFetcher(
		WithLookup(func(_ context.Context, host string) ([]netip.Addr, error) {
			addrs, ok := env.resolves[host]
			if !ok {
				return nil, fmt.Errorf("no records for %s", host)
			}
			return addrs, nil
		}),
		WithDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addr := netip.MustParseAddr(host)
			if !isGlobalUnicast(addr) {
				return nil, fmt.Errorf("refusing non-global-unicast dial %s", address)
			}
			env.dialed = append(env.dialed, address)
			_ = network
			return listener.dial()
		}),
	)
	env.fetcher = fetcher
	return env
}

func (e *testEnv) httpURL(host, path string) string {
	return fmt.Sprintf("http://%s%s", host, path)
}

func certWithURIs(uris ...string) *asn1DistributionPoint {
	return &asn1DistributionPoint{DistributionPoint: fullName(uris...)}
}

func TestSchemeAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/crl.der", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte("CRL-BYTES"))
	})
	env := newTestEnv(t, mux)

	allowed := env.httpURL(publicHost, "/crl.der")
	// Only four distribution points fit in one certificate (per-cert cap);
	// ldap, file, the scheme-less entry and the http URL together exercise
	// the scheme allowlist while leaving room for the allowed endpoint.
	cert := certWithCDP(buildCDPExtension(
		*certWithURIs("ldap://ldap.example.test/CN=CA,DC=test?certificateRevocationList"),
		*certWithURIs("file:///etc/pki/tls/crl/ca.crl"),
		*certWithURIs("/relative/path/ca.crl"),
		*certWithURIs(allowed),
	))

	bodies, err := NewChainFetcher(env.fetcher).FetchCRLForCert(context.Background(), cert)
	if err != nil {
		t.Fatalf("FetchCRLForCert: %v", err)
	}
	if len(bodies) != 1 || string(bodies[0]) != "CRL-BYTES" {
		t.Fatalf("unexpected bodies: %v", bodies)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("endpoint hit %d times, want 1", got)
	}
	if len(env.dialed) != 1 || !strings.HasPrefix(env.dialed[0], "8.8.8.8:") {
		t.Fatalf("dialed %v, want only validated public address", env.dialed)
	}

	for _, bad := range []string{
		"ldap://ldap.example.test/CN=CA",
		"ldaps://ldap.example.test/CN=CA",
		"file:///etc/pki/tls/crl/ca.crl",
		"ftp://ftp.example.test/ca.crl",
		"/relative/path/ca.crl",
		"LDAP://uppercase.example.test/CN=CA",
	} {
		if _, err := env.fetcher.FetchCRL(context.Background(), bad); !errors.Is(err, ErrSchemeNotAllowed) {
			t.Fatalf("FetchCRL(%q) err = %v, want ErrSchemeNotAllowed", bad, err)
		}
	}
}
