package netsec

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"reflect"
	"strings"
	"testing"
)

func TestConnectionNamingIdentityHeadersIsRejected(t *testing.T) {
	tests := []struct {
		name       string
		connection string
	}{
		{name: "user", connection: HeaderAuthUser},
		{name: "groups", connection: HeaderAuthGroups},
		{name: "lowercase user", connection: strings.ToLower(HeaderAuthUser)},
		{name: "mixed groups in list", connection: "keep-alive, x-AuTh-GrOuPs, close"},
		{name: "whitespace around user", connection: "\t " + HeaderAuthUser + " \t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authCalled := false
			nextCalled := false
			chain, err := New(
				func(*http.Request) (Identity, error) {
					authCalled = true
					return Identity{User: "alice"}, nil
				},
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					nextCalled = true
				}),
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			request := httptest.NewRequest(http.MethodGet, "http://proxy.example/resource", nil)
			request.Header.Set("Connection", tt.connection)
			request.Header.Set(HeaderAuthUser, "forged-user")
			request.Header.Set(HeaderAuthGroups, "forged-groups")
			recorder := httptest.NewRecorder()

			chain.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if authCalled {
				t.Fatal("authenticator was called for a request rejected by cleanup")
			}
			if nextCalled {
				t.Fatal("next handler was called for a request rejected by cleanup")
			}
			assertNoIdentityHeaders(t, recorder.Header())
		})
	}
}

func TestCleanupRemovesForgedAndHopByHopHeadersBeforeAuthentication(t *testing.T) {
	fixedHopHeaders := []string{
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"TE",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"Proxy-Connection",
	}

	authCalled := false
	nextCalled := false
	chain, err := New(
		func(r *http.Request) (Identity, error) {
			authCalled = true
			assertNoIdentityHeaders(t, r.Header)
			for _, name := range append([]string{"Connection", "X-Named-Hop"}, fixedHopHeaders...) {
				if got := r.Header.Values(name); len(got) != 0 {
					t.Errorf("authenticator saw hop-by-hop header %q = %q", name, got)
				}
			}
			return Identity{User: "alice", Groups: []string{"staff", "ops"}}, nil
		},
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			nextCalled = true
			if got := r.Header.Get(HeaderAuthUser); got != "alice" {
				t.Errorf("%s = %q, want alice", HeaderAuthUser, got)
			}
			if got := r.Header.Get(HeaderAuthGroups); got != "staff,ops" {
				t.Errorf("%s = %q, want staff,ops", HeaderAuthGroups, got)
			}
			if r.Trailer != nil {
				t.Errorf("trailer map was forwarded: %#v", r.Trailer)
			}
			for _, name := range append([]string{"Connection", "X-Named-Hop"}, fixedHopHeaders...) {
				if got := r.Header.Values(name); len(got) != 0 {
					t.Errorf("forward handler saw hop-by-hop header %q = %q", name, got)
				}
			}
			assertNoIdentityHeadersExceptInjected(t, r.Header)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/resource", nil)
	request.Header.Set(HeaderAuthUser, "forged-user")
	request.Header[strings.ToLower(HeaderAuthUser)] = []string{"duplicate-forged-user"}
	request.Header.Set(HeaderAuthGroups, "forged-groups")
	request.Header[strings.ToLower(HeaderAuthGroups)] = []string{"duplicate-forged-groups"}
	request.Header.Set("Connection", "keep-alive, X-Named-Hop")
	request.Header.Set("X-Named-Hop", "remove-me")
	for _, name := range fixedHopHeaders {
		request.Header.Set(name, "remove-me")
	}
	request.Trailer = http.Header{"X-Trailer": []string{"remove-me"}}
	recorder := httptest.NewRecorder()

	chain.ServeHTTP(recorder, request)

	if !authCalled {
		t.Fatal("authenticator was not called")
	}
	if !nextCalled {
		t.Fatal("forward handler was not called")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestCustomMiddlewareOnlyAllowedAfterInjection(t *testing.T) {
	auth := func(*http.Request) (Identity, error) {
		return Identity{User: "alice"}, nil
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mw := func(next http.Handler) http.Handler { return next }

	for _, position := range []Position{
		PositionBeforeCleanup,
		PositionAfterCleanup,
		PositionAfterAuthentication,
	} {
		_, err := NewChain(auth, next, MiddlewareSpec{
			Position:   position,
			Middleware: mw,
		})
		if !errors.Is(err, ErrInvalidMiddlewarePosition) {
			t.Errorf("position %s: error = %v, want %v", position, err, ErrInvalidMiddlewarePosition)
		}
	}

	chain, err := NewChain(auth, next, MiddlewareSpec{
		Position:   PositionAfterInjection,
		Middleware: mw,
	})
	if err != nil {
		t.Fatalf("NewChain() with after-injection middleware error = %v", err)
	}
	if chain == nil || chain.Handler() == nil {
		t.Fatal("valid chain was not constructed")
	}
}

func TestMiddlewaresRunInFixedOrderAfterInjection(t *testing.T) {
	var order []string
	auth := func(*http.Request) (Identity, error) {
		order = append(order, "authenticate")
		return Identity{User: "alice", Groups: []string{"staff"}}, nil
	}
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		order = append(order, "forward")
		if got := r.Header.Get(HeaderAuthUser); got != "alice" {
			t.Errorf("%s = %q, want alice", HeaderAuthUser, got)
		}
	})

	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first")
			if got := r.Header.Get(HeaderAuthUser); got != "alice" {
				t.Errorf("first middleware saw %s = %q, want alice", HeaderAuthUser, got)
			}
			next.ServeHTTP(w, r)
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second")
			if got := r.Header.Get(HeaderAuthGroups); got != "staff" {
				t.Errorf("second middleware saw %s = %q, want staff", HeaderAuthGroups, got)
			}
			next.ServeHTTP(w, r)
		})
	}

	chain, err := New(auth, next, first, second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"authenticate", "first", "second", "forward"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestAuthenticationFailureNeverInjectsIdentity(t *testing.T) {
	t.Run("default status", func(t *testing.T) {
		nextCalled := false
		customCalled := false
		chain, err := New(
			func(*http.Request) (Identity, error) {
				return Identity{User: "alice", Groups: []string{"staff"}}, errors.New("invalid credentials")
			},
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			}),
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					customCalled = true
					next.ServeHTTP(w, r)
				})
			},
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(HeaderAuthUser, "forged-user")
		request.Header.Set(HeaderAuthGroups, "forged-groups")
		recorder := httptest.NewRecorder()
		chain.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
		}
		assertNoIdentityHeaders(t, recorder.Header())
		if nextCalled {
			t.Fatal("forward handler ran after authentication failure")
		}
		if customCalled {
			t.Fatal("after-injection middleware ran after authentication failure")
		}
	})

	t.Run("custom rejection status", func(t *testing.T) {
		chain, err := New(
			func(*http.Request) (Identity, error) {
				return Identity{User: "alice"}, &AuthError{
					StatusCode: http.StatusForbidden,
					Err:        errors.New("forbidden"),
				}
			},
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("forward handler ran after authentication failure")
			}),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		recorder := httptest.NewRecorder()
		chain.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
		}
		assertNoIdentityHeaders(t, recorder.Header())
	})

	t.Run("preexisting response headers are cleared", func(t *testing.T) {
		chain, err := New(
			func(*http.Request) (Identity, error) {
				return Identity{User: "alice"}, errors.New("invalid credentials")
			},
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("forward handler ran after authentication failure")
			}),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(HeaderAuthUser, "stale-user")
			w.Header().Set(HeaderAuthGroups, "stale-groups")
			chain.ServeHTTP(w, r)
		})
		recorder := httptest.NewRecorder()
		outer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
		}
		assertNoIdentityHeaders(t, recorder.Header())
	})
}

func TestEmptyGroupsNeverPreservesForgedHeader(t *testing.T) {
	chain, err := New(
		func(*http.Request) (Identity, error) {
			return Identity{User: "alice"}, nil
		},
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if got := r.Header.Values(HeaderAuthGroups); len(got) != 0 {
				t.Errorf("%s values = %q, want none", HeaderAuthGroups, got)
			}
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(HeaderAuthGroups, "forged-groups")
	chain.ServeHTTP(httptest.NewRecorder(), request)
}

func TestReverseProxyForwardsOnlyInjectedIdentity(t *testing.T) {
	var received http.Header
	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "upstream.internal"
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			received = r.Header.Clone()
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}),
	}
	chain, err := New(
		func(r *http.Request) (Identity, error) {
			if got := r.Header.Get(HeaderAuthUser); got != "" {
				t.Errorf("authenticator saw forged %s = %q", HeaderAuthUser, got)
			}
			return Identity{User: "alice", Groups: []string{"staff"}}, nil
		},
		proxy,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://proxy.example/", nil)
	request.Header.Set("Connection", "keep-alive, X-Named-Hop")
	request.Header.Set("X-Named-Hop", "remove-me")
	request.Header.Set(HeaderAuthUser, "forged-user")
	request.Header.Set(HeaderAuthGroups, "forged-groups")
	recorder := httptest.NewRecorder()

	chain.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := received.Get(HeaderAuthUser); got != "alice" {
		t.Errorf("upstream saw %s = %q, want alice", HeaderAuthUser, got)
	}
	if got := received.Get(HeaderAuthGroups); got != "staff" {
		t.Errorf("upstream saw %s = %q, want staff", HeaderAuthGroups, got)
	}
	if got := received.Get("X-Named-Hop"); got != "" {
		t.Errorf("upstream saw X-Named-Hop = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func assertNoIdentityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for _, name := range []string{HeaderAuthUser, HeaderAuthGroups} {
		if got := header.Values(name); len(got) != 0 {
			t.Errorf("unexpected identity header %q = %q", name, got)
		}
	}
}

func assertNoIdentityHeadersExceptInjected(t *testing.T, header http.Header) {
	t.Helper()
	var userValues, groupValues []string
	for key, values := range header {
		switch {
		case strings.EqualFold(key, HeaderAuthUser):
			userValues = append(userValues, values...)
		case strings.EqualFold(key, HeaderAuthGroups):
			groupValues = append(groupValues, values...)
		}
	}
	if want := []string{"alice"}; !reflect.DeepEqual(userValues, want) {
		t.Errorf("%s values = %q, want %q", HeaderAuthUser, userValues, want)
	}
	if want := []string{"staff,ops"}; !reflect.DeepEqual(groupValues, want) {
		t.Errorf("%s values = %q, want %q", HeaderAuthGroups, groupValues, want)
	}
}
