package netsec

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestResponse(req *http.Request, status int, location string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
}

func basicAuth(login, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(login+":"+password))
}

func headerValue(header http.Header, name string) string {
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func mustNetrc(t *testing.T, data string) *Netrc {
	t.Helper()
	netrc, err := ParseNetrcString(data)
	if err != nil {
		t.Fatal(err)
	}
	return netrc
}

func TestTransportExactHostAndAuthorization(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine example.com login alice password secret`)
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newTestResponse(req, http.StatusOK, ""), nil
	})

	transport, err := NewRoundTripper(netrc, base, false)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		url           string
		authorization string
	}{
		{"exact host", "https://example.com/path", basicAuth("alice", "secret")},
		{"trailing dot", "https://EXAMPLE.COM./path", basicAuth("alice", "secret")},
		{"port ignored", "https://example.com:8443/path", basicAuth("alice", "secret")},
		{"near domain prefix", "https://evil-example.com/path", ""},
		{"near domain suffix", "https://example.com.evil.example/path", ""},
		{"subdomain is not a suffix match", "https://www.example.com/path", ""},
		{"plaintext disabled", "http://example.com/path", ""},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(http.MethodGet, test.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got := req.Header.Get("Authorization"); got != "" {
				t.Fatalf("input request was mutated: Authorization = %q", got)
			}
			if got := resp.Request.Header.Get("Authorization"); got != test.authorization {
				t.Fatalf("URL %s: Authorization = %q, want %q", test.url, got, test.authorization)
			}
		})
	}
}

func TestTransportExistingAuthorizationWins(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine example.com login alice password secret`)
	transport, err := NewRoundTripper(netrc, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newTestResponse(req, http.StatusOK, ""), nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header["authorization"] = []string{"Bearer caller-token"}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := headerValue(resp.Request.Header, "Authorization"); got != "Bearer caller-token" {
		t.Fatalf("Authorization = %q, want caller header", got)
	}
}

func TestTransportPlaintextHTTPOption(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine plain.example login alice password secret`)
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newTestResponse(req, http.StatusOK, ""), nil
	})

	req, err := http.NewRequest(http.MethodGet, "http://plain.example/path", nil)
	if err != nil {
		t.Fatal(err)
	}

	secure, err := NewRoundTripper(netrc, base, false)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := secure.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Request.Header.Get("Authorization"); got != "" {
		t.Fatalf("plaintext Authorization = %q, want none", got)
	}

	enabled, err := NewRoundTripper(netrc, base, true)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = enabled.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Request.Header.Get("Authorization"); got != basicAuth("alice", "secret") {
		t.Fatalf("plaintext Authorization = %q", got)
	}
}

func TestTransportPunycodeMatch(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine BÜCHER.Example. login alice password secret`)
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newTestResponse(req, http.StatusOK, ""), nil
	})
	transport, err := NewRoundTripper(netrc, base, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		"https://bücher.example/path",
		"https://xn--bcher-kva.example/path",
	} {
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Request.Header.Get("Authorization"); got != basicAuth("alice", "secret") {
			t.Fatalf("%s: Authorization = %q", target, got)
		}
	}
}

type redirectRecorder struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]string
	auth   map[string]string
}

func (r *redirectRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	route := req.URL.Host + req.URL.Path
	r.mu.Lock()
	r.auth[route] = req.Header.Get("Authorization")
	location := r.routes[route]
	r.mu.Unlock()

	if location == "" {
		return newTestResponse(req, http.StatusOK, ""), nil
	}
	return newTestResponse(req, http.StatusFound, location), nil
}

func (r *redirectRecorder) expectAuth(t *testing.T, route, want string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	got, ok := r.auth[route]
	if !ok {
		t.Fatalf("route %s was not requested", route)
	}
	if got != want {
		t.Fatalf("%s: Authorization = %q, want %q", route, got, want)
	}
}

func runRedirectChain(t *testing.T, netrc *Netrc) *redirectRecorder {
	t.Helper()

	recorder := &redirectRecorder{
		t: t,
		routes: map[string]string{
			"origin.example/start":  "https://attacker.example/next",
			"attacker.example/next": "https://origin.example/back",
			"origin.example/back":   "",
		},
		auth: make(map[string]string),
	}
	transport, err := NewRoundTripper(netrc, recorder, false)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Get("https://origin.example/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return recorder
}

func TestDefaultOnlyAppliesToInitialRequest(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `default login fallback password fallback-secret`)
	recorder := runRedirectChain(t, netrc)

	recorder.expectAuth(t, "origin.example/start", basicAuth("fallback", "fallback-secret"))
	recorder.expectAuth(t, "attacker.example/next", "")
	recorder.expectAuth(t, "origin.example/back", "")
}

func TestRedirectToOtherHostAndBackRematchesMachine(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine origin.example login origin-user password origin-pass
default login fallback password fallback-secret`)
	recorder := runRedirectChain(t, netrc)

	recorder.expectAuth(t, "origin.example/start", basicAuth("origin-user", "origin-pass"))
	recorder.expectAuth(t, "attacker.example/next", "")
	recorder.expectAuth(t, "origin.example/back", basicAuth("origin-user", "origin-pass"))
}

func TestRedirectUsesMachineEntryForNewHost(t *testing.T) {
	t.Parallel()

	netrc := mustNetrc(t, `machine origin.example login origin-user password origin-pass
machine attacker.example login attacker-user password attacker-pass
default login fallback password fallback-secret`)
	recorder := runRedirectChain(t, netrc)

	recorder.expectAuth(t, "origin.example/start", basicAuth("origin-user", "origin-pass"))
	recorder.expectAuth(t, "attacker.example/next", basicAuth("attacker-user", "attacker-pass"))
	recorder.expectAuth(t, "origin.example/back", basicAuth("origin-user", "origin-pass"))
}
