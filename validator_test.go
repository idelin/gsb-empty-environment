package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testValidator(t *testing.T, cfg Config) *Validator {
	t.Helper()
	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

// testNetwork is an in-memory TCP network. It lets the tests exercise the
// real net/http transport and dial path in sandboxes that forbid socket
// binding, while still recording the exact address handed to the dialer.
type testNetwork struct {
	mu        sync.Mutex
	nextPort  int
	listeners map[string]*testListener
	dialed    []string
}

type testListener struct {
	address string
	conns   chan net.Conn
	closed  chan struct{}
	once    sync.Once
}

type testAddr string

func (a testAddr) Network() string { return "tcp" }
func (a testAddr) String() string  { return string(a) }

func newTestNetwork() *testNetwork {
	return &testNetwork{nextPort: 42000, listeners: make(map[string]*testListener)}
}

func (n *testNetwork) serverAt(t *testing.T, ip string, handler http.Handler, useTLS bool) (*http.Server, int) {
	t.Helper()
	n.mu.Lock()
	n.nextPort++
	port := n.nextPort
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	ln := &testListener{
		address: address,
		conns:   make(chan net.Conn, 16),
		closed:  make(chan struct{}),
	}
	n.listeners[address] = ln
	n.mu.Unlock()

	srv := &http.Server{Handler: handler}
	var serveLn net.Listener = ln
	if useTLS {
		serveLn = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{testCertificate(t)}})
	}
	go func() {
		_ = srv.Serve(serveLn)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	return srv, port
}

func (n *testNetwork) Server(t *testing.T, handler http.Handler) (*http.Server, int) {
	t.Helper()
	return n.serverAt(t, "127.0.0.1", handler, false)
}

func (n *testNetwork) TLSServer(t *testing.T, handler http.Handler) (*http.Server, int) {
	t.Helper()
	return n.serverAt(t, "127.0.0.1", handler, true)
}

func (n *testNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	n.mu.Lock()
	n.dialed = append(n.dialed, address)
	ln := n.listeners[address]
	n.mu.Unlock()
	if ln == nil {
		return nil, &net.OpError{Op: "dial", Net: network, Addr: testAddr(address), Err: errors.New("connection refused")}
	}
	client, server := net.Pipe()
	select {
	case ln.conns <- server:
		return client, nil
	case <-ln.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func (n *testNetwork) Dialed() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.dialed...)
}

func (l *testListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *testListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *testListener) Addr() net.Addr { return testAddr(l.address) }

func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "allowed.test"},
		DNSNames:     []string{"allowed.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func publicLookup(addr string) LookupIPFunc {
	ip := netip.MustParseAddr(addr)
	return func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{ip}, nil
	}
}

func TestRejectsUserinfo(t *testing.T) {
	v := testValidator(t, Config{})
	for _, raw := range []string{
		"http://allowed@169.254.169.254/",
		"http://allowed:secret@169.254.169.254/",
		"http://user:@example.com/",
		"http://@example.com/",
		"http://169.254.169.254:80@allowed/",
		"http://example.com%2f@127.0.0.1/",
	} {
		_, err := v.Validate(raw)
		if !errors.Is(err, ErrUserInfo) {
			t.Errorf("Validate(%q) error = %v, want ErrUserInfo", raw, err)
		}
	}
	if _, err := v.Client().Get("http://allowed@169.254.169.254/"); !errors.Is(err, ErrUserInfo) {
		t.Errorf("Client.Get userinfo error = %v, want ErrUserInfo", err)
	}
}

func TestRejectsNumericAddressForms(t *testing.T) {
	v := testValidator(t, Config{})
	tests := []struct {
		raw  string
		want error
	}{
		{"http://2130706433/", ErrLoopbackAddress},
		{"http://0x7f000001/", ErrLoopbackAddress},
		{"http://017700000001/", ErrLoopbackAddress},
		{"http://0177.0.0.1/", ErrLoopbackAddress},
		{"http://127.1/", ErrLoopbackAddress},
		{"http://0x7f.1/", ErrLoopbackAddress},
		{"http://127.0.0.1./", ErrLoopbackAddress},
		{"http://１２７.０.０.１/", ErrLoopbackAddress},
		{"http://１２７。０。０。１/", ErrLoopbackAddress},
		{"http://[::ffff:127.0.0.1]/", ErrLoopbackAddress},
		{"http://[::ffff:7f00:1]/", ErrLoopbackAddress},
		{"http://169.254.169.254/", ErrLinkLocalAddress},
		{"http://0xa9fea9fe/", ErrLinkLocalAddress},
		{"http://[::ffff:a9fe:a9fe]/", ErrLinkLocalAddress},
		{"http://10.0.0.1/", ErrPrivateAddress},
		{"http://100.64.0.1/", ErrPrivateAddress},
		{"http://[fc00::1]/", ErrPrivateAddress},
		{"http://[fe80::1]/", ErrLinkLocalAddress},
		{"http://[::1]/", ErrLoopbackAddress},
		{"http://1.2.3.4.5/", ErrHost},
		{"http://09.0.0.1/", ErrHost},
		{"http://999/", ErrBlockedAddress},
	}
	for _, tt := range tests {
		_, err := v.Validate(tt.raw)
		if !errors.Is(err, tt.want) {
			t.Errorf("Validate(%q) error = %v, want %v", tt.raw, err, tt.want)
		}
	}
}

func TestRejectsEncodingDifferentials(t *testing.T) {
	v := testValidator(t, Config{})
	tests := []struct {
		raw  string
		want error
	}{
		{"http://%31%36%39.254.169.254/", ErrInvalidURL},
		{"http://%252f%252f169.254.169.254/", ErrHost},
		{"http://%2f%2f169.254.169.254/", ErrInvalidURL},
		{"http://%2531%2532%2537.0.0.1/", ErrHost},
		{"http://example.com%2f@127.0.0.1/", ErrUserInfo},
		{"http://example.com\\@127.0.0.1/", ErrInvalidURL},
		{" http://169.254.169.254/", ErrInvalidURL},
		{"http://169.254.169.254/\n", ErrInvalidURL},
	}
	for _, tt := range tests {
		_, err := v.Validate(tt.raw)
		if !errors.Is(err, tt.want) {
			t.Errorf("Validate(%q) error = %v, want %v", tt.raw, err, tt.want)
		}
	}
}

func TestRejectsFragments(t *testing.T) {
	v := testValidator(t, Config{})
	for _, raw := range []string{
		"http://example.com/#",
		"http://example.com/#fragment",
		"http://example.com/?x=1#fragment",
	} {
		if _, err := v.Validate(raw); !errors.Is(err, ErrFragment) {
			t.Errorf("Validate(%q) error = %v, want ErrFragment", raw, err)
		}
	}
}

func TestRejectsSchemesAndOpaqueURLs(t *testing.T) {
	v := testValidator(t, Config{})
	tests := []struct {
		raw  string
		want error
	}{
		{"ftp://example.com/", ErrScheme},
		{"file:///etc/passwd", ErrScheme},
		{"gopher://127.0.0.1/", ErrScheme},
		{"ws://example.com/", ErrScheme},
		{"http:169.254.169.254", ErrInvalidURL},
		{"//169.254.169.254/", ErrScheme},
	}
	for _, tt := range tests {
		if _, err := v.Validate(tt.raw); !errors.Is(err, tt.want) {
			t.Errorf("Validate(%q) error = %v, want %v", tt.raw, err, tt.want)
		}
	}
}

func TestPortPolicy(t *testing.T) {
	v := testValidator(t, Config{LookupIP: publicLookup("93.184.216.34")})
	tests := []struct {
		raw  string
		want error
	}{
		{"http://example.com:8080/", ErrPort},
		{"https://example.com:80/", ErrPort},
		{"http://example.com:0/", ErrPort},
		{"http://example.com:65536/", ErrPort},
		{"http://example.com:/", ErrHost},
	}
	for _, tt := range tests {
		if _, err := v.Validate(tt.raw); !errors.Is(err, tt.want) {
			t.Errorf("Validate(%q) error = %v, want %v", tt.raw, err, tt.want)
		}
	}
	if target, err := v.Validate("http://example.com:080/"); err != nil || target.Port != 80 {
		t.Fatalf("standard port with leading zero: target=%v err=%v", target, err)
	}

	withPort := testValidator(t, Config{
		LookupIP:     publicLookup("93.184.216.34"),
		AllowedPorts: []int{8080},
	})
	target, err := withPort.Validate("http://example.com:8080/")
	if err != nil {
		t.Fatalf("allowed port rejected: %v", err)
	}
	if target.Port != 8080 {
		t.Fatalf("port = %d, want 8080", target.Port)
	}

	anyPort := testValidator(t, Config{
		LookupIP:              publicLookup("93.184.216.34"),
		AllowNonStandardPorts: true,
	})
	if _, err := anyPort.Validate("https://example.com:9443/"); err != nil {
		t.Fatalf("AllowNonStandardPorts rejected: %v", err)
	}
}

func TestAllowedHostWithPortAlsoAllowsPort(t *testing.T) {
	v := testValidator(t, Config{
		LookupIP:     publicLookup("203.0.113.10"),
		AllowedHosts: []string{"allowed.test:8443"},
	})
	target, err := v.Validate("http://allowed.test:8443/")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if target.Port != 8443 || target.Host != "allowed.test" {
		t.Fatalf("target = %+v", target)
	}
}

func TestAnyDisallowedResolutionRejectsWholeTarget(t *testing.T) {
	v := testValidator(t, Config{
		LookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("10.0.0.1"),
			}, nil
		},
	})
	if _, err := v.Validate("http://mixed.test/"); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("Validate error = %v, want ErrPrivateAddress", err)
	}
}

func TestIDNANormalization(t *testing.T) {
	var mu sync.Mutex
	var lookedUp []string
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		lookedUp = append(lookedUp, host)
		mu.Unlock()
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	v := testValidator(t, Config{LookupIP: lookup})
	tests := []struct {
		raw  string
		want string
	}{
		{"http://bücher.example/", "xn--bcher-kva.example"},
		{"http://ｅｘａｍｐｌｅ.com/", "example.com"},
		{"http://example.com./", "example.com"},
	}
	for _, tt := range tests {
		target, err := v.Validate(tt.raw)
		if err != nil {
			t.Errorf("Validate(%q): %v", tt.raw, err)
			continue
		}
		if target.Host != tt.want {
			t.Errorf("Validate(%q) host = %q, want %q", tt.raw, target.Host, tt.want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, host := range lookedUp {
		if strings.ContainsFunc(host, func(r rune) bool { return r > 127 }) {
			t.Errorf("resolver received non-ASCII host %q", host)
		}
	}
}

func TestAllowedHostCanUseLoopback(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	v := testValidator(t, Config{
		LookupIP:     publicLookup("127.0.0.1"),
		AllowedHosts: []string{"localhost"},
		DialContext:  vn.DialContext,
	})
	req, target, err := v.NewRequest(context.Background(), http.MethodGet, "http://localhost:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
	if target.Host != "localhost" {
		t.Errorf("target host = %q", target.Host)
	}
}

func TestConnectionUsesPinnedValidatedAddress(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host)
	}))

	v := testValidator(t, Config{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host != "allowed.test" {
				return nil, errors.New("unexpected host")
			}
			return []netip.Addr{
				netip.MustParseAddr("127.0.0.1"),
				netip.MustParseAddr("127.0.0.2"),
			}, nil
		},
		AllowedCIDRs: []string{"127.0.0.0/8"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})

	raw := "http://allowed.test:" + strconv.Itoa(port) + "/"
	req, target, err := v.NewRequest(context.Background(), http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	wantHost := "allowed.test:" + strconv.Itoa(port)
	if string(body) != wantHost {
		t.Errorf("server saw Host %q, want %q", body, wantHost)
	}
	dialed := vn.Dialed()
	if len(dialed) != 1 {
		t.Fatalf("dialed addresses = %v, want one", dialed)
	}
	if dialed[0] != net.JoinHostPort(target.IP.String(), strconv.Itoa(port)) {
		t.Errorf("dialed %q, validated primary %q", dialed[0], target.Address())
	}
	if !target.AllowsAddress(netip.MustParseAddr("127.0.0.1")) ||
		!target.AllowsAddress(netip.MustParseAddr("127.0.0.2")) {
		t.Errorf("validated address set = %v", target.IPs)
	}
}

func TestHTTPSConnectionUsesPinnedAddress(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.TLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host)
	}))

	v := testValidator(t, Config{
		LookupIP:        publicLookup("127.0.0.1"),
		AllowedCIDRs:    []string{"127.0.0.0/8"},
		AllowedPorts:    []int{port},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // local test certificate
		DialContext:     vn.DialContext,
	})
	raw := "https://allowed.test:" + strconv.Itoa(port) + "/"
	req, target, err := v.NewRequest(context.Background(), http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	dialed := vn.Dialed()
	if len(dialed) != 1 || dialed[0] != target.Address() {
		t.Errorf("dialed %v, validated %q", dialed, target.Address())
	}
}

func TestResolvesOnlyOncePerRequest(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	var lookups atomic.Int32
	v := testValidator(t, Config{
		LookupIP: func(_ context.Context, _ string) ([]netip.Addr, error) {
			n := lookups.Add(1)
			if n == 1 {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		},
		AllowedCIDRs: []string{"127.0.0.1/32"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	raw := "http://changing.test:" + strconv.Itoa(port) + "/"
	req, target, err := v.NewRequest(context.Background(), http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if got := lookups.Load(); got != 1 {
		t.Errorf("lookups = %d, want 1", got)
	}
	if target.IP.String() != "127.0.0.1" {
		t.Errorf("target IP = %s", target.IP)
	}
}

func TestRedirectToUserinfoIsRejected(t *testing.T) {
	var hits atomic.Int32
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://allowed@169.254.169.254/", http.StatusFound)
	}))
	v := testValidator(t, Config{
		LookupIP:     publicLookup("127.0.0.1"),
		AllowedCIDRs: []string{"127.0.0.0/8"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	req, _, err := v.NewRequest(context.Background(), http.MethodGet, "http://redirect.test:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = v.Client().Do(req)
	if !errors.Is(err, ErrUserInfo) {
		t.Fatalf("Do error = %v, want ErrUserInfo", err)
	}
	if hits.Load() != 1 {
		t.Errorf("redirect target was contacted: hits = %d", hits.Load())
	}
}

func TestRedirectToPrivateNumericAddressIsRejected(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://2130706433/", http.StatusFound)
	}))

	v := testValidator(t, Config{
		LookupIP:     publicLookup("127.0.0.1"),
		AllowedHosts: []string{"start.test"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	req, _, err := v.NewRequest(context.Background(), http.MethodGet, "http://start.test:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = v.Client().Do(req)
	if !errors.Is(err, ErrLoopbackAddress) {
		t.Fatalf("Do error = %v, want ErrLoopbackAddress", err)
	}
	if len(vn.Dialed()) != 1 {
		t.Errorf("private redirect target was dialed: %v", vn.Dialed())
	}
}

func TestRedirectDNSChangeIsRevalidated(t *testing.T) {
	var finalHits atomic.Int32
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			finalHits.Add(1)
			_, _ = io.WriteString(w, "final")
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))

	var lookups atomic.Int32
	v := testValidator(t, Config{
		LookupIP: func(_ context.Context, _ string) ([]netip.Addr, error) {
			if lookups.Add(1) == 1 {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		},
		AllowedCIDRs: []string{"127.0.0.1/32"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	req, _, err := v.NewRequest(context.Background(), http.MethodGet, "http://rebind.test:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = v.Client().Do(req)
	if !errors.Is(err, ErrLinkLocalAddress) {
		t.Fatalf("Do error = %v, want ErrLinkLocalAddress", err)
	}
	if finalHits.Load() != 0 {
		t.Errorf("redirect final target was contacted")
	}
	if lookups.Load() < 2 {
		t.Errorf("lookups = %d, redirect hop was not resolved again", lookups.Load())
	}
}

func TestRedirectToAllowedHopWorks(t *testing.T) {
	var finalHits atomic.Int32
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			finalHits.Add(1)
			_, _ = io.WriteString(w, "final")
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))

	v := testValidator(t, Config{
		LookupIP:     publicLookup("127.0.0.1"),
		AllowedCIDRs: []string{"127.0.0.1/32"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	req, _, err := v.NewRequest(context.Background(), http.MethodGet, "http://allowed.test:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "final" || finalHits.Load() != 1 {
		t.Errorf("body=%q finalHits=%d", body, finalHits.Load())
	}
}

func TestTransportValidatesPlainHTTPRequest(t *testing.T) {
	vn := newTestNetwork()
	_, port := vn.Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	v := testValidator(t, Config{
		LookupIP:     publicLookup("127.0.0.1"),
		AllowedCIDRs: []string{"127.0.0.0/8"},
		AllowedPorts: []int{port},
		DialContext:  vn.DialContext,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://plain.test:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := v.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestCheckRedirectPolicy(t *testing.T) {
	v := testValidator(t, Config{LookupIP: publicLookup("93.184.216.34")})
	tests := []struct {
		raw  string
		want error
	}{
		{"http://allowed@169.254.169.254/", ErrUserInfo},
		{"http://example.com/#x", ErrFragment},
		{"ftp://example.com/", ErrScheme},
		{"http://example.com:8080/", ErrPort},
	}
	for _, tt := range tests {
		req, err := http.NewRequest(http.MethodGet, tt.raw, nil)
		if err != nil {
			continue
		}
		if err := v.CheckRedirect(req, nil); !errors.Is(err, tt.want) {
			t.Errorf("CheckRedirect(%q) error = %v, want %v", tt.raw, err, tt.want)
		}
	}
}

func TestNumericFormsNormalize(t *testing.T) {
	tests := map[string]string{
		"2130706433":   "127.0.0.1",
		"0x7f000001":   "127.0.0.1",
		"017700000001": "127.0.0.1",
		"0177.0.0.1":   "127.0.0.1",
		"127.1":        "127.0.0.1",
		"0x7f.1":       "127.0.0.1",
		"127.0.0.1.":   "127.0.0.1",
		"１２７.０.０.１":    "127.0.0.1",
		"127.0.0.1":    "127.0.0.1",
	}
	for input, want := range tests {
		got, err := NormalizeHost(input)
		if err != nil {
			t.Errorf("NormalizeHost(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", input, got, want)
		}
	}
}
