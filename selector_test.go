package netsec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errConnectionRefused = errors.New("connection refused")

type testIdentity struct {
	certificate tls.Certificate
	leaf        *x509.Certificate
	roots       *x509.CertPool
	pin         string
}

func newTestIdentity(t *testing.T, name string) testIdentity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := SPKIPinForCert(leaf)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return testIdentity{
		certificate: tls.Certificate{
			Certificate: [][]byte{der},
			PrivateKey:  key,
			Leaf:        leaf,
		},
		leaf:  leaf,
		roots: roots,
		pin:   pin,
	}
}

type stringAddress string

func (address stringAddress) Network() string { return "memory" }
func (address stringAddress) String() string  { return string(address) }

type memoryConn struct {
	incoming <-chan []byte
	outgoing chan<- []byte
	done     <-chan struct{}
	closeFn  func()
	pending  []byte
}

func newMemoryConnPair() (net.Conn, net.Conn) {
	clientToServer := make(chan []byte, 1024)
	serverToClient := make(chan []byte, 1024)
	done := make(chan struct{})
	once := &sync.Once{}
	closeBoth := func() {
		once.Do(func() {
			close(done)
		})
	}
	client := &memoryConn{
		incoming: serverToClient,
		outgoing: clientToServer,
		done:     done,
		closeFn:  closeBoth,
	}
	server := &memoryConn{
		incoming: clientToServer,
		outgoing: serverToClient,
		done:     done,
		closeFn:  closeBoth,
	}
	return client, server
}

func (c *memoryConn) Read(buffer []byte) (int, error) {
	for len(c.pending) == 0 {
		select {
		case data := <-c.incoming:
			if len(data) == 0 {
				return 0, io.EOF
			}
			c.pending = data
		case <-c.done:
			select {
			case data := <-c.incoming:
				if len(data) == 0 {
					return 0, io.EOF
				}
				c.pending = data
			default:
				return 0, net.ErrClosed
			}
		}
	}
	n := copy(buffer, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *memoryConn) Write(buffer []byte) (int, error) {
	data := append([]byte(nil), buffer...)
	select {
	case c.outgoing <- data:
		return len(buffer), nil
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *memoryConn) Close() error {
	c.closeFn()
	return nil
}

func (c *memoryConn) LocalAddr() net.Addr              { return stringAddress("memory") }
func (c *memoryConn) RemoteAddr() net.Addr             { return stringAddress("memory") }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }

type memoryDialer struct {
	mu  sync.RWMutex
	tcp map[string]func() (net.Conn, error)
	udp map[string]func() (net.Conn, error)
}

func newMemoryDialer() *memoryDialer {
	return &memoryDialer{
		tcp: make(map[string]func() (net.Conn, error)),
		udp: make(map[string]func() (net.Conn, error)),
	}
}

func (d *memoryDialer) registerTCP(address string, dial func() (net.Conn, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tcp[address] = dial
}

func (d *memoryDialer) registerUDP(address string, dial func() (net.Conn, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.udp[address] = dial
}

func (d *memoryDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	var dial func() (net.Conn, error)
	if network == "udp" {
		dial = d.udp[address]
	} else {
		dial = d.tcp[address]
	}
	d.mu.RUnlock()
	if dial == nil {
		return nil, &net.OpError{
			Op:   "dial",
			Net:  network,
			Addr: stringAddress(address),
			Err:  errConnectionRefused,
		}
	}
	return dial()
}

func testQuery(t *testing.T, name string) []byte {
	t.Helper()
	query, err := PackQueryWithID(0x4242, NewQuestion(name, TypeA))
	if err != nil {
		t.Fatal(err)
	}
	return query
}

func responseFor(t *testing.T, query []byte, mutate func(*Question)) []byte {
	t.Helper()
	parsed, err := parseMessage(query)
	if err != nil {
		t.Errorf("test server could not parse query: %v", err)
		return nil
	}
	if len(parsed.questions) != 1 {
		t.Errorf("query has %d questions", len(parsed.questions))
		return nil
	}
	question := parsed.questions[0]
	if mutate != nil {
		mutate(&question)
	}
	response, err := PackQueryWithID(parsed.id, question)
	if err != nil {
		t.Errorf("test server could not pack response: %v", err)
		return nil
	}
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	return response
}

type plainServer struct {
	address string
	queries atomic.Int32
}

func startPlainServer(t *testing.T, dialer *memoryDialer, address string, mutate func(*Question)) *plainServer {
	t.Helper()
	server := &plainServer{address: address}
	dialer.registerUDP(address, func() (net.Conn, error) {
		return &udpTestConn{
			remote: stringAddress(address),
			handler: func(query []byte) []byte {
				server.queries.Add(1)
				return responseFor(t, query, mutate)
			},
		}, nil
	})
	return server
}

type udpTestConn struct {
	remote   stringAddress
	handler  func([]byte) []byte
	response []byte
	read     bool
	closed   bool
}

func (c *udpTestConn) Read(buffer []byte) (int, error) {
	if c.closed {
		return 0, net.ErrClosed
	}
	if c.read {
		return 0, io.EOF
	}
	c.read = true
	return copy(buffer, c.response), nil
}

func (c *udpTestConn) Write(query []byte) (int, error) {
	if c.closed {
		return 0, net.ErrClosed
	}
	c.response = c.handler(append([]byte(nil), query...))
	return len(query), nil
}

func (c *udpTestConn) Close() error                     { c.closed = true; return nil }
func (c *udpTestConn) LocalAddr() net.Addr              { return stringAddress("local") }
func (c *udpTestConn) RemoteAddr() net.Addr             { return c.remote }
func (c *udpTestConn) SetDeadline(time.Time) error      { return nil }
func (c *udpTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *udpTestConn) SetWriteDeadline(time.Time) error { return nil }

type doTServer struct {
	address string
	queries atomic.Int32
}

func startDoTServer(t *testing.T, dialer *memoryDialer, address string, identity testIdentity, mutate func(*Question)) *doTServer {
	t.Helper()
	server := &doTServer{address: address}
	dialer.registerTCP(address, func() (net.Conn, error) {
		client, rawServer := newMemoryConnPair()
		go func() {
			defer rawServer.Close()
			tlsServer := tls.Server(rawServer, &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{identity.certificate},
			})
			if err := tlsServer.Handshake(); err != nil {
				return
			}
			var length [2]byte
			if _, err := io.ReadFull(tlsServer, length[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(length[:]))
			if _, err := io.ReadFull(tlsServer, query); err != nil {
				return
			}
			server.queries.Add(1)
			response := responseFor(t, query, mutate)
			binary.BigEndian.PutUint16(length[:], uint16(len(response)))
			tlsServer.Write(length[:])
			tlsServer.Write(response)
		}()
		return client, nil
	})
	return server
}

type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	addr  net.Addr
	once  sync.Once
}

func newPipeListener(address string) *pipeListener {
	return &pipeListener{
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
		addr:  stringAddress(address),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		close(l.done)
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

type doHServer struct {
	address  string
	requests atomic.Int32
	close    func()
}

func startDoHServer(t *testing.T, dialer *memoryDialer, address string, identity testIdentity, contentType string, mutate func(*Question)) *doHServer {
	t.Helper()
	listener := newPipeListener(address)
	server := &doHServer{address: address}
	effectiveContentType := contentType
	if effectiveContentType == "" {
		effectiveContentType = "application/dns-message"
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		server.requests.Add(1)
		query, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(writer, "invalid DoH request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", effectiveContentType)
		writer.Write(responseFor(t, query, mutate))
	})
	httpServer := &http.Server{Handler: handler}
	tlsListener := tls.NewListener(listener, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{identity.certificate},
	})
	go httpServer.Serve(tlsListener)
	dialer.registerTCP(address, func() (net.Conn, error) {
		client, rawServer := newMemoryConnPair()
		select {
		case listener.conns <- rawServer:
			return client, nil
		case <-listener.done:
			client.Close()
			return nil, net.ErrClosed
		}
	})
	server.close = func() {
		httpServer.Close()
		listener.Close()
	}
	t.Cleanup(server.close)
	return server
}

func blockedAddress(protocol Protocol, index int) string {
	return fmt.Sprintf("blocked-%s-%d.invalid:%d", protocol, index, 443)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertQuestionMatches(t *testing.T, query, response []byte) {
	t.Helper()
	if err := validateResponse(query, response); err != nil {
		t.Fatalf("response did not validate: %v", err)
	}
}

func TestStrictBlockedPortsNeverFallBackToPlaintext(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	dialer := newMemoryDialer()
	plain := startPlainServer(t, dialer, "plain.test:53", nil)
	query := testQuery(t, "example.test")
	selector, err := NewSelector(Strict, []Upstream{
		DoHUpstream("doh", "https://resolver.test/dns-query", "resolver.test", []string{identity.pin}, blockedAddress(DoH, 0)),
		DoTUpstream("dot", "resolver.test", []string{identity.pin}, blockedAddress(DoT, 1)),
		PlainUpstream("plain", plain.address),
	}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	_, err = selector.Exchange(context.Background(), query)
	if err == nil {
		t.Fatal("strict selector succeeded despite blocked secure ports")
	}
	if !errors.Is(err, ErrStrictNoFallback) {
		t.Fatalf("error does not identify strict no-fallback policy: %v", err)
	}
	if got := plain.queries.Load(); got != 0 {
		t.Fatalf("strict mode sent %d plaintext queries", got)
	}
	var exchangeErr *ExchangeError
	if !errors.As(err, &exchangeErr) {
		t.Fatalf("unexpected error type %T", err)
	}
	if len(exchangeErr.Attempts) != 2 {
		t.Fatalf("got %d secure attempts, want 2", len(exchangeErr.Attempts))
	}
	for _, attempt := range exchangeErr.Attempts {
		if attempt.Protocol == PlainDNS {
			t.Fatal("strict mode attempted plaintext DNS")
		}
	}
}

func TestWrongCertificateIdentityRejected(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	dialer := newMemoryDialer()
	server := startDoTServer(t, dialer, "dot.test:853", identity, nil)
	plain := startPlainServer(t, dialer, "plain.test:53", nil)
	query := testQuery(t, "example.test")
	selector, err := NewSelector(Strict, []Upstream{
		{
			Name:      "dot",
			Protocol:  DoT,
			Bootstrap: []string{server.address},
			AuthName:  "wrong.test",
			SPKIPins:  []string{identity.pin},
			RootCAs:   identity.roots,
		},
		PlainUpstream("plain", plain.address),
	}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	_, err = selector.Exchange(context.Background(), query)
	if err == nil {
		t.Fatal("selector accepted a certificate for the wrong identity")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("error does not mention certificate identity: %v", err)
	}
	if got := plain.queries.Load(); got != 0 {
		t.Fatalf("strict mode sent %d plaintext queries", got)
	}
}

func TestDoHWrongCertificateIdentityRejected(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	dialer := newMemoryDialer()
	server := startDoHServer(t, dialer, "doh.test:443", identity, "", nil)
	plain := startPlainServer(t, dialer, "plain.test:53", nil)
	query := testQuery(t, "example.test")
	selector, err := NewSelector(Strict, []Upstream{
		{
			Name:      "doh",
			Protocol:  DoH,
			URL:       "https://resolver.test/dns-query",
			Bootstrap: []string{server.address},
			AuthName:  "wrong.test",
			SPKIPins:  []string{identity.pin},
			RootCAs:   identity.roots,
		},
		PlainUpstream("plain", plain.address),
	}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	_, err = selector.Exchange(context.Background(), query)
	if err == nil {
		t.Fatal("DoH selector accepted a certificate for the wrong identity")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("error does not mention certificate identity: %v", err)
	}
	if got := plain.queries.Load(); got != 0 {
		t.Fatalf("strict mode sent %d plaintext queries", got)
	}
}

func TestWrongSPKIPinRejected(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	other := newTestIdentity(t, "other.test")
	dialer := newMemoryDialer()
	server := startDoTServer(t, dialer, "dot.test:853", identity, nil)
	plain := startPlainServer(t, dialer, "plain.test:53", nil)
	query := testQuery(t, "example.test")
	selector, err := NewSelector(Strict, []Upstream{
		{
			Name:      "dot",
			Protocol:  DoT,
			Bootstrap: []string{server.address},
			AuthName:  "resolver.test",
			SPKIPins:  []string{other.pin},
			RootCAs:   identity.roots,
		},
		PlainUpstream("plain", plain.address),
	}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	_, err = selector.Exchange(context.Background(), query)
	if err == nil {
		t.Fatal("selector accepted a certificate with the wrong SPKI pin")
	}
	if !errors.Is(err, ErrSPKIPinMismatch) && !strings.Contains(err.Error(), "SPKI") {
		t.Fatalf("error does not identify SPKI mismatch: %v", err)
	}
	if got := plain.queries.Load(); got != 0 {
		t.Fatalf("strict mode sent %d plaintext queries", got)
	}
}

func TestBootstrapIsOnlyAConnectionHint(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	query := testQuery(t, "example.test")

	t.Run("doh", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoHServer(t, dialer, "doh.test:443", identity, "", nil)
		selector, err := NewSelector(Strict, []Upstream{
			{
				Name:      "doh",
				Protocol:  DoH,
				URL:       "https://poisoned.invalid/dns-query",
				Bootstrap: []string{blockedAddress(DoH, 9), server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
		}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		response, err := selector.Exchange(context.Background(), query)
		if err != nil {
			t.Fatalf("bootstrap hint was not used, or certificate name was overwritten: %v", err)
		}
		assertQuestionMatches(t, query, response)
	})

	t.Run("dot", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoTServer(t, dialer, "dot.test:853", identity, nil)
		selector, err := NewSelector(Strict, []Upstream{
			{
				Name:      "dot",
				Protocol:  DoT,
				Bootstrap: []string{server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
		}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		response, err := selector.Exchange(context.Background(), query)
		if err != nil {
			t.Fatalf("bootstrap hint was not used, or certificate name was overwritten: %v", err)
		}
		assertQuestionMatches(t, query, response)
	})
}

func TestDoHContentTypeIsEnforced(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	query := testQuery(t, "example.test")

	t.Run("strict", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoHServer(t, dialer, "doh.test:443", identity, "text/plain", nil)
		plain := startPlainServer(t, dialer, "plain.test:53", nil)
		selector, err := NewSelector(Strict, []Upstream{
			{
				Name:      "doh",
				Protocol:  DoH,
				URL:       "https://resolver.test/dns-query",
				Bootstrap: []string{server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
			PlainUpstream("plain", plain.address),
		}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		_, err = selector.Exchange(context.Background(), query)
		if err == nil || !errors.Is(err, ErrContentType) {
			t.Fatalf("expected content-type failure, got %v", err)
		}
		if got := plain.queries.Load(); got != 0 {
			t.Fatalf("strict mode sent %d plaintext queries", got)
		}
	})

	t.Run("opportunistic", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoHServer(t, dialer, "doh.test:443", identity, "text/plain", nil)
		plain := startPlainServer(t, dialer, "plain.test:53", nil)
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		var recorded []Degradation
		selector, err := NewSelector(Opportunistic, []Upstream{
			{
				Name:      "doh",
				Protocol:  DoH,
				URL:       "https://resolver.test/dns-query",
				Bootstrap: []string{server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
			PlainUpstream("plain", plain.address),
		}, WithLogger(logger), WithDialer(dialer), WithDegradationHook(func(degradation Degradation) {
			recorded = append(recorded, degradation)
		}), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		result, err := selector.Resolve(context.Background(), query)
		if err != nil {
			t.Fatalf("opportunistic fallback failed: %v", err)
		}
		if !result.Degraded || result.Protocol != PlainDNS || result.Degradation == nil {
			t.Fatalf("fallback was not recorded: %+v", result)
		}
		if got := plain.queries.Load(); got != 1 {
			t.Fatalf("plaintext fallback sent %d queries, want 1", got)
		}
		if len(recorded) != 1 {
			t.Fatalf("degradation hook called %d times, want 1", len(recorded))
		}
		if !strings.Contains(strings.ToLower(logs.String()), "degraded") ||
			!strings.Contains(strings.ToLower(logs.String()), "plaintext") {
			t.Fatalf("degradation was not logged: %q", logs.String())
		}
		assertQuestionMatches(t, query, result.Message)
	})
}

func TestQuestionSectionRewriteIsRejected(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	query := testQuery(t, "example.test")
	rewrite := func(question *Question) {
		question.Name = "evil.test"
	}

	t.Run("strict", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoTServer(t, dialer, "dot.test:853", identity, rewrite)
		plain := startPlainServer(t, dialer, "plain.test:53", nil)
		selector, err := NewSelector(Strict, []Upstream{
			{
				Name:      "dot",
				Protocol:  DoT,
				Bootstrap: []string{server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
			PlainUpstream("plain", plain.address),
		}, WithLogger(discardLogger()), WithDialer(dialer), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		_, err = selector.Exchange(context.Background(), query)
		if err == nil || !errors.Is(err, ErrQuestionMismatch) {
			t.Fatalf("expected question mismatch failure, got %v", err)
		}
		if got := plain.queries.Load(); got != 0 {
			t.Fatalf("strict mode sent %d plaintext queries", got)
		}
	})

	t.Run("opportunistic", func(t *testing.T) {
		dialer := newMemoryDialer()
		server := startDoTServer(t, dialer, "dot.test:853", identity, rewrite)
		plain := startPlainServer(t, dialer, "plain.test:53", nil)
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		selector, err := NewSelector(Opportunistic, []Upstream{
			{
				Name:      "dot",
				Protocol:  DoT,
				Bootstrap: []string{server.address},
				AuthName:  "resolver.test",
				SPKIPins:  []string{identity.pin},
				RootCAs:   identity.roots,
			},
			PlainUpstream("plain", plain.address),
		}, WithLogger(logger), WithDialer(dialer), WithTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		result, err := selector.Resolve(context.Background(), query)
		if err != nil {
			t.Fatalf("opportunistic fallback failed: %v", err)
		}
		if !result.Degraded {
			t.Fatal("fallback was not marked as degraded")
		}
		if !errors.Is(result.Degradation.Reason, ErrQuestionMismatch) {
			t.Fatalf("degradation reason lost question mismatch: %v", result.Degradation.Reason)
		}
		if got := plain.queries.Load(); got != 1 {
			t.Fatalf("plaintext fallback sent %d queries, want 1", got)
		}
		if !strings.Contains(strings.ToLower(logs.String()), "degraded") {
			t.Fatalf("degradation was not logged: %q", logs.String())
		}
	})
}

func TestOpportunisticLegitimateFallback(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")
	dialer := newMemoryDialer()
	plain := startPlainServer(t, dialer, "plain.test:53", nil)
	query := testQuery(t, "example.test")
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	var recorded Degradation
	selector, err := NewSelector(Opportunistic, []Upstream{
		DoHUpstream("doh", "https://resolver.test/dns-query", "resolver.test", []string{identity.pin}, blockedAddress(DoH, 7)),
		PlainUpstream("plain", plain.address),
	}, WithLogger(logger), WithDialer(dialer), WithDegradationHook(func(degradation Degradation) {
		recorded = degradation
	}), WithTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	result, err := selector.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("legitimate fallback failed: %v", err)
	}
	if !result.Degraded || result.Degradation == nil {
		t.Fatalf("fallback was not recorded: %+v", result)
	}
	if result.Degradation.From != "doh" || result.Degradation.To != "plain" {
		t.Fatalf("unexpected degradation route: %+v", result.Degradation)
	}
	if recorded.From != "doh" || recorded.To != "plain" {
		t.Fatalf("degradation hook did not receive the route: %+v", recorded)
	}
	if got := plain.queries.Load(); got != 1 {
		t.Fatalf("plaintext fallback sent %d queries, want 1", got)
	}
	if !strings.Contains(strings.ToLower(logs.String()), "degraded") {
		t.Fatalf("degradation was not logged: %q", logs.String())
	}
	assertQuestionMatches(t, query, result.Message)
}

func TestStrictConfigurationRequiresCallerSuppliedTrust(t *testing.T) {
	identity := newTestIdentity(t, "resolver.test")

	_, err := NewSelector(Strict, []Upstream{
		DoHUpstream("doh", "https://resolver.test/dns-query", "", nil, blockedAddress(DoH, 0)),
	})
	if err == nil || !errors.Is(err, ErrMissingAuthName) {
		t.Fatalf("expected missing authentication name, got %v", err)
	}

	_, err = NewSelector(Strict, []Upstream{
		DoHUpstream("doh", "https://resolver.test/dns-query", "resolver.test", nil, blockedAddress(DoH, 0)),
	})
	if err == nil || !errors.Is(err, ErrMissingSPKIPin) {
		t.Fatalf("expected missing SPKI pin, got %v", err)
	}

	if _, err := NewSelector(Strict, []Upstream{
		DoHUpstream("doh", "http://resolver.test/dns-query", "resolver.test", []string{identity.pin}),
	}); err == nil || !errors.Is(err, ErrInsecureDoHURL) {
		t.Fatalf("expected insecure DoH URL rejection, got %v", err)
	}
}

func TestResponseIDMismatchRejected(t *testing.T) {
	query := testQuery(t, "example.test")
	response, err := PackQueryWithID(0x9999, NewQuestion("example.test", TypeA))
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	if err := validateResponse(query, response); !errors.Is(err, ErrResponseIDMismatch) {
		t.Fatalf("expected transaction ID mismatch, got %v", err)
	}
}
