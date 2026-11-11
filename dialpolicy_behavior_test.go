package dialpolicy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	goodIP    = "203.0.113.10"
	bannedIP  = "203.0.113.20"
	goodIP2   = "198.51.100.7"
	testHost  = "rebind.example.org"
	testPort  = "8080"
	httpsPort = "8443"
)

type atomicValue struct{ v atomic.Value }

func (a *atomicValue) store(s string) { a.v.Store(s) }
func (a *atomicValue) load() string {
	if v := a.v.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func isCertVerifyError(err error) bool {
	var verifyErr x509.UnknownAuthorityError
	if errors.As(err, &verifyErr) {
		return true
	}
	var hostErr x509.HostnameError
	return errors.As(err, &hostErr)
}

func isClosedConnErr(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

func waitForAccepted(t *testing.T, counter *countingListener, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if counter.accepted.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d accepts, got %d", want, counter.accepted.Load())
}

func TestMultiAnswerOnlyVerifiedDialed(t *testing.T) {
	dialer := newFakeDialer()
	listener := dialer.register(goodIP)
	counter := &countingListener{Listener: listener}
	serve(t, counter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "peer=%s host=%s", r.RemoteAddr, r.Host)
	}), nil)

	resolver := newScriptResolver()
	resolver.set(testHost, []netip.Addr{MustAddr(bannedIP), MustAddr(goodIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)

	resp, body := get(t, guard.Client(), "http://"+testHost+":"+testPort+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "host="+testHost+":"+testPort) {
		t.Fatalf("Host header not preserved: %q", body)
	}
	attempted := dialer.attempted()
	if len(attempted) != 1 || attempted[0] != goodIP {
		t.Fatalf("only %s should be dialed, attempted=%v", goodIP, attempted)
	}
	if counter.accepted.Load() != 1 {
		t.Fatalf("expected 1 accepted connection, got %d", counter.accepted.Load())
	}
}

func TestAllAnswersForbidden(t *testing.T) {
	dialer := newFakeDialer()
	dialer.register(goodIP)
	resolver := newScriptResolver()
	resolver.set(testHost, []netip.Addr{MustAddr(bannedIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)

	_, err := guard.Client().Get("http://" + testHost + ":" + testPort + "/")
	expectForbidden(t, err)
	if len(dialer.attempted()) != 0 {
		t.Fatalf("no dial should occur, attempted=%v", dialer.attempted())
	}
}

// TestBannedAnswerNeverFallbackDialed: when the first answer is banned and no
// listener exists for it, the guard must skip it entirely and succeed on the
// next verified answer, proving rejected addresses are never dialed even as
// fallbacks.
func TestBannedAnswerNeverFallbackDialed(t *testing.T) {
	dialer := newFakeDialer()
	listener := dialer.register(goodIP)
	serve(t, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}), nil)

	resolver := newScriptResolver()
	resolver.set(testHost, []netip.Addr{MustAddr(bannedIP), MustAddr(goodIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)

	_, body := get(t, guard.Client(), "http://"+testHost+":"+testPort+"/")
	if body != "ok" {
		t.Fatalf("unexpected body %q", body)
	}
	for _, attempted := range dialer.attempted() {
		if attempted == bannedIP {
			t.Fatal("banned address was dialed as a fallback")
		}
	}
}

func TestIPLiteralEnforced(t *testing.T) {
	dialer := newFakeDialer()
	dialer.register(bannedIP)
	guard := newGuard(t, allowOnly(goodIP), newScriptResolver(), dialer)

	_, err := guard.Client().Get("http://" + bannedIP + ":" + testPort + "/")
	expectForbidden(t, err)
	if len(dialer.attempted()) != 0 {
		t.Fatalf("banned literal was dialed: %v", dialer.attempted())
	}
}

func TestTLSPreservesHostname(t *testing.T) {
	dialer := newFakeDialer()
	listener := dialer.register(goodIP)
	var sawServerName atomicValue
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			sawServerName.store(r.TLS.ServerName)
		}
		fmt.Fprint(w, "ok")
	})
	cert, pool := selfSignedCert(t, "example.com", "*.example.com")
	serve(t, listener, handler, &cert)

	resolver := newScriptResolver()
	resolver.set("example.com", []netip.Addr{MustAddr(goodIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)
	guard.tlsConfig = &tls.Config{RootCAs: pool}

	resp, body := get(t, guard.Client(), "https://example.com:"+httpsPort+"/")
	if resp.StatusCode != http.StatusOK || body != "ok" {
		t.Fatalf("unexpected response %d %q", resp.StatusCode, body)
	}
	if got := sawServerName.load(); got != "example.com" {
		t.Fatalf("TLS ServerName = %q, want example.com", got)
	}
	if got := dialer.attempted(); len(got) != 1 || got[0] != goodIP {
		t.Fatalf("dial should target verified IP, got %v", got)
	}

	resolver.set("not-a-san.example.net", []netip.Addr{MustAddr(goodIP)})
	client := guard.Client()
	client.Timeout = 2 * time.Second
	_, err := client.Get("https://not-a-san.example.net:" + httpsPort + "/")
	if err == nil {
		t.Fatal("expected TLS failure for hostname absent from certificate")
	}
	if isCertVerifyError(err) {
		return
	}
	if !strings.Contains(err.Error(), "TLS") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected TLS verification failure, got %v", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	dialer := newFakeDialer()
	goodListener := dialer.register(goodIP)
	bannedListener := dialer.register(bannedIP)

	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+bannedIP+":"+testPort+"/inside", http.StatusFound)
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/bounce", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+testHost+":"+testPort+"/ok", http.StatusFound)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "landed")
	})
	serve(t, goodListener, mux, nil)
	serve(t, bannedListener, mux, nil)

	resolver := newScriptResolver()
	resolver.set(testHost, []netip.Addr{MustAddr(goodIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)

	_, err := guard.Client().Get("http://" + testHost + ":" + testPort + "/start")
	expectForbidden(t, err)

	_, err = guard.Client().Get("http://" + testHost + ":" + testPort + "/file")
	expectForbidden(t, err)

	resp, body := get(t, guard.Client(), "http://"+testHost+":"+testPort+"/bounce")
	if resp.StatusCode != http.StatusOK || body != "landed" {
		t.Fatalf("safe redirect failed: %d %q", resp.StatusCode, body)
	}
	for _, attempted := range dialer.attempted() {
		if attempted != goodIP {
			t.Fatalf("forbidden peer %s was dialed", attempted)
		}
	}
}

func TestRebindAndReuse(t *testing.T) {
	dialer := newFakeDialer()
	listener := dialer.register(goodIP)
	counter := &countingListener{Listener: listener}
	serve(t, counter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "peer=%s", r.RemoteAddr)
	}), nil)

	resolver := newScriptResolver()
	resolver.set(testHost,
		[]netip.Addr{MustAddr(goodIP)},
		[]netip.Addr{MustAddr(bannedIP)},
	)
	guard := newGuard(t, allowOnly(goodIP, goodIP2), resolver, dialer)
	client := guard.Client()

	_, body1 := get(t, client, "http://"+testHost+":"+testPort+"/one")
	if !strings.Contains(body1, goodIP) {
		t.Fatalf("first request peer wrong: %q", body1)
	}
	if dialer.dialCount.Load() != 1 {
		t.Fatalf("first request should dial once, got %d", dialer.dialCount.Load())
	}

	_, body2 := get(t, client, "http://"+testHost+":"+testPort+"/two")
	if !strings.Contains(body2, goodIP) {
		t.Fatalf("reused connection peer wrong: %q", body2)
	}
	if counter.accepted.Load() != 1 {
		t.Fatalf("connection was not reused: accepts=%d", counter.accepted.Load())
	}
	for _, attempted := range dialer.attempted() {
		if attempted == bannedIP {
			t.Fatal("banned rebind target was dialed")
		}
	}

	guard.Transport().CloseIdleConnections()
	_, err := client.Get("http://" + testHost + ":" + testPort + "/three")
	expectForbidden(t, err)
}

func TestHotSwapSeversInFlightAndPooled(t *testing.T) {
	dialer := newFakeDialer()
	listener := dialer.register(goodIP)
	counter := &countingListener{Listener: listener}

	block := make(chan struct{})
	requestReceived := make(chan struct{}, 1)
	serve(t, counter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slow") {
			select {
			case requestReceived <- struct{}{}:
			default:
			}
			<-block
		}
		fmt.Fprint(w, "done")
	}), nil)

	resolver := newScriptResolver()
	resolver.set(testHost, []netip.Addr{MustAddr(goodIP)})
	guard := newGuard(t, allowOnly(goodIP), resolver, dialer)
	client := guard.Client()

	_, body := get(t, client, "http://"+testHost+":"+testPort+"/warm")
	if body != "done" {
		t.Fatalf("warmup failed: %q", body)
	}

	guard.UpdatePolicy(allowOnly(goodIP2))
	acceptsAfterSwap := counter.accepted.Load()
	_, err := client.Get("http://" + testHost + ":" + testPort + "/again")
	expectForbidden(t, err)
	if counter.accepted.Load() != acceptsAfterSwap {
		t.Fatal("new connection accepted after policy swap")
	}

	guard.UpdatePolicy(allowOnly(goodIP))
	errCh := make(chan error, 1)
	go func() {
		_, e := client.Get("http://" + testHost + ":" + testPort + "/slow")
		errCh <- e
	}()
	waitForAccepted(t, counter, acceptsAfterSwap+1)
	select {
	case <-requestReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the in-flight request")
	}
	guard.UpdatePolicy(allowOnly(goodIP2))
	close(block)
	err = <-errCh
	if err == nil {
		t.Fatal("in-flight request to newly forbidden peer should fail")
	}
	if !errors.Is(err, ErrForbidden) && !errors.Is(err, io.EOF) && !isClosedConnErr(err) {
		t.Fatalf("expected severed connection error, got %v", err)
	}
}

func TestPrefixPolicy(t *testing.T) {
	policy := PrefixPolicy{
		AllowPrefixes: []netip.Prefix{MustPrefix("10.0.0.0/8")},
		DenyPrefixes:  []netip.Prefix{MustPrefix("10.1.0.0/16")},
	}
	cases := []struct {
		ip    string
		allow bool
	}{
		{"10.0.1.1", true},
		{"10.1.2.3", false},
		{"8.8.8.8", false},
		{"127.0.0.1", false},
		{"::1", false},
	}
	for _, tc := range cases {
		if got := policy.Allow(MustAddr(tc.ip)); got != tc.allow {
			t.Fatalf("Allow(%s) = %v, want %v", tc.ip, got, tc.allow)
		}
	}

	empty := PrefixPolicy{}
	if empty.Allow(MustAddr("1.2.3.4")) {
		t.Fatal("empty policy must deny")
	}

	publicFirst := PrefixPolicy{
		AllowPrefixes: []netip.Prefix{MustPrefix("0.0.0.0/0"), MustPrefix("::/0")},
		DenyPrefixes:  BlockedPrefixes,
	}
	for _, blocked := range []string{"127.0.0.1", "10.0.0.5", "169.254.1.1", "192.168.1.1", "100.64.0.1", "::1", "fc00::1"} {
		if publicFirst.Allow(MustAddr(blocked)) {
			t.Fatalf("blocked address %s was allowed", blocked)
		}
	}
	if !publicFirst.Allow(MustAddr("8.8.8.8")) {
		t.Fatal("ordinary public address should be allowed")
	}
}

func TestControlHookRejectsForbiddenPeer(t *testing.T) {
	guard := New(allowOnly(goodIP))
	if err := guard.control("tcp", net.JoinHostPort(goodIP, "80"), nil); err != nil {
		t.Fatalf("allowed peer rejected: %v", err)
	}
	err := guard.control("tcp", net.JoinHostPort(bannedIP, "80"), nil)
	expectForbidden(t, err)
}
