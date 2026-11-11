package signedlink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testNow  = time.Unix(1_800_000_000, 0)
	testKeys = []Key{
		{ID: "old", Secret: []byte("old-secret"), NotAfter: testNow.Add(time.Hour)},
		{ID: "cur", Secret: []byte("current-secret")},
	}
	testStore = mustStore(testKeys, "cur")
)

func mustStore(keys []Key, active string) *KeyStore {
	ks, err := NewKeyStore(keys, active)
	if err != nil {
		panic(err)
	}
	return ks
}

// pipeListener is an in-memory net.Listener: the sandbox forbids socket
// binds, so the real HTTP round trip runs over net.Pipe connections
// instead. Client and server still speak genuine HTTP/1.1 on the wire.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}
func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (l *pipeListener) Addr() net.Addr { return pipeAddr("pipe") }

type pipeAddr string

func (a pipeAddr) Network() string { return string(a) }
func (a pipeAddr) String() string  { return string(a) }

// testEnv is a real HTTP server plus a client wired to it.
type testEnv struct {
	base   string
	client *http.Client
}

// startServer spins up a real HTTP server guarded by the middleware and
// returns a client that talks to it.
func startServer(t *testing.T, ks *KeyStore, now func() time.Time) testEnv {
	t.Helper()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "media-bytes")
	})
	ln := &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
	srv := &http.Server{Handler: MiddlewareWithClock(ks, now, ok)}
	go srv.Serve(ln)
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			clientEnd, serverEnd := net.Pipe()
			ln.conns <- serverEnd
			return clientEnd, nil
		},
	}}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		srv.Close()
	})
	return testEnv{base: "http://unix", client: client}
}

func doReq(t *testing.T, env testEnv, method, rawURL string) int {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func sign(t *testing.T, ks *KeyStore, method, rawURL string, extra url.Values) string {
	t.Helper()
	u, err := ks.SignURL(method, rawURL, testNow.Add(10*time.Minute), extra)
	if err != nil {
		t.Fatalf("SignURL: %v", err)
	}
	return u
}

// swapSig takes the sig from signedURL and grafts it onto targetURL,
// simulating an attacker reusing a captured signature on a different
// field combination.
func swapSig(t *testing.T, signedURL, targetURL string) string {
	t.Helper()
	su, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	sig := su.Query().Get(ParamSig)
	sep := "?"
	if strings.Contains(targetURL, "?") {
		sep = "&"
	}
	return targetURL + sep + ParamSig + "=" + sig
}

func TestValidSignedURLRoundTrip(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	u := sign(t, testStore, http.MethodGet, env.base+"/media/clip.mp4", url.Values{"q": {"hd"}})
	if got := doReq(t, env, http.MethodGet, u); got != http.StatusOK {
		t.Fatalf("valid signed URL: got %d, want 200", got)
	}
}

// TestNoConcatenationCollisions proves that distinct field combinations
// which would collide under naive "k+v" or "k=v&" joining cannot share a
// signature, over real HTTP round trips.
func TestNoConcatenationCollisions(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })

	cases := []struct {
		name       string
		signPath   string
		signExtra  url.Values
		attackPath string // raw target URL (unsigned) that reuses the sig
	}{
		{
			name:       "split key/value boundary",
			signPath:   "/m",
			signExtra:  url.Values{"a": {"bc"}},
			attackPath: "/m?ab=c",
		},
		{
			name:       "empty value vs absent key",
			signPath:   "/m",
			signExtra:  url.Values{"a": {""}},
			attackPath: "/m",
		},
		{
			name:       "empty value vs bare key without equals",
			signPath:   "/m",
			signExtra:  url.Values{"a": {""}},
			attackPath: "/m?a",
		},
		{
			name:       "duplicate values vs single joined value",
			signPath:   "/m",
			signExtra:  url.Values{"a": {"x", "y"}},
			attackPath: "/m?a=xy",
		},
		{
			name:       "duplicate values vs single value",
			signPath:   "/m",
			signExtra:  url.Values{"a": {"1", "1"}},
			attackPath: "/m?a=1",
		},
		{
			name:       "value absorbing separator",
			signPath:   "/m",
			signExtra:  url.Values{"a": {"b=c"}},
			attackPath: "/m?a=b&c=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signed := sign(t, testStore, http.MethodGet, env.base+tc.signPath, tc.signExtra)
			// Sanity: the legitimately signed URL must pass.
			if got := doReq(t, env, http.MethodGet, signed); got != http.StatusOK {
				t.Fatalf("signed URL rejected: got %d", got)
			}
			attack := swapSig(t, signed, env.base+tc.attackPath)
			if got := doReq(t, env, http.MethodGet, attack); got != http.StatusForbidden {
				t.Fatalf("collision attack succeeded: got %d, want 403 (%s)", got, attack)
			}
		})
	}
}

func TestEscapedPathBinding(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })

	signed := sign(t, testStore, http.MethodGet, env.base+"/media/a%2Fb", nil)
	if got := doReq(t, env, http.MethodGet, signed); got != http.StatusOK {
		t.Fatalf("signed escaped path rejected: got %d", got)
	}
	// Same decoded path, different raw form: must not verify.
	attack := swapSig(t, signed, env.base+"/media/a/b")
	if got := doReq(t, env, http.MethodGet, attack); got != http.StatusForbidden {
		t.Fatalf("unescaped path reuse: got %d, want 403", got)
	}
	// Different escape casing is a different raw path too.
	attack = swapSig(t, signed, env.base+"/media/a%2fb")
	if got := doReq(t, env, http.MethodGet, attack); got != http.StatusForbidden {
		t.Fatalf("lowercase-escape reuse: got %d, want 403", got)
	}
}

func TestMethodBinding(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	signed := sign(t, testStore, http.MethodGet, env.base+"/media/clip.mp4", nil)
	if got := doReq(t, env, http.MethodPost, signed); got != http.StatusForbidden {
		t.Fatalf("POST with GET signature: got %d, want 403", got)
	}
	if got := doReq(t, env, http.MethodHead, signed); got != http.StatusForbidden {
		t.Fatalf("HEAD with GET signature: got %d, want 403", got)
	}
}

func TestExpiryEnforced(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	u, err := testStore.SignURL(http.MethodGet, env.base+"/m", testNow.Add(-time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := doReq(t, env, http.MethodGet, u); got != http.StatusForbidden {
		t.Fatalf("expired link: got %d, want 403", got)
	}
}

func TestDuplicateSignatureFieldsRejected(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	signed := sign(t, testStore, http.MethodGet, env.base+"/m", nil)
	u, _ := url.Parse(signed)
	q := u.Query()

	dup := func(param string) string {
		qq := url.Values{}
		for k, vs := range q {
			qq[k] = append([]string(nil), vs...)
		}
		qq[param] = append(qq[param], q.Get(param))
		return env.base + "/m?" + qq.Encode()
	}

	for _, param := range []string{ParamSig, ParamExpires, ParamKeyID} {
		if got := doReq(t, env, http.MethodGet, dup(param)); got != http.StatusForbidden {
			t.Fatalf("duplicate %q: got %d, want 403", param, got)
		}
	}
}

func TestOldKeyGraceWindow(t *testing.T) {
	// Sign with a store whose active key is the old one.
	oldActive := mustStore(testKeys, "old")
	env := startServer(t, testStore, func() time.Time { return testNow })
	signed := sign(t, oldActive, http.MethodGet, env.base+"/m", nil)

	// Within the grace window the old key still verifies.
	if got := doReq(t, env, http.MethodGet, signed); got != http.StatusOK {
		t.Fatalf("old key inside grace window: got %d, want 200", got)
	}

	// Past the grace window the same link must fail.
	pastEnv := startServer(t, testStore, func() time.Time {
		return testNow.Add(2 * time.Hour)
	})
	pastURL := strings.Replace(signed, env.base, pastEnv.base, 1)
	if got := doReq(t, pastEnv, http.MethodGet, pastURL); got != http.StatusForbidden {
		t.Fatalf("old key past grace window: got %d, want 403", got)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	other := mustStore([]Key{
		{ID: "cur", Secret: []byte("current-secret")},
		{ID: "rogue", Secret: []byte("rogue")},
	}, "rogue")
	signed := sign(t, other, http.MethodGet, env.base+"/m", nil)
	if got := doReq(t, env, http.MethodGet, signed); got != http.StatusForbidden {
		t.Fatalf("unknown kid: got %d, want 403", got)
	}
}

func TestTamperedExpiresRejected(t *testing.T) {
	env := startServer(t, testStore, func() time.Time { return testNow })
	signed := sign(t, testStore, http.MethodGet, env.base+"/m", nil)
	u, _ := url.Parse(signed)
	q := u.Query()
	q.Set(ParamExpires, fmt.Sprint(testNow.Add(time.Hour).Unix()))
	u.RawQuery = q.Encode()
	if got := doReq(t, env, http.MethodGet, u.String()); got != http.StatusForbidden {
		t.Fatalf("tampered expires: got %d, want 403", got)
	}
}
