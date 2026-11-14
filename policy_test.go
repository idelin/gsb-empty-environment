package netsec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testTarget(t *testing.T, server *httptest.Server) Target {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return Target{Scheme: u.Scheme, Host: u.Hostname(), Port: port}
}

func newLocalServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local test sockets are unavailable: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	return server
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type pipeListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	addr      net.Addr
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
		addr:   pipeAddr("127.0.0.1:8080"),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

func startPipeServer(t *testing.T, handler http.Handler) *pipeListener {
	t.Helper()
	listener := newPipeListener()
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	return listener
}

func (l *pipeListener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestMetadataAndLinkLocalBlockedBeforeResolution(t *testing.T) {
	t.Parallel()

	lookupCalled := atomic.Int32{}
	p, err := New(Config{
		AllowedDomains: []string{
			"169.254.169.254",
			"100.100.100.200",
			"192.0.0.192",
			"168.63.129.16",
			"fe80::1",
			"::ffff:169.254.169.254",
			"fd00:ec2::254",
			"::a9fe:a9fe",
			"64:ff9b::a9fe:a9fe",
			"2852039166",
			"0251.0376.0251.0376",
			"0xA9FEA9FE",
		},
		AllowedPorts: []int{80},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			lookupCalled.Add(1)
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	urls := []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://100.100.100.200/latest/meta-data/",
		"http://192.0.0.192/opc/v1/instance/",
		"http://168.63.129.16/",
		"http://[fe80::1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://[fd00:ec2::254]/",
		"http://[::a9fe:a9fe]/",
		"http://[64:ff9b::a9fe:a9fe]/",
		"http://2852039166/",
		"http://0251.0376.0251.0376/",
		"http://0xA9FEA9FE/",
	}
	for _, rawURL := range urls {
		if _, err := p.Do(mustRequest(t, rawURL)); err == nil {
			t.Errorf("%s: expected rejection", rawURL)
		}
	}
	if lookupCalled.Load() != 0 {
		t.Fatal("literal metadata URL triggered DNS lookup")
	}
}

func TestMetadataHostnameBlockedBeforeResolution(t *testing.T) {
	t.Parallel()

	lookupCalled := atomic.Int32{}
	p, err := New(Config{
		AllowedDomains: []string{"metadata.google.internal"},
		AllowedPorts:   []int{80},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			lookupCalled.Add(1)
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateURL("http://metadata.google.internal/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("got %v, want ErrBlockedAddress", err)
	}
	if lookupCalled.Load() != 0 {
		t.Fatal("metadata hostname triggered DNS lookup")
	}
}

func TestDNSAnswerContainingMetadataIsRejected(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		AllowedDomains: []string{"callback.test"},
		AllowedPorts:   []int{80},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{
				net.ParseIP("93.184.216.34"),
				net.ParseIP("169.254.169.254"),
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateURL("http://callback.test/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("got %v, want ErrBlockedAddress", err)
	}
}

func TestCredentialProviderRunsOnlyForAllowedTargets(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p, err := New(Config{
		AllowedDomains: []string{"allowed.test", "169.254.169.254"},
		AllowedPorts:   []int{80},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
		CredentialProvider: func(Target) (http.Header, bool) {
			calls.Add(1)
			return http.Header{"Authorization": {"Bearer must-not-leak"}}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Do(mustRequest(t, "http://169.254.169.254/")); err == nil {
		t.Fatal("metadata target was allowed")
	}
	if _, err := p.Do(mustRequest(t, "http://not-allowed.test/")); err == nil {
		t.Fatal("non-allowlisted target was allowed")
	}
	if calls.Load() != 0 {
		t.Fatalf("credential provider called %d times for rejected targets", calls.Load())
	}
}

func TestRedirectIsNotFollowedAndCredentialsStayOnAllowedTarget(t *testing.T) {
	t.Parallel()

	var metadataHits atomic.Int32
	metadata := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("metadata server received credentials: %q", got)
		}
	}))
	defer metadata.Close()

	redirect := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer callback-secret" {
			t.Errorf("allowed server got Authorization %q", got)
		}
		http.Redirect(w, r, metadata.URL+"/latest/meta-data/", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	target := testTarget(t, redirect)
	p, err := New(Config{
		AllowedDomains: []string{target.Host},
		AllowedPorts:   []int{target.Port},
		BearerToken:    "callback-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := p.Do(mustRequest(t, redirect.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	if metadataHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d times", metadataHits.Load())
	}
}

func TestDNSRebindingUsesPinnedValidatedAddress(t *testing.T) {
	t.Parallel()

	var allowedHits atomic.Int32
	allowed := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer pinned-secret" {
			t.Errorf("allowed server got Authorization %q", got)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer allowed.Close()

	var metadataHits atomic.Int32
	metadata := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("metadata server received credentials: %q", got)
		}
	}))
	defer metadata.Close()

	target := testTarget(t, allowed)
	var lookups atomic.Int32
	p, err := New(Config{
		AllowedDomains: []string{"rebind.test"},
		AllowedPorts:   []int{target.Port},
		AllowLoopback:  true,
		BearerToken:    "pinned-secret",
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			if lookups.Add(1) == 1 {
				return []net.IP{net.ParseIP(target.Host)}, nil
			}
			// A second lookup would hand the connection attempt to metadata.
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := p.Do(mustRequest(t, "http://rebind.test:"+strconv.Itoa(target.Port)+"/callback"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("unexpected response: %d %q", resp.StatusCode, body)
	}
	if lookups.Load() != 1 {
		t.Fatalf("DNS lookup count = %d, want 1 (pinned)", lookups.Load())
	}
	if allowedHits.Load() != 1 || metadataHits.Load() != 0 {
		t.Fatalf("allowed hits = %d, metadata hits = %d", allowedHits.Load(), metadataHits.Load())
	}
}

func TestAllowedOutboundGetsCredentials(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer trusted-secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Callback-Key"); got != "key-123" {
			t.Errorf("X-Callback-Key = %q", got)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	target := testTarget(t, server)
	p, err := New(Config{
		AllowedDomains: []string{target.Host},
		AllowedPorts:   []int{target.Port},
		CredentialProvider: func(got Target) (http.Header, bool) {
			if got != target {
				t.Errorf("provider target = %#v, want %#v", got, target)
				return nil, false
			}
			return http.Header{
				"Authorization":  {"Bearer trusted-secret"},
				"X-Callback-Key": {"key-123"},
			}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := p.Do(mustRequest(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, hits = %d", resp.StatusCode, hits.Load())
	}
}

func TestPolicyHTTPBehaviorWithoutSockets(t *testing.T) {
	t.Run("legal outbound and credentials ignore environment proxy", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://169.254.169.254:80")
		t.Setenv("HTTPS_PROXY", "http://169.254.169.254:80")
		t.Setenv("ALL_PROXY", "http://169.254.169.254:80")

		var hits atomic.Int32
		listener := startPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer in-memory-secret" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = io.WriteString(w, "ok")
		}))

		p, err := New(Config{
			AllowedDomains: []string{"127.0.0.1"},
			AllowedPorts:   []int{8080},
			BearerToken:    "in-memory-secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		var dialed atomic.Value
		p.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed.Store(address)
			return listener.DialContext(ctx, network, address)
		}

		resp, err := p.Do(mustRequest(t, "http://127.0.0.1:8080/"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if body, _ := io.ReadAll(resp.Body); string(body) != "ok" || hits.Load() != 1 {
			t.Fatalf("body = %q, hits = %d", body, hits.Load())
		}
		if got, _ := dialed.Load().(string); got != "127.0.0.1:8080" {
			t.Fatalf("dialed %q, want the direct target instead of the proxy", got)
		}
	})

	t.Run("redirect is returned without replaying credentials", func(t *testing.T) {
		var hits atomic.Int32
		listener := startPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer redirect-secret" {
				t.Errorf("Authorization = %q", got)
			}
			w.Header().Set("Location", "http://169.254.169.254/latest/meta-data/")
			w.WriteHeader(http.StatusFound)
		}))

		p, err := New(Config{
			AllowedDomains: []string{"127.0.0.1"},
			AllowedPorts:   []int{8080},
			BearerToken:    "redirect-secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		p.dial = listener.DialContext

		resp, err := p.Do(mustRequest(t, "http://127.0.0.1:8080/"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound || hits.Load() != 1 {
			t.Fatalf("status = %d, hits = %d", resp.StatusCode, hits.Load())
		}
		if location := resp.Header.Get("Location"); location != "" {
			t.Fatalf("redirect Location was retained: %q", location)
		}
	})

	t.Run("plain client cannot follow a policy redirect", func(t *testing.T) {
		var hits atomic.Int32
		listener := startPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Location", "http://169.254.169.254/latest/meta-data/")
			w.WriteHeader(http.StatusTemporaryRedirect)
		}))

		p, err := New(Config{
			AllowedDomains: []string{"127.0.0.1"},
			AllowedPorts:   []int{8080},
			BearerToken:    "redirect-secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		p.dial = listener.DialContext

		client := &http.Client{Transport: p}
		resp, err := client.Get("http://127.0.0.1:8080/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTemporaryRedirect || hits.Load() != 1 {
			t.Fatalf("status = %d, hits = %d", resp.StatusCode, hits.Load())
		}
	})

	t.Run("DNS rebinding dials only the validated address", func(t *testing.T) {
		var hits atomic.Int32
		listener := startPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = io.WriteString(w, "pinned")
		}))

		var lookups atomic.Int32
		var dialed atomic.Value
		p, err := New(Config{
			AllowedDomains: []string{"rebind.test"},
			AllowedPorts:   []int{8080},
			AllowLoopback:  true,
			LookupIP: func(context.Context, string) ([]net.IP, error) {
				if lookups.Add(1) == 1 {
					return []net.IP{net.ParseIP("127.0.0.1")}, nil
				}
				return []net.IP{net.ParseIP("169.254.169.254")}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		p.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed.Store(address)
			return listener.DialContext(ctx, network, address)
		}

		resp, err := p.Do(mustRequest(t, "http://rebind.test:8080/"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if body, _ := io.ReadAll(resp.Body); string(body) != "pinned" {
			t.Fatalf("body = %q", body)
		}
		if got, _ := dialed.Load().(string); got != "127.0.0.1:8080" {
			t.Fatalf("dialed %q, want 127.0.0.1:8080", got)
		}
		if lookups.Load() != 1 || hits.Load() != 1 {
			t.Fatalf("lookups = %d, hits = %d", lookups.Load(), hits.Load())
		}
	})

	t.Run("proxy headers are removed", func(t *testing.T) {
		proxyAuth := make(chan string, 1)
		listener := startPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxyAuth <- r.Header.Get("Proxy-Authorization")
		}))

		p, err := New(Config{
			AllowedDomains: []string{"127.0.0.1"},
			AllowedPorts:   []int{8080},
			BearerToken:    "target-secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		p.dial = listener.DialContext

		req := mustRequest(t, "http://127.0.0.1:8080/")
		req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
		resp, err := p.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		select {
		case got := <-proxyAuth:
			if got != "" {
				t.Fatalf("Proxy-Authorization reached the server: %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("server did not receive request")
		}
	})

	t.Run("https verifies the allowed host", func(t *testing.T) {
		listener := newPipeListener()
		server := &httptest.Server{
			Listener: listener,
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer tls-secret" {
					t.Errorf("Authorization = %q", got)
				}
				_, _ = io.WriteString(w, "tls-ok")
			})},
		}
		server.StartTLS()
		t.Cleanup(server.Close)

		pool := x509.NewCertPool()
		pool.AddCert(server.Certificate())
		p, err := New(Config{
			AllowedDomains: []string{"127.0.0.1"},
			AllowedPorts:   []int{8080},
			BearerToken:    "tls-secret",
			TLSConfig:      &tls.Config{RootCAs: pool},
		})
		if err != nil {
			t.Fatal(err)
		}
		p.dial = listener.DialContext

		resp, err := p.Do(mustRequest(t, "https://127.0.0.1:8080/"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if body, _ := io.ReadAll(resp.Body); string(body) != "tls-ok" {
			t.Fatalf("body = %q", body)
		}
	})
}

func TestEnvironmentProxyIsIgnored(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("proxy received credentials: %q", got)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	var targetHits atomic.Int32
	targetServer := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer direct-secret" {
			t.Errorf("target Authorization = %q", got)
		}
		_, _ = io.WriteString(w, "direct")
	}))
	defer targetServer.Close()

	target := testTarget(t, targetServer)
	p, err := New(Config{
		AllowedDomains: []string{"proxy-bypass.test"},
		AllowedPorts:   []int{target.Port},
		AllowLoopback:  true,
		BearerToken:    "direct-secret",
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP(target.Host)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := p.Do(mustRequest(t, "http://proxy-bypass.test:"+strconv.Itoa(target.Port)+"/"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != "direct" {
		t.Fatalf("body = %q", body)
	}
	if proxyHits.Load() != 0 {
		t.Fatalf("environment proxy was used %d times", proxyHits.Load())
	}
	if targetHits.Load() != 1 {
		t.Fatalf("target hits = %d, want 1", targetHits.Load())
	}
}

func TestHostOverrideAndDisallowedTargetAreRejected(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		AllowedDomains: []string{"allowed.test"},
		AllowedPorts:   []int{80},
		BearerToken:    "do-not-leak",
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mustRequest(t, "http://allowed.test/")
	req.Host = "169.254.169.254"
	if _, err := p.Do(req); !errors.Is(err, ErrHostOverride) {
		t.Fatalf("Host override: got %v, want ErrHostOverride", err)
	}

	req = mustRequest(t, "http://allowed.test/")
	req.Header.Set("Host", "metadata.google.internal")
	if _, err := p.Do(req); !errors.Is(err, ErrHostOverride) {
		t.Fatalf("Host header: got %v, want ErrHostOverride", err)
	}

	req = mustRequest(t, "http://not-allowed.test/")
	if _, err := p.Do(req); !errors.Is(err, ErrBlockedTarget) {
		t.Fatalf("disallowed host: got %v, want ErrBlockedTarget", err)
	}
}

func TestWildcardAndPortAllowlist(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		AllowedDomains: []string{"*.example.com", ".example.org"},
		AllowedPorts:   []int{443},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	allowed := []string{
		"https://api.example.com/",
		"https://deep.api.example.com/",
		"https://example.org/",
		"https://api.example.org/",
	}
	for _, rawURL := range allowed {
		if err := p.ValidateURL(rawURL); err != nil {
			t.Errorf("%s: %v", rawURL, err)
		}
	}
	blocked := []string{
		"https://example.com/",
		"https://evil-example.com/",
		"https://api.example.com.evil.test/",
		"https://api.example.com:8443/",
	}
	for _, rawURL := range blocked {
		if err := p.ValidateURL(rawURL); err == nil {
			t.Errorf("%s: expected rejection", rawURL)
		}
	}
}

func TestAllowlistEntryPortAndLocalhost(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		AllowedDomains: []string{"api.example.com:443"},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateURL("https://api.example.com:443/"); err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateURL("https://api.example.com:80/"); err == nil {
		t.Fatal("port outside the allowlist was accepted")
	}

	localhost, err := New(Config{
		AllowedDomains: []string{"localhost"},
		AllowedPorts:   []int{8080},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := localhost.ValidateURL("http://localhost:8080/"); err != nil {
		t.Fatal(err)
	}
}

func TestProxyHeadersAndConnectAreRejected(t *testing.T) {
	t.Parallel()

	proxyAuth := make(chan string, 1)
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyAuth <- r.Header.Get("Proxy-Authorization")
	}))
	defer server.Close()

	target := testTarget(t, server)
	p, err := New(Config{
		AllowedDomains: []string{target.Host},
		AllowedPorts:   []int{target.Port},
		BearerToken:    "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mustRequest(t, "https://allowed.test/")
	req.Method = http.MethodConnect
	if _, err := p.Do(req); !errors.Is(err, ErrProxyBypass) {
		t.Fatalf("CONNECT: got %v, want ErrProxyBypass", err)
	}

	req = mustRequest(t, server.URL)
	req.Header.Set("Proxy-Authorization", "Basic leaked")
	req.Header.Set("Proxy-Connection", "keep-alive")
	resp, err := p.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := <-proxyAuth; got != "" {
		t.Fatalf("Proxy-Authorization reached the target: %q", got)
	}
}
