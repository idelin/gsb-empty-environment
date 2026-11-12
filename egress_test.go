package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
)

// scriptResolver answers LookupNetIP from a per-call script, letting tests
// simulate answers that change between lookups (DNS rebinding).
type scriptResolver struct {
	mu       sync.Mutex
	script   [][]netip.Addr
	err      error
	calls    int
	networks []string
	hosts    []string
}

func (r *scriptResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.networks = append(r.networks, network)
	r.hosts = append(r.hosts, host)
	if r.err != nil {
		return nil, r.err
	}
	if r.calls >= len(r.script) {
		return nil, errors.New("scriptResolver: no more scripted answers")
	}
	out := append([]netip.Addr(nil), r.script[r.calls]...)
	r.calls++
	return out, nil
}

func (r *scriptResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type staticResolver struct {
	hosts map[string][]netip.Addr
}

func (r *staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if addrs, ok := r.hosts[host]; ok {
		return append([]netip.Addr(nil), addrs...), nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// dialRecorder records every address handed to the underlying dialer and
// optionally forwards to a real dial target (test seam for loopback servers).
type dialRecorder struct {
	mu     sync.Mutex
	dialed []string
	target string
}

func (d *dialRecorder) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	target := d.target
	d.mu.Unlock()
	if target != "" {
		var dl net.Dialer
		return dl.DialContext(ctx, network, target)
	}
	return nil, &net.OpError{Op: "dial", Net: network, Addr: &net.TCPAddr{}, Err: errors.New("dialRecorder: network disabled in test")}
}

func (d *dialRecorder) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dialed...)
}

// record only captures the address; use it when another seam produces the
// actual connection.
func (d *dialRecorder) record(address string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dialed = append(d.dialed, address)
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return addr
}

func TestNormalizeAuthority(t *testing.T) {
	tests := []struct {
		name    string
		scheme  string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "http default port", scheme: "http", raw: "example.com", want: "example.com:80"},
		{name: "https default port", scheme: "https", raw: "example.com", want: "example.com:443"},
		{name: "uppercase host lowercased", scheme: "https", raw: "API.Example.COM", want: "api.example.com:443"},
		{name: "explicit port kept", scheme: "https", raw: "example.com:8443", want: "example.com:8443"},
		{name: "scheme case insensitive", scheme: "HTTPS", raw: "Example.COM:443", want: "example.com:443"},
		{name: "ipv6 bracket default port", scheme: "https", raw: "[2001:db8::2]", want: "[2001:db8::2]:443"},
		{name: "ipv6 bracket explicit port", scheme: "http", raw: "[2001:db8::1]:8080", want: "[2001:db8::1]:8080"},
		{name: "ipv4 canonicalized", scheme: "http", raw: "198.51.100.7:8080", want: "198.51.100.7:8080"},
		{name: "ipv6 without brackets rejected", scheme: "https", raw: "2001:db8::1", wantErr: true},
		{name: "unterminated bracket rejected", scheme: "https", raw: "[2001:db8::1", wantErr: true},
		{name: "bad port rejected", scheme: "https", raw: "example.com:99999", wantErr: true},
		{name: "unsupported scheme rejected", scheme: "gopher", raw: "example.com", wantErr: true},
		{name: "empty authority rejected", scheme: "https", raw: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := normalizeAuthority(tt.scheme, tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", auth.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := auth.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUppercaseHostnameMatchesAllowList(t *testing.T) {
	resolver := &staticResolver{hosts: map[string][]netip.Addr{
		"api.example.com": {mustAddr(t, "198.51.100.10")},
	}}
	authz, err := New([]Entry{{Scheme: "HTTPS", Authority: "API.Example.COM"}}, WithResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "api.example.com",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := decision.Authority(), "api.example.com:443"; got != want {
		t.Fatalf("canonical authority = %q, want %q", got, want)
	}
	addrs := decision.Addrs()
	if len(addrs) != 1 || addrs[0] != mustAddr(t, "198.51.100.10") {
		t.Fatalf("Addrs = %v, want [198.51.100.10]", addrs)
	}
}

func TestMetadataAndSensitiveAddressesBlocked(t *testing.T) {
	blocked := []string{
		"169.254.169.254",
		"127.0.0.1",
		"127.1.2.3",
		"10.0.0.5",
		"172.16.4.9",
		"192.168.1.1",
		"0.0.0.0",
		"169.254.10.10",
		"::1",
		"fe80::1",
		"fc00::1",
		"::",
	}
	for _, text := range blocked {
		if err := validateIP(mustAddr(t, text)); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("validateIP(%s) error = %v, want ErrBlockedAddress", text, err)
		}
	}

	allowed := []string{"198.51.100.1", "203.0.113.2", "8.8.8.8", "2001:db8::1"}
	for _, text := range allowed {
		if err := validateIP(mustAddr(t, text)); err != nil {
			t.Errorf("validateIP(%s) unexpected error: %v", text, err)
		}
	}

	if _, err := New([]Entry{{Scheme: "http", Authority: "169.254.169.254"}}); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("New(metadata literal) error = %v, want ErrBlockedAddress", err)
	}

	resolver := &staticResolver{hosts: map[string][]netip.Addr{
		"metadata.attacker.test": {mustAddr(t, "169.254.169.254")},
	}}
	authz, err := New([]Entry{{Scheme: "http", Authority: "metadata.attacker.test"}}, WithResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = authz.Check(context.Background(), CheckInput{Scheme: "http", Authority: "metadata.attacker.test"})
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Check error = %v, want ErrBlockedAddress", err)
	}
}

func TestDNSRebindConnectionPinsFirstResolution(t *testing.T) {
	good := mustAddr(t, "198.51.100.50")
	metadata := mustAddr(t, "169.254.169.254")
	resolver := &scriptResolver{script: [][]netip.Addr{{good}, {metadata}}}

	authz, err := New([]Entry{{Scheme: "https", Authority: "rebind.example.com"}}, WithResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "rebind.example.com",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	recorder := &dialRecorder{}
	decision.baseDial = recorder.dial

	conn, err := decision.DialContext(context.Background(), "tcp")
	if conn != nil {
		t.Fatal("expected dial to fail with recorder, got a connection")
	}
	if err == nil {
		t.Fatal("expected dial error from recorder")
	}
	if got := recorder.snapshot(); len(got) != 1 || got[0] != "198.51.100.50:443" {
		t.Fatalf("dialed addresses = %v, want [198.51.100.50:443]", got)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls = %d, want exactly 1 (no re-resolution at dial time)", resolver.callCount())
	}
	if addrs := decision.Addrs(); len(addrs) != 1 || addrs[0] != good {
		t.Fatalf("pinned Addrs = %v, want [%s]", addrs, good)
	}

	if _, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "rebind.example.com",
	}); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("second Check error = %v, want ErrBlockedAddress", err)
	}
}

func TestHostHeaderMustMatchAuthority(t *testing.T) {
	authz, err := New([]Entry{{Scheme: "https", Authority: "example.com"}},
		WithResolver(&staticResolver{hosts: map[string][]netip.Addr{
			"example.com": {mustAddr(t, "198.51.100.20")},
		}}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "example.com",
		Host:      "evil.example.org",
	})
	if !errors.Is(err, ErrHostMismatch) {
		t.Fatalf("mismatched Host error = %v, want ErrHostMismatch", err)
	}

	// Case-insensitive, default-port-normalized Host values are accepted.
	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "example.com",
		Host:      "EXAMPLE.COM:443",
	})
	if err != nil {
		t.Fatalf("equivalent Host rejected: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://example.com/path", nil)
	decision.Apply(req)
	if req.Host != "example.com:443" {
		t.Fatalf("req.Host = %q, want example.com:443", req.Host)
	}
}

func TestForbiddenHeaders(t *testing.T) {
	authz, err := New([]Entry{{Scheme: "https", Authority: "example.com"}},
		WithResolver(&staticResolver{hosts: map[string][]netip.Addr{
			"example.com": {mustAddr(t, "198.51.100.20")},
		}}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, hdr := range []string{"Connection", "Transfer-Encoding", "Upgrade"} {
		h := http.Header{}
		h.Set(hdr, "x")
		_, err := authz.Check(context.Background(), CheckInput{
			Scheme:    "https",
			Authority: "example.com",
			Header:    h,
		})
		if !errors.Is(err, ErrForbiddenHeader) {
			t.Errorf("header %s: error = %v, want ErrForbiddenHeader", hdr, err)
		}
	}

	// A lower-case key is canonicalized by http.Header and still caught.
	h := http.Header{}
	h.Set("connection", "keep-alive")
	if err := checkForbiddenHeaders(h); !errors.Is(err, ErrForbiddenHeader) {
		t.Fatalf("lower-case Connection: error = %v, want ErrForbiddenHeader", err)
	}

	// Ordinary headers pass.
	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "example.com",
		Host:      "example.com:443",
		Header:    http.Header{"X-Trace-Id": []string{"abc"}},
	})
	if err != nil {
		t.Fatalf("ordinary header rejected: %v", err)
	}
	if got := decision.Authority(); got != "example.com:443" {
		t.Fatalf("Authority = %q", got)
	}
}

func TestSchemeAndAuthorityAllowList(t *testing.T) {
	authz, err := New([]Entry{
		{Scheme: "https", Authority: "example.com"},
		{Scheme: "http", Authority: "example.com:8080"},
		{Scheme: "https", Authority: "[2001:db8::1]:8443"},
	}, WithResolver(&staticResolver{hosts: map[string][]netip.Addr{
		"example.com": {mustAddr(t, "198.51.100.20")},
	}}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	notAllowed := []CheckInput{
		{Scheme: "http", Authority: "example.com"},         // wrong scheme/port
		{Scheme: "https", Authority: "example.com:8443"},   // wrong port
		{Scheme: "https", Authority: "sub.example.com"},    // suffix must not match
		{Scheme: "https", Authority: "notexample.com"},     // unrelated host
		{Scheme: "http", Authority: "example.com:8081"},    // wrong port
		{Scheme: "https", Authority: "[2001:db8::2]:8443"}, // different v6 host
		{Scheme: "https", Authority: "[2001:db8::1]"},      // default 443 != 8443
	}
	for _, in := range notAllowed {
		if _, err := authz.Check(context.Background(), in); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Check(%s://%s) error = %v, want ErrNotAllowed", in.Scheme, in.Authority, err)
		}
	}

	allowed := CheckInput{Scheme: "HTTPS", Authority: "EXAMPLE.COM:443"}
	decision, err := authz.Check(context.Background(), allowed)
	if err != nil {
		t.Fatalf("exact match rejected: %v", err)
	}
	if decision.authority.mapKey() != (key{scheme: "https", host: "example.com", port: 443}) {
		t.Fatalf("unexpected map key: %+v", decision.authority.mapKey())
	}

	// Exact IPv6 literal entry is allowed and needs no resolver.
	v6, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "[2001:db8::1]:8443",
	})
	if err != nil {
		t.Fatalf("ipv6 entry Check: %v", err)
	}
	if got := v6.Authority(); got != "[2001:db8::1]:8443" {
		t.Fatalf("ipv6 Authority = %q", got)
	}
	if got := v6.authority.String(); got != "[2001:db8::1]:8443" {
		t.Fatalf("ipv6 canonical authority = %q", got)
	}

	// Blocked literal entries are refused at registration.
	for _, entry := range []Entry{
		{Scheme: "http", Authority: "127.0.0.1"},
		{Scheme: "http", Authority: "[::1]:8080"},
		{Scheme: "http", Authority: "10.1.2.3:9000"},
		{Scheme: "http", Authority: "[fe80::7]"},
	} {
		if _, err := New([]Entry{entry}); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("New(%v) error = %v, want ErrBlockedAddress", entry, err)
		}
	}
}

func TestAddressFamilyFiltering(t *testing.T) {
	decision := &Decision{
		authority: authority{scheme: "https", host: "dual.example", port: 443},
		addrs: []netip.Addr{
			mustAddr(t, "198.51.100.4"),
			mustAddr(t, "2001:db8::4"),
		},
		baseDial: (&dialRecorder{}).dial,
	}

	recorder := &dialRecorder{}
	decision.baseDial = recorder.dial

	if _, err := decision.DialContext(context.Background(), "tcp4"); err == nil {
		t.Fatal("tcp4 dial unexpectedly succeeded")
	}
	if got := recorder.snapshot(); len(got) != 1 || got[0] != "198.51.100.4:443" {
		t.Fatalf("tcp4 dialed = %v, want [198.51.100.4:443]", got)
	}

	recorder2 := &dialRecorder{}
	decision.baseDial = recorder2.dial
	if _, err := decision.DialContext(context.Background(), "tcp6"); err == nil {
		t.Fatal("tcp6 dial unexpectedly succeeded")
	}
	if got := recorder2.snapshot(); len(got) != 1 || got[0] != "[2001:db8::4]:443" {
		t.Fatalf("tcp6 dialed = %v, want [[2001:db8::4]:443]", got)
	}
}

func TestLegalHTTPS443RequestPinnedAndVerified(t *testing.T) {
	mux := http.NewServeMux()
	var seenHost string
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		w.Header().Set("X-Connection", "ok")
		_, _ = w.Write([]byte("hello"))
	})
	listener := newPipeListener()
	defer listener.Close()

	// The allow list names a public-looking host; the fake resolver pins it
	// to a documentation IP, while the test dial seam forwards to the local
	// in-memory TLS server without re-resolving the name.
	const allowedHost = "example.com"
	resolver := &staticResolver{hosts: map[string][]netip.Addr{
		allowedHost: {mustAddr(t, "198.51.100.77")},
	}}
	authz, err := New([]Entry{{Scheme: "https", Authority: allowedHost}}, WithResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	roots := startTLSPipeServer(t, listener, allowedHost, mux)

	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: allowedHost, // no port: must default to 443
		Host:      allowedHost + ":443",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	recorder := &dialRecorder{}
	decision.baseDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		recorder.record(address)
		return listener.Dial(ctx, network, address)
	}

	req, err := http.NewRequest(http.MethodGet, "https://"+allowedHost+"/ok", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	decision.Apply(req)

	transport := decision.Transport()
	transport.TLSClientConfig.RootCAs = roots

	client := decision.Client()
	client.Transport = transport

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}

	// The dialer must have received the validated IP literal on port 443,
	// never the host name and never a second lookup result.
	if got := recorder.snapshot(); len(got) != 1 || got[0] != "198.51.100.77:443" {
		t.Fatalf("dialed = %v, want [198.51.100.77:443]", got)
	}
	if seenHost != "example.com:443" {
		t.Fatalf("server saw Host = %q, want example.com:443", seenHost)
	}
	if transport.TLSClientConfig.ServerName != allowedHost {
		t.Fatalf("TLS ServerName = %q, want %q", transport.TLSClientConfig.ServerName, allowedHost)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var followHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, httptest.NewRequest(http.MethodGet, "/start", nil), "/landed", http.StatusFound)
	})
	mux.HandleFunc("/landed", func(http.ResponseWriter, *http.Request) {
		followHits++
	})
	listener := newPipeListener()
	defer listener.Close()
	roots := startTLSPipeServer(t, listener, "redirect.example.com", mux)

	resolver := &staticResolver{hosts: map[string][]netip.Addr{
		"redirect.example.com": {mustAddr(t, "198.51.100.88")},
	}}
	authz, err := New([]Entry{{Scheme: "https", Authority: "redirect.example.com"}}, WithResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	decision, err := authz.Check(context.Background(), CheckInput{
		Scheme:    "https",
		Authority: "redirect.example.com",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	decision.baseDial = listener.Dial

	transport := decision.Transport()
	transport.TLSClientConfig.RootCAs = roots

	req, err := http.NewRequest(http.MethodGet, "https://redirect.example.com/start", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	decision.Apply(req)

	client := decision.Client()
	client.Transport = transport

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 returned to caller untouched", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/landed" {
		t.Fatalf("Location = %q, want /landed", loc)
	}
	if followHits != 0 {
		t.Fatalf("redirect target was hit %d times; host must not follow redirects", followHits)
	}
}

func TestInputFromRequestAndTransportDefaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com:9443/v1/x", nil)
	req.Host = "api.example.com:9443"
	in := InputFromRequest(req)
	if in.Scheme != "https" || in.Authority != "api.example.com:9443" || in.Host != "api.example.com:9443" {
		t.Fatalf("InputFromRequest = %+v", in)
	}

	decision := &Decision{
		authority: authority{scheme: "http", host: "api.example.com", port: 8080},
		addrs:     []netip.Addr{mustAddr(t, "198.51.100.9")},
		baseDial:  (&dialRecorder{}).dial,
	}
	tr := decision.Transport()
	if tr.Proxy != nil {
		t.Fatal("Transport.Proxy must be nil so environment proxies cannot bypass the allow list")
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.ServerName != "" {
		t.Fatalf("plain http must not set a TLS ServerName, got %q", tr.TLSClientConfig.ServerName)
	}

	httpsDecision := &Decision{
		authority: authority{scheme: "https", host: "api.example.com", port: 443},
		addrs:     []netip.Addr{mustAddr(t, "198.51.100.9")},
		baseDial:  (&dialRecorder{}).dial,
	}
	httpsTransport := httpsDecision.Transport()
	if httpsTransport.TLSClientConfig == nil ||
		httpsTransport.TLSClientConfig.ServerName != "api.example.com" ||
		httpsTransport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("https Transport TLS config = %+v", httpsTransport.TLSClientConfig)
	}

	// The pinned dialer must be wired into the cloned transport.
	if httpsTransport.DialContext == nil {
		t.Fatal("https Transport must use the pinned DialContext")
	}
}
