package netsec_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0087"
)

const layerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"

type capturedRequest struct {
	Method          string
	Host            string
	Path            string
	RawQuery        string
	Header          http.Header
	ClientCertCount int
}

type capture struct {
	mu       sync.Mutex
	requests []capturedRequest
	handler  http.HandlerFunc
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	certCount := 0
	if r.TLS != nil {
		certCount = len(r.TLS.PeerCertificates)
	}
	c.mu.Lock()
	c.requests = append(c.requests, capturedRequest{
		Method:          r.Method,
		Host:            r.Host,
		Path:            r.URL.Path,
		RawQuery:        r.URL.RawQuery,
		Header:          r.Header.Clone(),
		ClientCertCount: certCount,
	})
	c.mu.Unlock()
	c.handler(w, r)
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *capture) all() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *capture) at(i int) capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[i]
}

type testSite struct {
	host              string
	port              int
	cap               *capture
	cert              tls.Certificate
	requestClientCert bool

	mu    sync.Mutex
	conns []net.Conn
}

func newTestSite(t *testing.T, host string, requestClientCert bool, handler http.HandlerFunc) *testSite {
	t.Helper()
	port := int(atomic.AddInt32(&nextTestPort, 1))
	site := &testSite{
		host:              host,
		port:              port,
		cap:               &capture{handler: handler},
		cert:              newTestCertificate(t),
		requestClientCert: requestClientCert,
	}
	t.Cleanup(site.close)
	return site
}

func (s *testSite) origin() string {
	return "https://" + net.JoinHostPort(s.host, strconv.Itoa(s.port))
}

func (s *testSite) url(path string) string {
	return s.origin() + path
}

func testTransport(t *testing.T, sites ...*testSite) *http.Transport {
	t.Helper()
	routes := make(map[string]*testSite, len(sites))
	for _, site := range sites {
		routes[site.host] = site
	}
	return &http.Transport{
		Proxy:               nil,
		DialContext:         dialRoutes(routes),
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // test stand-ins use local certificates
		TLSHandshakeTimeout: 3 * time.Second,
		ForceAttemptHTTP2:   false,
	}
}

func dialRoutes(routes map[string]*testSite) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		site, ok := routes[host]
		if !ok {
			return nil, fmt.Errorf("no test route for %s", host)
		}
		clientConn, serverConn := net.Pipe()
		site.track(serverConn)
		go site.serveTLS(serverConn)
		return clientConn, nil
	}
}

func externalClientWithSecrets(t *testing.T, site *testSite, base *http.Transport) *http.Client {
	t.Helper()
	transport := base.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{site.cert}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	origin, err := url.Parse(site.origin())
	if err != nil {
		t.Fatalf("parse origin: %v", err)
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "external_session", Value: "must-not-leak"}})
	return &http.Client{Transport: transport, Jar: jar}
}

var (
	nextTestPort int32 = 8443
	certMu       sync.Mutex
	certCache    tls.Certificate
)

func newTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	certMu.Lock()
	defer certMu.Unlock()
	if len(certCache.Certificate) != 0 {
		return certCache
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "netsec test stand-in"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"example.test", "*.example.test", "other.test", "*.other.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse test certificate: %v", err)
	}
	certCache = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	return certCache
}

func (s *testSite) track(conn net.Conn) {
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()
}

func (s *testSite) close() {
	s.mu.Lock()
	conns := append([]net.Conn(nil), s.conns...)
	s.conns = nil
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (s *testSite) serveTLS(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	config := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
	if s.requestClientCert {
		config.ClientAuth = tls.RequestClientCert
	}
	tlsConn := tls.Server(raw, config)
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	reader := bufio.NewReader(tlsConn)
	for {
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		state := tlsConn.ConnectionState()
		request.TLS = &state
		recorder := httptest.NewRecorder()
		s.cap.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.ContentLength = int64(recorder.Body.Len())
		response.TransferEncoding = nil
		response.Close = request.Close
		if err := response.Write(tlsConn); err != nil {
			_ = response.Body.Close()
			return
		}
		_ = response.Body.Close()
		if request.Close {
			return
		}
	}
}

func mustRepository(t *testing.T, site *testSite, name string) netsec.Repository {
	t.Helper()
	repo, err := netsec.NewRepository(site.origin(), name)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	return repo
}

func digestOf(payload []byte) netsec.Digest {
	sum := sha256.Sum256(payload)
	return netsec.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func manifestFor(repo netsec.Repository, seed string) netsec.ManifestRef {
	return netsec.ManifestRef{Repository: repo, Digest: digestOf([]byte(seed))}
}

func validToken(repo netsec.Repository, digest netsec.Digest, value string) *netsec.BearerToken {
	return &netsec.BearerToken{
		Value:     value,
		Audiences: []string{repo.Source()},
		Scopes: []netsec.PullScope{{
			Repository: repo.Name,
			Digest:     digest,
			Actions:    []string{"pull"},
		}},
	}
}

func newFetcher(t *testing.T, cfg netsec.FetcherConfig) *netsec.Fetcher {
	t.Helper()
	if cfg.CacheDir == "" {
		cfg.CacheDir = t.TempDir()
	}
	fetcher, err := netsec.NewFetcher(cfg)
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	t.Cleanup(func() { _ = fetcher.Close() })
	return fetcher
}

func readLayer(t *testing.T, reader io.ReadCloser, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read layer: %v", err)
	}
	return payload
}

func fetchLayer(t *testing.T, fetcher *netsec.Fetcher, ctx context.Context, plan *netsec.LayerPlan) []byte {
	t.Helper()
	reader, err := fetcher.Fetch(ctx, plan)
	return readLayer(t, reader, err)
}

func servePayload(payload []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}
}

func serveStatus(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(status), status)
	}
}

func serveRedirect(location string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(status)
	}
}

func assertNoExternalSecrets(t *testing.T, request capturedRequest) {
	t.Helper()
	if got := request.Header.Get("Authorization"); got != "" {
		t.Errorf("external request carried Authorization %q", got)
	}
	if got := request.Header.Get("Cookie"); got != "" {
		t.Errorf("external request carried Cookie %q", got)
	}
	if got := request.Header.Get("X-Registry-Tenant"); got != "" {
		t.Errorf("external request carried repository header %q", got)
	}
	if request.ClientCertCount != 0 {
		t.Errorf("external request presented %d client certificates", request.ClientCertCount)
	}
	for name := range request.Header {
		switch http.CanonicalHeaderKey(name) {
		case "Accept", "User-Agent":
		default:
			t.Errorf("external request carried unregistered header %q=%q", name, request.Header.Get(name))
		}
	}
}

func assertPublicHeaders(t *testing.T, request capturedRequest, accept, userAgent string) {
	t.Helper()
	if got := request.Header.Get("Accept"); got != accept {
		t.Errorf("Accept = %q, want %q", got, accept)
	}
	if got := request.Header.Get("User-Agent"); got != userAgent {
		t.Errorf("User-Agent = %q, want %q", got, userAgent)
	}
}

func TestRepositoryLayerUsesExactSourceAndScopedToken(t *testing.T) {
	blob := []byte("verified compressed layer bytes")
	digest := digestOf(blob)
	repoName := "team/app"
	repoSite := newTestSite(t, "registry.example.test", false, servePayload(blob))
	repo := mustRepository(t, repoSite, repoName)
	manifest := manifestFor(repo, "manifest-a")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}

	policy := netsec.NewExternalURLPolicy()
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Kind != netsec.SourceRepository {
		t.Fatalf("unexpected plan sources: %#v", plan.Sources)
	}
	wantPath, _ := repo.BlobPath(digest)
	if got := plan.Sources[0].URL.Path; got != wantPath {
		t.Fatalf("repository source path = %q, want %q", got, wantPath)
	}

	var tokenCalls int32
	provider := netsec.TokenProviderFunc(func(_ context.Context, gotRepo netsec.Repository, gotDigest netsec.Digest) (*netsec.BearerToken, error) {
		atomic.AddInt32(&tokenCalls, 1)
		if gotRepo.Source() != repo.Source() {
			t.Errorf("token requested for wrong repository %q", gotRepo.Source())
		}
		if gotDigest != digest {
			t.Errorf("token requested for wrong digest %q", gotDigest)
		}
		return validToken(repo, digest, "repo-secret"), nil
	})
	base := testTransport(t, repoSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:            policy,
		TokenProvider:     provider,
		RepositoryClient:  &http.Client{Transport: base},
		ExternalClient:    &http.Client{Transport: base.Clone()},
		PublicAccept:      layerMediaType,
		PublicUserAgent:   "netsec-test/1.0",
		RepositoryHeaders: http.Header{"X-Registry-Tenant": []string{"tenant-a"}},
	})

	got := fetchLayer(t, fetcher, context.Background(), plan)
	if string(got) != string(blob) {
		t.Fatalf("layer bytes = %q, want %q", got, blob)
	}
	if count := repoSite.cap.count(); count != 1 {
		t.Fatalf("repository requests = %d, want 1", count)
	}
	request := repoSite.cap.at(0)
	if request.Path != wantPath {
		t.Errorf("request path = %q, want %q", request.Path, wantPath)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer repo-secret" {
		t.Errorf("Authorization = %q", got)
	}
	if got := request.Header.Get("X-Registry-Tenant"); got != "tenant-a" {
		t.Errorf("repository custom header = %q", got)
	}
	assertPublicHeaders(t, request, layerMediaType, "netsec-test/1.0")
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Errorf("token provider calls = %d, want 1", got)
	}
}

func TestRepositoryTokenScopeIsMandatory(t *testing.T) {
	blob := []byte("scoped repository blob")
	digest := digestOf(blob)
	repoSite := newTestSite(t, "registry.example.test", false, servePayload(blob))
	repo := mustRepository(t, repoSite, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}
	base := testTransport(t, repoSite)

	cases := []struct {
		name  string
		token *netsec.BearerToken
	}{
		{
			name: "wrong audience",
			token: &netsec.BearerToken{
				Value:     "secret",
				Audiences: []string{"https://other.example.test"},
				Scopes:    []netsec.PullScope{{Repository: repo.Name, Digest: digest, Actions: []string{"pull"}}},
			},
		},
		{
			name: "wrong digest",
			token: &netsec.BearerToken{
				Value:     "secret",
				Audiences: []string{repo.Source()},
				Scopes:    []netsec.PullScope{{Repository: repo.Name, Digest: digestOf([]byte("other")), Actions: []string{"pull"}}},
			},
		},
		{
			name: "missing pull",
			token: &netsec.BearerToken{
				Value:     "secret",
				Audiences: []string{repo.Source()},
				Scopes:    []netsec.PullScope{{Repository: repo.Name, Digest: digest, Actions: []string{"push"}}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := netsec.NewExternalURLPolicy()
			plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
			if err != nil {
				t.Fatalf("BuildLayerPlan: %v", err)
			}
			fetcher := newFetcher(t, netsec.FetcherConfig{
				Policy: policy,
				TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
					return tc.token, nil
				}),
				RepositoryClient: &http.Client{Transport: base.Clone()},
				ExternalClient:   &http.Client{Transport: base.Clone()},
			})
			reader, err := fetcher.Fetch(context.Background(), plan)
			if err == nil {
				if reader != nil {
					_ = reader.Close()
				}
				t.Fatal("Fetch succeeded with an unauthorized token")
			}
			if !errors.Is(err, netsec.ErrTokenScope) {
				t.Fatalf("error = %v, want ErrTokenScope", err)
			}
			before := repoSite.cap.count()
			if before != 0 {
				t.Fatalf("repository received %d requests despite rejected token", before)
			}
		})
	}
}

func TestExternalLayerUsesOnlyRegisteredPublicHeaders(t *testing.T) {
	blob := []byte("external public layer")
	digest := digestOf(blob)
	accept := "application/vnd.oci.image.layer.v1.tar+gzip"
	userAgent := "netsec-test/mirror"
	cdn := newTestSite(t, "cdn1.example.test", true, servePayload(blob))
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	layer := netsec.Layer{
		Digest:    digest,
		Size:      int64(len(blob)),
		MediaType: layerMediaType,
		URLs:      []string{cdn.url("/layers/blob")},
	}
	repo := mustRepository(t, cdn, "team/app")
	manifest := manifestFor(repo, "manifest")
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}

	base := testTransport(t, cdn)
	var tokenCalls int32
	provider := netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
		atomic.AddInt32(&tokenCalls, 1)
		return validToken(repo, digest, "repo-secret"), nil
	})
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:            policy,
		TokenProvider:     provider,
		RepositoryClient:  &http.Client{Transport: base.Clone()},
		ExternalClient:    externalClientWithSecrets(t, cdn, base),
		PublicAccept:      accept,
		PublicUserAgent:   userAgent,
		RepositoryHeaders: http.Header{"X-Registry-Tenant": []string{"tenant-a"}},
	})

	got := fetchLayer(t, fetcher, context.Background(), plan)
	if string(got) != string(blob) {
		t.Fatalf("layer bytes = %q, want %q", got, blob)
	}
	if count := cdn.cap.count(); count != 1 {
		t.Fatalf("external requests = %d, want 1", count)
	}
	request := cdn.cap.at(0)
	assertPublicHeaders(t, request, accept, userAgent)
	assertNoExternalSecrets(t, request)
	if got := atomic.LoadInt32(&tokenCalls); got != 0 {
		t.Errorf("token provider was called %d times for external-only layer", got)
	}
}

func TestSecretInjectionRigIsEffective(t *testing.T) {
	cdn := newTestSite(t, "cdn1.example.test", true, servePayload([]byte("cookie and cert probe")))
	base := testTransport(t, cdn)
	client := externalClientWithSecrets(t, cdn, base)
	response, err := client.Get(cdn.url("/probe"))
	if err != nil {
		t.Fatalf("direct probe request: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	request := cdn.cap.at(0)
	if got := request.Header.Get("Cookie"); !strings.Contains(got, "external_session=must-not-leak") {
		t.Fatalf("cookie injection was not active: %q", got)
	}
	if request.ClientCertCount == 0 {
		t.Fatal("client certificate injection was not active")
	}
}

func TestExternalLayerOmitsUnregisteredHeaders(t *testing.T) {
	blob := []byte("minimal public request")
	digest := digestOf(blob)
	cdn := newTestSite(t, "cdn1.example.test", false, servePayload(blob))
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	repo := mustRepository(t, cdn, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{cdn.url("/public")}}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, cdn)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})
	_ = fetchLayer(t, fetcher, context.Background(), plan)

	request := cdn.cap.at(0)
	if got := request.Header.Get("Accept"); got != "" {
		t.Errorf("unregistered Accept was sent: %q", got)
	}
	if got := request.Header.Get("User-Agent"); got != "" {
		t.Errorf("unregistered User-Agent was sent: %q", got)
	}
	if got := request.Header.Get("Accept-Encoding"); got != "" {
		t.Errorf("implicit Accept-Encoding was sent: %q", got)
	}
	assertNoExternalSecrets(t, request)
}

func TestTransformedResponseEncodingIsRejected(t *testing.T) {
	blob := []byte("content-encoded layer")
	digest := digestOf(blob)
	cdn := newTestSite(t, "cdn1.example.test", false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(blob)
	})
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	repo := mustRepository(t, cdn, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{cdn.url("/encoded")}}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, cdn)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})
	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch accepted an HTTP-transformed body")
	}
	if !errors.Is(err, netsec.ErrContentEncoding) {
		t.Fatalf("error = %v, want ErrContentEncoding", err)
	}
}

func TestSameSiteRepositoryRedirectStripsToken(t *testing.T) {
	blob := []byte("redirected layer")
	digest := digestOf(blob)
	repoName := "team/app"
	cdn := newTestSite(t, "cdn.registry.example.test", true, servePayload(blob))
	repoSite := newTestSite(t, "registry.example.test", false, nil)
	expectedPath, _ := mustRepository(t, repoSite, repoName).BlobPath(digest)
	repoSite.cap.handler = serveRedirect(cdn.url("/signed/blob"), http.StatusTemporaryRedirect)
	repo := mustRepository(t, repoSite, repoName)
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}

	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, repoSite, cdn)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy: policy,
		TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
			return validToken(repo, digest, "repo-secret"), nil
		}),
		RepositoryClient:  &http.Client{Transport: base.Clone()},
		ExternalClient:    externalClientWithSecrets(t, cdn, base),
		PublicAccept:      layerMediaType,
		PublicUserAgent:   "netsec-test/1.0",
		RepositoryHeaders: http.Header{"X-Registry-Tenant": []string{"tenant-a"}},
	})

	got := fetchLayer(t, fetcher, context.Background(), plan)
	if string(got) != string(blob) {
		t.Fatalf("layer bytes = %q, want %q", got, blob)
	}
	if count := repoSite.cap.count(); count != 1 {
		t.Fatalf("repository requests = %d, want 1", count)
	}
	repoRequest := repoSite.cap.at(0)
	if repoRequest.Path != expectedPath {
		t.Errorf("repository path = %q, want %q", repoRequest.Path, expectedPath)
	}
	if got := repoRequest.Header.Get("Authorization"); got != "Bearer repo-secret" {
		t.Errorf("repository Authorization = %q", got)
	}
	if got := repoRequest.Header.Get("X-Registry-Tenant"); got != "tenant-a" {
		t.Errorf("repository custom header = %q", got)
	}
	if count := cdn.cap.count(); count != 1 {
		t.Fatalf("CDN requests = %d, want 1", count)
	}
	assertPublicHeaders(t, cdn.cap.at(0), layerMediaType, "netsec-test/1.0")
	assertNoExternalSecrets(t, cdn.cap.at(0))
}

func TestSameOriginRepositoryRedirectIsTokenless(t *testing.T) {
	blob := []byte("same-origin redirected layer")
	digest := digestOf(blob)
	repoName := "team/app"
	repoSite := newTestSite(t, "registry.example.test", false, nil)
	repo := mustRepository(t, repoSite, repoName)
	repoSite.cap.handler = func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/signed/blob") {
			_, _ = w.Write(blob)
			return
		}
		w.Header().Set("Location", "/signed/blob")
		w.WriteHeader(http.StatusSeeOther)
	}
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}
	policy := netsec.NewExternalURLPolicy()
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, repoSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy: policy,
		TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
			return validToken(repo, digest, "repo-secret"), nil
		}),
		RepositoryClient:  &http.Client{Transport: base.Clone()},
		ExternalClient:    &http.Client{Transport: base.Clone()},
		PublicAccept:      layerMediaType,
		PublicUserAgent:   "netsec-test/1.0",
		RepositoryHeaders: http.Header{"X-Registry-Tenant": []string{"tenant-a"}},
	})

	got := fetchLayer(t, fetcher, context.Background(), plan)
	if string(got) != string(blob) {
		t.Fatalf("layer bytes = %q, want %q", got, blob)
	}
	if count := repoSite.cap.count(); count != 2 {
		t.Fatalf("repository-origin requests = %d, want 2", count)
	}
	if got := repoSite.cap.at(0).Header.Get("Authorization"); got != "Bearer repo-secret" {
		t.Errorf("exact blob request Authorization = %q", got)
	}
	redirected := repoSite.cap.at(1)
	if got := redirected.Header.Get("Authorization"); got != "" {
		t.Errorf("same-origin non-blob redirect carried Authorization %q", got)
	}
	if got := redirected.Header.Get("X-Registry-Tenant"); got != "" {
		t.Errorf("same-origin non-blob redirect carried repository header %q", got)
	}
	assertPublicHeaders(t, redirected, layerMediaType, "netsec-test/1.0")
}

func TestCrossSiteRedirectIsBlockedBeforeRequest(t *testing.T) {
	blob := []byte("must not leave registry")
	digest := digestOf(blob)
	evil := newTestSite(t, "evil.other.test", true, servePayload(blob))
	repoSite := newTestSite(t, "registry.example.test", false, nil)
	repoSite.cap.handler = serveRedirect(evil.url("/collect"), http.StatusFound)
	repo := mustRepository(t, repoSite, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(evil.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, repoSite, evil)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy: policy,
		TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
			return validToken(repo, digest, "repo-secret"), nil
		}),
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   externalClientWithSecrets(t, evil, base),
		PublicAccept:     layerMediaType,
		PublicUserAgent:  "netsec-test/1.0",
	})

	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch followed a cross-site redirect")
	}
	if !errors.Is(err, netsec.ErrURLPolicy) {
		t.Fatalf("error = %v, want ErrURLPolicy", err)
	}
	if count := repoSite.cap.count(); count != 1 {
		t.Fatalf("repository requests = %d, want 1", count)
	}
	if got := repoSite.cap.at(0).Header.Get("Authorization"); got != "Bearer repo-secret" {
		t.Errorf("repository Authorization = %q", got)
	}
	if count := evil.cap.count(); count != 0 {
		t.Fatalf("cross-site recipient received %d requests, want 0", count)
	}
}

func TestEachRedirectHopIsRevalidated(t *testing.T) {
	blob := []byte("per hop policy")
	digest := digestOf(blob)
	evil := newTestSite(t, "evil.other.test", false, servePayload(blob))
	cdn2 := newTestSite(t, "cdn2.example.test", false, nil)
	cdn2.cap.handler = serveRedirect(evil.url("/final"), http.StatusFound)
	cdn1 := newTestSite(t, "cdn1.example.test", false, nil)
	cdn1.cap.handler = serveRedirect(cdn2.url("/second"), http.StatusFound)

	policy := netsec.NewExternalURLPolicy()
	for _, site := range []*testSite{cdn1, cdn2, evil} {
		if err := policy.RegisterOrigin(site.origin()); err != nil {
			t.Fatalf("RegisterOrigin: %v", err)
		}
	}
	repo := mustRepository(t, cdn1, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{
		Digest:    digest,
		Size:      int64(len(blob)),
		MediaType: layerMediaType,
		URLs:      []string{cdn1.url("/first")},
	}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, cdn1, cdn2, evil)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
		PublicAccept:     layerMediaType,
		PublicUserAgent:  "netsec-test/1.0",
	})

	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch followed a cross-site redirect")
	}
	if !errors.Is(err, netsec.ErrURLPolicy) {
		t.Fatalf("error = %v, want ErrURLPolicy", err)
	}
	if cdn1.cap.count() != 1 || cdn2.cap.count() != 1 {
		t.Fatalf("same-site hops = %d, %d, want 1, 1", cdn1.cap.count(), cdn2.cap.count())
	}
	if evil.cap.count() != 0 {
		t.Fatalf("cross-site recipient received %d requests, want 0", evil.cap.count())
	}
}

func TestUnregisteredRedirectPortIsBlocked(t *testing.T) {
	blob := []byte("port policy")
	digest := digestOf(blob)
	closedSite := newTestSite(t, "cdn2.example.test", false, servePayload(blob))
	cdn1 := newTestSite(t, "cdn1.example.test", false, serveRedirect(closedSite.url("/blob"), http.StatusFound))

	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn1.origin()); err != nil {
		t.Fatalf("RegisterOrigin(cdn1): %v", err)
	}
	// Deliberately do not register cdn2's non-default port.
	repo := mustRepository(t, cdn1, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{cdn1.url("/blob")}}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, cdn1, closedSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})
	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch followed redirect to an unregistered port")
	}
	if !errors.Is(err, netsec.ErrURLPolicy) {
		t.Fatalf("error = %v, want ErrURLPolicy", err)
	}
	if closedSite.cap.count() != 0 {
		t.Fatalf("unregistered-port recipient received %d requests", closedSite.cap.count())
	}
}

func TestWrongDigestIsNeverPublished(t *testing.T) {
	expected := []byte("expected-layer")
	served := []byte("tampered-layer")
	digest := digestOf(expected)
	cdn := newTestSite(t, "cdn1.example.test", false, servePayload(served))
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	repo := mustRepository(t, cdn, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(served)), MediaType: layerMediaType, URLs: []string{cdn.url("/bad")}}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	cacheDir := t.TempDir()
	base := testTransport(t, cdn)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
		CacheDir:         cacheDir,
	})

	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch published a wrong-digest layer")
	}
	if !errors.Is(err, netsec.ErrDigestMismatch) {
		t.Fatalf("error = %v, want ErrDigestMismatch", err)
	}
	assertEmptyDir(t, cacheDir)
	if count := cdn.cap.count(); count != 1 {
		t.Fatalf("requests = %d, want 1", count)
	}

	reader, err = fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("second Fetch reused a rejected blob")
	}
	if count := cdn.cap.count(); count != 2 {
		t.Fatalf("requests after failed retry = %d, want 2", count)
	}
	assertEmptyDir(t, cacheDir)
}

func TestLengthMismatchIsNeverPublished(t *testing.T) {
	blob := []byte("short-body")
	digest := digestOf(blob)
	cdn := newTestSite(t, "cdn1.example.test", false, servePayload(blob))
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	repo := mustRepository(t, cdn, "team/app")
	manifest := manifestFor(repo, "manifest")
	cacheDir := t.TempDir()
	base := testTransport(t, cdn)

	cases := []struct {
		name string
		size int64
	}{
		{name: "truncated", size: int64(len(blob) + 8)},
		{name: "too long", size: int64(len(blob) - 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layer := netsec.Layer{Digest: digest, Size: tc.size, MediaType: layerMediaType, URLs: []string{cdn.url("/" + strings.ReplaceAll(tc.name, " ", "-"))}}
			plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
			if err != nil {
				t.Fatalf("BuildLayerPlan: %v", err)
			}
			fetcher := newFetcher(t, netsec.FetcherConfig{
				Policy:           policy,
				RepositoryClient: &http.Client{Transport: base.Clone()},
				ExternalClient:   &http.Client{Transport: base.Clone()},
				CacheDir:         cacheDir,
			})
			reader, err := fetcher.Fetch(context.Background(), plan)
			if err == nil {
				_ = reader.Close()
				t.Fatal("Fetch published a length-mismatched layer")
			}
			if !errors.Is(err, netsec.ErrSizeMismatch) {
				t.Fatalf("error = %v, want ErrSizeMismatch", err)
			}
			assertEmptyDir(t, cacheDir)
		})
	}
}

func TestWrongDigestTriesNextURL(t *testing.T) {
	good := []byte("the-correct-layer-bytes")
	bad := []byte("the-tampered-layer-byte")
	if len(good) != len(bad) {
		t.Fatalf("test payloads must have equal length")
	}
	digest := digestOf(good)
	badSite := newTestSite(t, "cdn1.example.test", false, servePayload(bad))
	goodSite := newTestSite(t, "cdn2.example.test", false, servePayload(good))
	policy := netsec.NewExternalURLPolicy()
	for _, site := range []*testSite{badSite, goodSite} {
		if err := policy.RegisterOrigin(site.origin()); err != nil {
			t.Fatalf("RegisterOrigin: %v", err)
		}
	}
	repo := mustRepository(t, badSite, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{
		Digest:    digest,
		Size:      int64(len(good)),
		MediaType: layerMediaType,
		URLs:      []string{badSite.url("/bad"), goodSite.url("/good")},
	}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	cacheDir := t.TempDir()
	base := testTransport(t, badSite, goodSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
		CacheDir:         cacheDir,
	})

	got := fetchLayer(t, fetcher, context.Background(), plan)
	if string(got) != string(good) {
		t.Fatalf("layer bytes = %q, want %q", got, good)
	}
	if badSite.cap.count() != 1 || goodSite.cap.count() != 1 {
		t.Fatalf("requests = %d, %d, want 1, 1", badSite.cap.count(), goodSite.cap.count())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache entries = %d, want 1", len(entries))
	}
}

func TestTwoManifestsWithSameDigestShareVerifiedBlob(t *testing.T) {
	blob := []byte("shared verified blob")
	digest := digestOf(blob)
	firstSite := newTestSite(t, "cdn1.example.test", false, servePayload(blob))
	secondSite := newTestSite(t, "cdn2.example.test", false, servePayload(blob))
	policy := netsec.NewExternalURLPolicy()
	for _, site := range []*testSite{firstSite, secondSite} {
		if err := policy.RegisterOrigin(site.origin()); err != nil {
			t.Fatalf("RegisterOrigin: %v", err)
		}
	}
	repo := mustRepository(t, firstSite, "team/app")
	manifestA := manifestFor(repo, "manifest-a")
	manifestB := manifestFor(repo, "manifest-b")
	layerA := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{firstSite.url("/blob")}}
	layerB := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: "application/vnd.oci.image.layer.v1.tar", URLs: []string{secondSite.url("/other")}}
	planA, err := netsec.BuildLayerPlan(policy, manifestA, layerA)
	if err != nil {
		t.Fatalf("BuildLayerPlan A: %v", err)
	}
	planB, err := netsec.BuildLayerPlan(policy, manifestB, layerB)
	if err != nil {
		t.Fatalf("BuildLayerPlan B: %v", err)
	}
	base := testTransport(t, firstSite, secondSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})

	if got := fetchLayer(t, fetcher, context.Background(), planA); string(got) != string(blob) {
		t.Fatalf("first blob = %q", got)
	}
	if got := fetchLayer(t, fetcher, context.Background(), planB); string(got) != string(blob) {
		t.Fatalf("second blob = %q", got)
	}
	if firstSite.cap.count() != 1 {
		t.Errorf("first source requests = %d, want 1", firstSite.cap.count())
	}
	if secondSite.cap.count() != 0 {
		t.Errorf("second source requests = %d, want 0 (verified cache hit)", secondSite.cap.count())
	}
}

func TestAllURLsFail(t *testing.T) {
	blob := []byte("all fail")
	digest := digestOf(blob)
	sites := make([]*testSite, 0, 4)
	urls := make([]string, 0, 4)
	policy := netsec.NewExternalURLPolicy()
	for i := 0; i < 4; i++ {
		site := newTestSite(t, fmt.Sprintf("cdn%d.example.test", i+1), false, serveStatus(http.StatusServiceUnavailable))
		sites = append(sites, site)
		urls = append(urls, site.url(fmt.Sprintf("/blob-%d", i)))
		if err := policy.RegisterOrigin(site.origin()); err != nil {
			t.Fatalf("RegisterOrigin: %v", err)
		}
	}
	repo := mustRepository(t, sites[0], "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: urls}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	cacheDir := t.TempDir()
	base := testTransport(t, sites...)
	var tokenCalls int32
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy: policy,
		TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
			atomic.AddInt32(&tokenCalls, 1)
			return validToken(repo, digest, "repo-secret"), nil
		}),
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
		CacheDir:         cacheDir,
	})

	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch succeeded when every URL failed")
	}
	if !errors.Is(err, netsec.ErrHTTPStatus) {
		t.Fatalf("error = %v, want ErrHTTPStatus", err)
	}
	for i, site := range sites {
		if count := site.cap.count(); count != 1 {
			t.Errorf("source %d requests = %d, want 1", i, count)
		}
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 0 {
		t.Errorf("token provider called %d times for external URLs", got)
	}
	assertEmptyDir(t, cacheDir)
}

func TestURLListSuppressesRepositoryFallback(t *testing.T) {
	blob := []byte("no fallback")
	digest := digestOf(blob)
	cdn := newTestSite(t, "cdn1.example.test", false, serveStatus(http.StatusBadGateway))
	repoSite := newTestSite(t, "registry.example.test", false, servePayload(blob))
	policy := netsec.NewExternalURLPolicy()
	if err := policy.RegisterOrigin(cdn.origin()); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	repo := mustRepository(t, repoSite, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{cdn.url("/blob")}}
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, cdn, repoSite)
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy: policy,
		TokenProvider: netsec.TokenProviderFunc(func(context.Context, netsec.Repository, netsec.Digest) (*netsec.BearerToken, error) {
			return validToken(repo, digest, "repo-secret"), nil
		}),
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})
	reader, err := fetcher.Fetch(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("Fetch succeeded through an implicit repository fallback")
	}
	if cdn.cap.count() != 1 {
		t.Errorf("external requests = %d, want 1", cdn.cap.count())
	}
	if repoSite.cap.count() != 0 {
		t.Errorf("repository received %d fallback requests, want 0", repoSite.cap.count())
	}
}

func TestConcurrentSameDigestDoesNotShareCredentialedInflightRequest(t *testing.T) {
	blob := []byte("concurrent repository layer")
	digest := digestOf(blob)
	var arrivals int32
	secondArrived := make(chan struct{})
	var closeSecond sync.Once
	repoSite := newTestSite(t, "registry.example.test", false, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&arrivals, 1)
		if n == 1 {
			select {
			case <-secondArrived:
			case <-time.After(3 * time.Second):
			}
		}
		if n == 2 {
			closeSecond.Do(func() { close(secondArrived) })
		}
		_, _ = w.Write(blob)
	})
	repo := mustRepository(t, repoSite, "team/app")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}
	policy := netsec.NewExternalURLPolicy()
	plan, err := netsec.BuildLayerPlan(policy, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan: %v", err)
	}
	base := testTransport(t, repoSite)
	var tokenSequence int32
	provider := netsec.TokenProviderFunc(func(_ context.Context, gotRepo netsec.Repository, gotDigest netsec.Digest) (*netsec.BearerToken, error) {
		n := atomic.AddInt32(&tokenSequence, 1)
		return validToken(gotRepo, gotDigest, fmt.Sprintf("tok-%d", n)), nil
	})
	fetcher := newFetcher(t, netsec.FetcherConfig{
		Policy:           policy,
		TokenProvider:    provider,
		RepositoryClient: &http.Client{Transport: base.Clone()},
		ExternalClient:   &http.Client{Transport: base.Clone()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			reader, err := fetcher.Fetch(ctx, plan)
			if err != nil {
				results <- err
				return
			}
			payload, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil {
				results <- readErr
				return
			}
			if closeErr != nil {
				results <- closeErr
				return
			}
			if string(payload) != string(blob) {
				results <- fmt.Errorf("payload = %q", payload)
				return
			}
			results <- nil
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent fetch: %v", err)
		}
	}
	if count := repoSite.cap.count(); count != 2 {
		t.Fatalf("repository requests = %d, want 2 (no credentialed in-flight sharing)", count)
	}
	auths := make([]string, 0, 2)
	for _, request := range repoSite.cap.all() {
		auths = append(auths, request.Header.Get("Authorization"))
	}
	sort.Strings(auths)
	want := []string{"Bearer tok-1", "Bearer tok-2"}
	sort.Strings(want)
	if strings.Join(auths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Authorization headers = %v, want %v", auths, want)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory %s is not empty: %v", dir, names)
	}
}
