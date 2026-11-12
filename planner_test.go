package netsec

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type observation struct {
	host          string
	uri           string
	authorization string
	cookie        string
	proxyAuth     string
	userinfo      string
}

type recorder struct {
	mu  sync.Mutex
	all []observation
}

func (r *recorder) record(obs observation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = append(r.all, obs)
}

func (r *recorder) snapshot() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]observation, len(r.all))
	copy(out, r.all)
	return out
}

func callbackHandler(rec *recorder, redirects map[string]string, finalBody string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		userinfo := ""
		if req.URL.User != nil {
			userinfo = req.URL.User.String()
		}
		rec.record(observation{
			host:          req.Host,
			uri:           req.URL.RequestURI(),
			authorization: req.Header.Get("Authorization"),
			cookie:        req.Header.Get("Cookie"),
			proxyAuth:     req.Header.Get("Proxy-Authorization"),
			userinfo:      userinfo,
		})
		if target, ok := redirects[req.URL.Path]; ok {
			http.Redirect(w, req, target, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, finalBody)
	})
}

func endpointHandler(rec *recorder, redirects map[string]string) http.Handler {
	return callbackHandler(rec, redirects, "artifact-body")
}

type testSites struct {
	private            *fakeEndpoint
	mirrorTLS          *fakeEndpoint
	mirrorPlain        *fakeEndpoint
	privateOrg         Origin
	mirrorTLSOrg       Origin
	mirrorOrg          Origin
	rec                *recorder
	privateRedirects   map[string]string
	mirrorTLSRedirects map[string]string
	mirrorRedirects    map[string]string
	endpoints          map[string]*fakeEndpoint
}

func newTestSites(t *testing.T) *testSites {
	t.Helper()
	rec := &recorder{}

	cert := mustSelfSignedCert(t)

	privateRedirects := map[string]string{}
	mirrorTLSRedirects := map[string]string{}
	mirrorRedirects := map[string]string{}

	private := newFakeEndpoint(t, "127.0.0.1", "9443", "https", endpointHandler(rec, privateRedirects), &tls.Config{Certificates: []tls.Certificate{cert}})
	mirrorTLS := newFakeEndpoint(t, "127.0.0.1", "9444", "https", endpointHandler(rec, mirrorTLSRedirects), &tls.Config{Certificates: []tls.Certificate{cert}})
	mirrorPlain := newFakeEndpoint(t, "127.0.0.1", "9080", "http", endpointHandler(rec, mirrorRedirects), nil)

	endpoints := map[string]*fakeEndpoint{
		private.originKey():     private,
		mirrorTLS.originKey():   mirrorTLS,
		mirrorPlain.originKey(): mirrorPlain,
	}
	t.Cleanup(func() {
		for _, endpoint := range endpoints {
			endpoint.close()
		}
	})

	return &testSites{
		private:            private,
		mirrorTLS:          mirrorTLS,
		mirrorPlain:        mirrorPlain,
		privateOrg:         private.origin,
		mirrorTLSOrg:       mirrorTLS.origin,
		mirrorOrg:          mirrorPlain.origin,
		rec:                rec,
		privateRedirects:   privateRedirects,
		mirrorTLSRedirects: mirrorTLSRedirects,
		mirrorRedirects:    mirrorRedirects,
		endpoints:          endpoints,
	}
}

func newTestSitesNoRedirects(t *testing.T) *testSites {
	t.Helper()
	return newTestSites(t)
}

func (s *testSites) setPrivateRedirect(t *testing.T, path, target string) {
	t.Helper()
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.privateRedirects[path] = target
}

func (s *testSites) setMirrorRedirect(t *testing.T, path, target string) {
	t.Helper()
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.mirrorRedirects[path] = target
}

func (s *testSites) setMirrorTLSRedirect(t *testing.T, path, target string) {
	t.Helper()
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.mirrorTLSRedirects[path] = target
}

func (s *testSites) transport() http.RoundTripper {
	return &memoryTransport{endpoints: s.endpoints}
}

func (s *testSites) planner(rules ...Rule) *Planner {
	return &Planner{
		Store:     NewCredentialStore(rules...),
		Transport: s.transport(),
	}
}

func mustOrigin(t *testing.T, raw string) Origin {
	t.Helper()
	origin, err := CanonicalOrigin(raw)
	if err != nil {
		t.Fatalf("CanonicalOrigin(%q): %v", raw, err)
	}
	return origin
}

func mustSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}
	return pair
}

const privateToken = "private-token"

func privateRule(origin Origin) Rule {
	return Rule{Origin: origin, Purpose: PurposeModuleDownload, Credential: BearerToken(privateToken)}
}

// fakeEndpoint is an in-memory HTTP server reachable through
// memoryTransport. It owns a virtual scheme/host/port identity and, for
// TLS endpoints, performs a real TLS handshake over net.Pipe so the
// tests exercise actual scheme-based credential separation without
// binding any network socket.
type fakeEndpoint struct {
	origin   Origin
	handler  http.Handler
	tlsConf  *tls.Config
	listener *pipeListener
	server   *http.Server
	done     chan struct{}
}

func newFakeEndpoint(t *testing.T, host, port, scheme string, handler http.Handler, tlsConf *tls.Config) *fakeEndpoint {
	t.Helper()
	endpoint := &fakeEndpoint{
		origin:   Origin{Scheme: scheme, Host: host, Port: port},
		handler:  handler,
		tlsConf:  tlsConf,
		listener: newPipeListener(),
		done:     make(chan struct{}),
	}
	endpoint.server = &http.Server{Handler: handler}
	go func() {
		defer close(endpoint.done)
		if tlsConf != nil {
			endpoint.server.TLSConfig = tlsConf.Clone()
			_ = endpoint.server.ServeTLS(endpoint.listener, "", "")
		} else {
			_ = endpoint.server.Serve(endpoint.listener)
		}
	}()
	return endpoint
}

func (e *fakeEndpoint) originKey() string {
	return e.origin.String()
}

func (e *fakeEndpoint) URL() string {
	return e.originKey()
}

func (e *fakeEndpoint) close() {
	e.server.Close()
	<-e.done
}

// pipeListener hands out ends of net.Pipe connections without binding
// any network socket.
type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
	addr   pipeAddr
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		ch:     make(chan net.Conn),
		closed: make(chan struct{}),
		addr:   pipeAddr{},
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.closed:
		return nil, errors.New("pipe listener closed")
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return l.addr
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// memoryTransport dials virtual endpoints over in-memory pipes. The
// request URL's normalized origin selects the endpoint; no real network
// stack is involved.
type memoryTransport struct {
	endpoints map[string]*fakeEndpoint
}

func (m *memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := originFromURL(req.URL)
	if err != nil {
		return nil, err
	}
	endpoint, ok := m.endpoints[origin.key()]
	if !ok {
		return nil, errors.New("no in-memory endpoint registered for " + origin.key())
	}

	clientConn, serverConn := net.Pipe()
	select {
	case endpoint.listener.ch <- serverConn:
	case <-endpoint.listener.closed:
		clientConn.Close()
		return nil, errors.New("endpoint listener is closed")
	}

	conn := net.Conn(clientConn)
	if endpoint.tlsConf != nil {
		tlsConn := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true})
		if err := tlsConn.Handshake(); err != nil {
			clientConn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	return writeRequestAndReadResponse(conn, req)
}

func writeRequestAndReadResponse(conn net.Conn, req *http.Request) (*http.Response, error) {
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body = &pipeBody{body: resp.Body, conn: conn}
	return resp, nil
}

type pipeBody struct {
	body io.ReadCloser
	conn net.Conn
	once sync.Once
}

func (p *pipeBody) Read(dst []byte) (int, error) {
	return p.body.Read(dst)
}

func (p *pipeBody) Close() error {
	p.once.Do(func() {
		p.conn.Close()
	})
	return p.body.Close()
}

// TestRedirectAcrossPort verifies that moving from the private source to
// a mirror on the same scheme but a different port drops the private
// token, because credentials are re-selected for the new origin.
func TestRedirectAcrossPort(t *testing.T) {
	sites := newTestSitesNoRedirects(t)
	sites.setPrivateRedirect(t, "/module.zip", sites.mirrorTLS.URL()+"/module.zip")
	planner := sites.planner(privateRule(sites.privateOrg))

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/module.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 2 {
		t.Fatalf("expected 2 callbacks, got %d: %+v", len(obs), obs)
	}
	if obs[0].authorization != "Bearer "+privateToken {
		t.Fatalf("private source did not receive configured token: %q", obs[0].authorization)
	}
	if obs[1].authorization != "" {
		t.Fatalf("private token leaked across port to mirror: %q", obs[1].authorization)
	}
	if obs[1].host != sites.mirrorTLSOrg.Host+":"+sites.mirrorTLSOrg.Port {
		t.Fatalf("mirror callback host mismatch: %q", obs[1].host)
	}

	if len(resp.Plan.Hops) != 2 || !resp.Plan.Hops[0].Authenticated || resp.Plan.Hops[1].Authenticated {
		t.Fatalf("plan authentication flags wrong: %+v", resp.Plan.Hops)
	}
}

// TestRedirectAcrossScheme verifies that an https -> http downgrade
// redirect cannot carry the private origin's credentials.
func TestRedirectAcrossScheme(t *testing.T) {
	sites := newTestSitesNoRedirects(t)
	sites.setPrivateRedirect(t, "/module.zip", sites.mirrorPlain.URL()+"/module.zip")
	planner := sites.planner(privateRule(sites.privateOrg))

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/module.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(obs))
	}
	if obs[0].authorization != "Bearer "+privateToken {
		t.Fatalf("private source auth header wrong: %q", obs[0].authorization)
	}
	if obs[1].authorization != "" || obs[1].proxyAuth != "" || obs[1].cookie != "" {
		t.Fatalf("credentials leaked across scheme downgrade: %+v", obs[1])
	}
	if resp.Plan.Hops[1].Origin.Scheme != "http" {
		t.Fatalf("mirror origin scheme not normalized: %+v", resp.Plan.Hops[1].Origin)
	}
}

// TestRedirectUserinfoIgnored verifies that userinfo embedded by the
// origin server in the Location header is never turned into an
// Authorization header and never appears in the forwarded URL.
func TestRedirectUserinfoIgnored(t *testing.T) {
	sites := newTestSitesNoRedirects(t)
	sites.setPrivateRedirect(t, "/module.zip",
		"https://attacker:stolen-secret@"+sites_mirrorHost(sites.mirrorTLSOrg)+"/module.zip")
	planner := sites.planner(privateRule(sites.privateOrg))

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/module.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(obs))
	}
	mirror := obs[1]
	if mirror.userinfo != "" {
		t.Fatalf("userinfo was forwarded to mirror: %q", mirror.userinfo)
	}
	if strings.Contains(mirror.uri, "attacker") || strings.Contains(mirror.uri, "stolen-secret") {
		t.Fatalf("userinfo leaked into mirror request URI: %q", mirror.uri)
	}
	if mirror.authorization != "" {
		t.Fatalf("mirror received authorization derived from userinfo: %q", mirror.authorization)
	}
	if resp.Plan.Hops[1].Authenticated {
		t.Fatalf("mirror hop must be unauthenticated without a store rule")
	}
}

// TestRedirectBounceBack verifies that when the mirror bounces the
// request back to the private source, credentials are re-selected from
// the store rather than remembered, and the mirror never saw the token.
func TestRedirectBounceBack(t *testing.T) {
	sites := newTestSitesNoRedirects(t)
	sites.setPrivateRedirect(t, "/module.zip", sites.mirrorPlain.URL()+"/mirror/module.zip")
	sites.setMirrorRedirect(t, "/mirror/module.zip", sites.private.URL()+"/module/final.zip")
	planner := sites.planner(privateRule(sites.privateOrg))

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/module.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 3 {
		t.Fatalf("expected 3 callbacks, got %d: %+v", len(obs), obs)
	}
	if obs[0].authorization != "Bearer "+privateToken {
		t.Fatalf("first private hop auth wrong: %q", obs[0].authorization)
	}
	if obs[1].authorization != "" {
		t.Fatalf("mirror hop received private token: %q", obs[1].authorization)
	}
	if obs[2].authorization != "Bearer "+privateToken {
		t.Fatalf("bounce-back to private source lost re-selected token: %q", obs[2].authorization)
	}

	for i, want := range []bool{true, false, true} {
		if resp.Plan.Hops[i].Authenticated != want {
			t.Fatalf("hop %d authenticated flag = %v, want %v", i, resp.Plan.Hops[i].Authenticated, want)
		}
	}
}

// TestPurposeBinding verifies that a credential authorized for one
// purpose is not used for a different purpose even on the same origin.
func TestPurposeBinding(t *testing.T) {
	sites := newTestSites(t)
	store := NewCredentialStore(
		Rule{Origin: sites.privateOrg, Purpose: PurposeModuleDownload, Credential: BearerToken("download-only")},
		Rule{Origin: sites.privateOrg, Purpose: PurposeMetadata, Credential: BearerToken("metadata-only")},
	)
	planner := &Planner{Store: store, Transport: sites.transport()}

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/v1.mod", PurposeMetadata, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 1 || obs[0].authorization != "Bearer metadata-only" {
		t.Fatalf("purpose-bound credential not selected: %+v", obs)
	}

	resp2, err := planner.Do(http.MethodGet, sites.private.URL()+"/v1.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp2.Body.Close()

	obs = sites.rec.snapshot()
	if obs[1].authorization != "Bearer download-only" {
		t.Fatalf("download credential not selected for its purpose: %q", obs[1].authorization)
	}

	resp3, err := planner.Do(http.MethodGet, sites.private.URL()+"/v1.list", Purpose("audit"), nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp3.Body.Close()

	obs = sites.rec.snapshot()
	if obs[2].authorization != "" {
		t.Fatalf("unknown purpose must not receive any credential: %q", obs[2].authorization)
	}
}

// TestCanonicalOrigin verifies normalization of scheme, host and port,
// including default ports, IPv6 and ignored userinfo.
func TestCanonicalOrigin(t *testing.T) {
	cases := []struct {
		raw  string
		want Origin
	}{
		{"HTTPS://Example.COM:443/path", Origin{Scheme: "https", Host: "example.com", Port: "443"}},
		{"http://example.com:80", Origin{Scheme: "http", Host: "example.com", Port: "80"}},
		{"https://example.com/x?y=1#f", Origin{Scheme: "https", Host: "example.com", Port: "443"}},
		{"http://user:pass@127.0.0.1:8080/", Origin{Scheme: "http", Host: "127.0.0.1", Port: "8080"}},
		{"http://[::1]:8080/", Origin{Scheme: "http", Host: "::1", Port: "8080"}},
		{"https://example.com.:443/", Origin{Scheme: "https", Host: "example.com", Port: "443"}},
	}
	for _, tc := range cases {
		got, err := CanonicalOrigin(tc.raw)
		if err != nil {
			t.Fatalf("CanonicalOrigin(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("CanonicalOrigin(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}

	if _, err := CanonicalOrigin("ftp://example.com/"); err == nil {
		t.Fatal("expected error for non-http(s) scheme")
	}
}

// TestStoreRejectsCrossOriginRule verifies that a rule scoped to one
// origin never authorizes a different port, scheme or host.
func TestStoreRejectsCrossOriginRule(t *testing.T) {
	private := Origin{Scheme: "https", Host: "source.example", Port: "443"}
	store := NewCredentialStore(privateRule(private))

	if _, ok := store.CredentialFor(private, PurposeModuleDownload); !ok {
		t.Fatal("configured origin/purpose should be authorized")
	}
	for _, other := range []Origin{
		{Scheme: "https", Host: "source.example", Port: "8443"},
		{Scheme: "http", Host: "source.example", Port: "443"},
		{Scheme: "https", Host: "mirror.example", Port: "443"},
	} {
		if _, ok := store.CredentialFor(other, PurposeModuleDownload); ok {
			t.Fatalf("rule for %s must not authorize %s", private, other)
		}
	}
	if _, ok := store.CredentialFor(private, PurposeMetadata); ok {
		t.Fatal("rule must not authorize a different purpose")
	}
}

// TestUnconfiguredOriginStartsAnonymous verifies that even the initial
// request stays anonymous when the store has no matching rule.
func TestUnconfiguredOriginStartsAnonymous(t *testing.T) {
	sites := newTestSites(t)
	planner := sites.planner(privateRule(sites.mirrorTLSOrg))

	resp, err := planner.Do(http.MethodGet, sites.private.URL()+"/module.zip", PurposeModuleDownload, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	obs := sites.rec.snapshot()
	if len(obs) != 1 || obs[0].authorization != "" {
		t.Fatalf("unconfigured origin must be anonymous: %+v", obs)
	}
}

// TestMaxHops verifies that redirect loops are bounded.
func TestMaxHops(t *testing.T) {
	sites := newTestSitesNoRedirects(t)
	sites.setPrivateRedirect(t, "/loop", sites.private.URL()+"/loop")
	planner := sites.planner(privateRule(sites.privateOrg))
	planner.MaxHops = 3

	if _, err := planner.Do(http.MethodGet, sites.private.URL()+"/loop", PurposeModuleDownload, nil); err == nil {
		t.Fatal("expected redirect loop to be rejected")
	}
}

func sites_mirrorHost(origin Origin) string {
	return net.JoinHostPort(origin.Host, origin.Port)
}
