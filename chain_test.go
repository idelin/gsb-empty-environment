package netsec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestConnectionNamingIdentityHeaderIsRejected(t *testing.T) {
	tests := []struct {
		name       string
		connection []string
	}{
		{name: "user", connection: []string{"X-Auth-User"}},
		{name: "groups", connection: []string{"keep-alive, X-Auth-Groups"}},
		{name: "lowercase", connection: []string{" x-auth-user , upgrade "}},
		{name: "multiple header lines", connection: []string{"keep-alive", "x-auth-groups"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authCalled := false
			forwardCalled := false
			chain, err := NewChain(
				func(*http.Request) (Identity, error) {
					authCalled = true
					return Identity{User: "alice"}, nil
				},
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					forwardCalled = true
				}),
			)
			if err != nil {
				t.Fatalf("NewChain: %v", err)
			}

			request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
			request.Header["connection"] = append([]string(nil), test.connection...)
			request.Header.Set(HeaderAuthUser, "attacker")
			request.Header.Set(HeaderAuthGroups, "admins")

			response := httptest.NewRecorder()
			chain.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if authCalled {
				t.Fatal("authenticator ran for a request rejected during sanitization")
			}
			if forwardCalled {
				t.Fatal("forward handler ran for a request rejected during sanitization")
			}
			assertIdentityHeadersAbsent(t, response.Header())
		})
	}
}

func TestSanitizeBeforeAuthAndInjectAfterAuth(t *testing.T) {
	authSawCleanRequest := false
	forwardCalled := false

	chain, err := NewChain(
		func(request *http.Request) (Identity, error) {
			authSawCleanRequest = requestIsClean(t, request)
			return Identity{User: "alice", Groups: []string{"staff", "ops"}}, nil
		},
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			forwardCalled = true
			if got := request.Header.Get(HeaderAuthUser); got != "alice" {
				t.Errorf("%s = %q, want alice", HeaderAuthUser, got)
			}
			if got := request.Header.Get(HeaderAuthGroups); got != "staff,ops" {
				t.Errorf("%s = %q, want staff,ops", HeaderAuthGroups, got)
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header.Set("Connection", "keep-alive, X-Remove-Me")
	request.Header.Set("Keep-Alive", "timeout=5")
	request.Header.Set("Proxy-Authenticate", "Basic")
	request.Header.Set("Proxy-Authorization", "Basic Zm9vOmJhcg==")
	request.Header.Set("TE", "trailers")
	request.Header.Set("Trailer", "X-Trailer")
	request.Header.Set("Transfer-Encoding", "chunked")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("X-Remove-Me", "attacker controlled")
	request.Header.Set(HeaderAuthUser, "spoofed-user")
	request.Header["x-auth-groups"] = []string{"spoofed-group"}
	request.TransferEncoding = []string{"chunked"}
	request.Trailer = http.Header{"X-Trailer": []string{"value"}}
	request.Close = true

	response := httptest.NewRecorder()
	chain.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if !authSawCleanRequest {
		t.Fatal("authenticator did not receive the sanitized request")
	}
	if !forwardCalled {
		t.Fatal("forward handler was not called")
	}
	if request.Header.Get(HeaderAuthUser) != "alice" {
		t.Fatalf("%s = %q after chain, want alice", HeaderAuthUser, request.Header.Get(HeaderAuthUser))
	}
	if request.Header.Get(HeaderAuthGroups) != "staff,ops" {
		t.Fatalf("%s = %q after chain, want staff,ops", HeaderAuthGroups, request.Header.Get(HeaderAuthGroups))
	}
}

func TestCustomMiddlewareRunsOnlyAfterInjection(t *testing.T) {
	var order []string
	var middlewareSawUser string
	var middlewareSawGroups string

	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			order = append(order, "first")
			middlewareSawUser = request.Header.Get(HeaderAuthUser)
			middlewareSawGroups = request.Header.Get(HeaderAuthGroups)
			next.ServeHTTP(w, request)
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			order = append(order, "second")
			next.ServeHTTP(w, request)
		})
	}

	authenticated := false
	chain, err := NewChain(
		func(request *http.Request) (Identity, error) {
			authenticated = true
			if request.Header.Get(HeaderAuthUser) != "" {
				t.Fatal("custom middleware ran before authentication")
			}
			return Identity{User: "alice", Groups: []string{"staff"}}, nil
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			order = append(order, "forward")
		}),
		WithMiddleware(first, second),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	response := httptest.NewRecorder()
	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil))

	if !authenticated {
		t.Fatal("authenticator was not called")
	}
	if got, want := order, []string{"first", "second", "forward"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("middleware order = %v, want %v", got, want)
	}
	if middlewareSawUser != "alice" {
		t.Fatalf("middleware saw %s = %q, want alice", HeaderAuthUser, middlewareSawUser)
	}
	if middlewareSawGroups != "staff" {
		t.Fatalf("middleware saw %s = %q, want staff", HeaderAuthGroups, middlewareSawGroups)
	}
}

func TestMiddlewareConfigurationCannotBeChangedAfterConstruction(t *testing.T) {
	var calls []string
	middleware := []Middleware{
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls = append(calls, "original")
				next.ServeHTTP(w, request)
			})
		},
	}

	chain, err := NewChain(
		func(*http.Request) (Identity, error) {
			calls = append(calls, "auth")
			return Identity{User: "alice"}, nil
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			calls = append(calls, "forward")
		}),
		WithMiddleware(middleware...),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	middleware[0] = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			calls = append(calls, "mutated")
			next.ServeHTTP(w, request)
		})
	}

	chain.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil),
	)

	if got, want := calls, []string{"auth", "original", "forward"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestAuthenticationFailureDoesNotInjectIdentity(t *testing.T) {
	customCalled := false
	forwardCalled := false

	chain, err := NewChain(
		func(request *http.Request) (Identity, error) {
			if request.Header.Get(HeaderAuthUser) != "" || request.Header.Get(HeaderAuthGroups) != "" {
				t.Error("authenticator received spoofed identity headers")
			}
			return Identity{}, errors.New("invalid credentials")
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			forwardCalled = true
		}),
		WithMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				customCalled = true
				next.ServeHTTP(w, request)
			})
		}),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header.Set(HeaderAuthUser, "spoofed-user")
	request.Header.Set(HeaderAuthGroups, "admins")
	response := httptest.NewRecorder()
	response.Header().Set(HeaderAuthUser, "stale-user")
	response.Header().Set(HeaderAuthGroups, "stale-groups")

	chain.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if customCalled {
		t.Fatal("custom middleware ran after authentication failed")
	}
	if forwardCalled {
		t.Fatal("forward handler ran after authentication failed")
	}
	assertIdentityHeadersAbsent(t, response.Header())
}

func TestUnauthorizedHandlerOption(t *testing.T) {
	chain, err := NewChain(
		func(*http.Request) (Identity, error) {
			return Identity{}, errors.New("no")
		},
		http.NotFoundHandler(),
		WithUnauthorizedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderAuthUser, "must-not-leak")
			w.Header().Set(HeaderAuthGroups, "must-not-leak")
			w.WriteHeader(http.StatusForbidden)
		})),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	response := httptest.NewRecorder()
	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	assertIdentityHeadersAbsent(t, response.Result().Header)
}

func TestUnauthorizedHandlerCannotLeaveIdentityHeadersWithoutWriting(t *testing.T) {
	chain, err := NewChain(
		func(*http.Request) (Identity, error) {
			return Identity{}, errors.New("no")
		},
		http.NotFoundHandler(),
		WithUnauthorizedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderAuthUser, "must-not-leak")
			w.Header().Set(HeaderAuthGroups, "must-not-leak")
		})),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	response := httptest.NewRecorder()
	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil))
	assertIdentityHeadersAbsent(t, response.Header())
}

func TestNewChainRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewChain(nil, http.NotFoundHandler()); err == nil {
		t.Fatal("NewChain accepted a nil authenticator")
	}
	if _, err := NewChain(func(*http.Request) (Identity, error) { return Identity{}, nil }, nil); err == nil {
		t.Fatal("NewChain accepted a nil forward handler")
	}
	if _, err := NewChain(
		func(*http.Request) (Identity, error) { return Identity{}, nil },
		http.NotFoundHandler(),
		WithMiddleware(nil),
	); err == nil {
		t.Fatal("NewChain accepted a nil middleware")
	}
}

func TestConfigConstructor(t *testing.T) {
	chain, err := New(Config{
		Authenticator: func(*http.Request) (Identity, error) {
			return Identity{User: "bob", Groups: []string{"dev"}}, nil
		},
		Forward: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Header.Get(HeaderAuthUser) != "bob" {
				t.Errorf("%s = %q, want bob", HeaderAuthUser, request.Header.Get(HeaderAuthUser))
			}
			w.WriteHeader(http.StatusOK)
		}),
		Middleware: []Middleware{func(next http.Handler) http.Handler { return next }},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	response := httptest.NewRecorder()
	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestChainHandlesNilRequestHeaderMap(t *testing.T) {
	chain, err := NewChain(
		func(*http.Request) (Identity, error) {
			return Identity{User: "alice"}, nil
		},
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if got := request.Header.Get(HeaderAuthUser); got != "alice" {
				t.Errorf("%s = %q, want alice", HeaderAuthUser, got)
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header = nil
	response := httptest.NewRecorder()
	chain.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestChainWithReverseProxyForwardsTrustedIdentity(t *testing.T) {
	target, err := url.Parse("http://backend.example")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var backendUser string
	var backendGroups string
	var backendRemoved string
	proxy.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		backendUser = request.Header.Get(HeaderAuthUser)
		backendGroups = request.Header.Get(HeaderAuthGroups)
		backendRemoved = request.Header.Get("X-Remove-Me")
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})

	chain, err := NewChain(
		func(*http.Request) (Identity, error) {
			return Identity{User: "alice", Groups: []string{"staff", "ops"}}, nil
		},
		proxy,
	)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header.Set("Connection", "X-Remove-Me")
	request.Header.Set("X-Remove-Me", "attacker controlled")
	request.Header.Set(HeaderAuthUser, "spoofed-user")
	request.Header.Set(HeaderAuthGroups, "spoofed-groups")
	response := httptest.NewRecorder()
	chain.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if backendUser != "alice" {
		t.Errorf("backend saw %s = %q, want alice", HeaderAuthUser, backendUser)
	}
	if backendGroups != "staff,ops" {
		t.Errorf("backend saw %s = %q, want staff,ops", HeaderAuthGroups, backendGroups)
	}
	if backendRemoved != "" {
		t.Errorf("backend saw Connection-nominated header %q", backendRemoved)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSanitizeRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header.Set("Connection", "X-Remove-Me")
	request.Header.Set("X-Remove-Me", "value")
	request.Header.Set(HeaderAuthUser, "spoofed")

	if err := SanitizeRequest(request); err != nil {
		t.Fatalf("SanitizeRequest: %v", err)
	}
	if len(headerValues(request.Header, "Connection")) != 0 {
		t.Error("Connection survived sanitization")
	}
	if len(headerValues(request.Header, "X-Remove-Me")) != 0 {
		t.Error("Connection-nominated header survived sanitization")
	}
	if len(headerValues(request.Header, HeaderAuthUser)) != 0 {
		t.Error("spoofed identity survived sanitization")
	}
}

func requestIsClean(t *testing.T, request *http.Request) bool {
	t.Helper()
	if len(headerValues(request.Header, "Connection")) != 0 {
		t.Error("Connection survived sanitization")
	}
	for _, name := range hopByHopHeaders {
		if len(headerValues(request.Header, name)) != 0 {
			t.Errorf("%s survived sanitization", name)
		}
	}
	if len(headerValues(request.Header, "X-Remove-Me")) != 0 {
		t.Error("Connection-nominated header survived sanitization")
	}
	if len(headerValues(request.Header, HeaderAuthUser)) != 0 {
		t.Error("spoofed X-Auth-User survived sanitization")
	}
	if len(headerValues(request.Header, HeaderAuthGroups)) != 0 {
		t.Error("spoofed X-Auth-Groups survived sanitization")
	}
	if request.TransferEncoding != nil {
		t.Error("TransferEncoding survived sanitization")
	}
	if request.Trailer != nil {
		t.Error("Trailer survived sanitization")
	}
	if request.Close {
		t.Error("Close survived sanitization")
	}
	return true
}

func assertIdentityHeadersAbsent(t *testing.T, header http.Header) {
	t.Helper()
	for name, values := range header {
		if isIdentityHeader(name) && strings.TrimSpace(strings.Join(values, "")) != "" {
			t.Errorf("unexpected response identity header %s: %q", name, values)
		}
	}
}
