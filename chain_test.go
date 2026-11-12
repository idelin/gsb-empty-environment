package netsec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type backendRecorder struct {
	called bool
	header http.Header
}

func (b *backendRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.called = true
		b.header = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})
}

func okAuth(user string, groups ...string) Authenticator {
	return AuthenticatorFunc(func(r *http.Request) (Identity, error) {
		return Identity{User: user, Groups: groups}, nil
	})
}

func failAuth() Authenticator {
	return AuthenticatorFunc(func(r *http.Request) (Identity, error) {
		return Identity{}, errors.New("bad credentials")
	})
}

func serve(chain *Chain, backend http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	chain.Handler(backend).ServeHTTP(rec, r)
	return rec
}

func TestConnectionNamingIdentityHeaderRejected(t *testing.T) {
	for _, target := range []string{"X-Auth-User", "x-auth-groups", "X-AUTH-USER"} {
		backend := &backendRecorder{}
		chain := NewChain(okAuth("alice", "dev"))

		r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
		r.Header.Set("Connection", "keep-alive, "+target)

		rec := serve(chain, backend.handler(), r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Connection naming %q: got status %d, want 400", target, rec.Code)
		}
		if backend.called {
			t.Fatalf("Connection naming %q: backend must not be reached", target)
		}
	}
}

func TestConnectionNamedHopByHopHeaderStripped(t *testing.T) {
	backend := &backendRecorder{}
	chain := NewChain(okAuth("alice", "dev"))

	r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
	r.Header.Set("Connection", "X-Hop, X-Auth-User-Not") // similar-but-not-identity names are fine
	r.Header.Set("X-Hop", "1")
	r.Header.Set("X-Auth-User-Not", "spoof")

	rec := serve(chain, backend.handler(), r)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if got := backend.header.Get("X-Hop"); got != "" {
		t.Fatalf("Connection-named header X-Hop not stripped: %q", got)
	}
	if got := backend.header.Get("X-Auth-User-Not"); got != "" {
		t.Fatalf("Connection-named header X-Auth-User-Not not stripped: %q", got)
	}
	if got := backend.header.Get("Connection"); got != "" {
		t.Fatalf("Connection header forwarded: %q", got)
	}
}

func TestFixedHopByHopHeadersStripped(t *testing.T) {
	backend := &backendRecorder{}
	chain := NewChain(okAuth("alice", "dev"))

	r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
	for _, h := range []string{
		"Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"TE", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		r.Header.Set(h, "x")
	}

	serve(chain, backend.handler(), r)
	for _, h := range hopByHopHeaders {
		if got := backend.header.Get(h); got != "" {
			t.Fatalf("hop-by-hop header %s forwarded: %q", h, got)
		}
	}
}

func TestForgedIdentityHeadersReplaced(t *testing.T) {
	backend := &backendRecorder{}
	chain := NewChain(okAuth("alice", "dev", "ops"))

	r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
	r.Header.Set("X-Auth-User", "eve")
	r.Header.Set("X-Auth-Groups", "admin")

	rec := serve(chain, backend.handler(), r)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if got := backend.header.Get("X-Auth-User"); got != "alice" {
		t.Fatalf("X-Auth-User = %q, want injected %q", got, "alice")
	}
	gotGroups := backend.header.Values("X-Auth-Groups")
	if len(gotGroups) != 2 || gotGroups[0] != "dev" || gotGroups[1] != "ops" {
		t.Fatalf("X-Auth-Groups = %v, want [dev ops]", gotGroups)
	}
}

func TestCustomMiddlewareCannotBeInsertedIntoFixedStages(t *testing.T) {
	chain := NewChain(okAuth("alice"))
	mw := func(next http.Handler) http.Handler { return next }

	for _, stage := range []Stage{StageSanitize, StageAuth, StageInject, StageForward} {
		if err := chain.Insert(stage, mw); !errors.Is(err, ErrStageLocked) {
			t.Fatalf("Insert(stage=%d) = %v, want ErrStageLocked", stage, err)
		}
	}
}

func TestCustomMiddlewareRunsAfterInject(t *testing.T) {
	backend := &backendRecorder{}
	chain := NewChain(okAuth("alice", "dev"))

	var sawUser, sawGroups string
	chain.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sawUser = r.Header.Get("X-Auth-User")
			sawGroups = r.Header.Get("X-Auth-Groups")
			next.ServeHTTP(w, r)
		})
	})

	r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
	rec := serve(chain, backend.handler(), r)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if sawUser != "alice" || sawGroups != "dev" {
		t.Fatalf("custom middleware ran before inject: user=%q groups=%q", sawUser, sawGroups)
	}
}

func TestAuthFailureResponseHasNoInjectedHeaders(t *testing.T) {
	backend := &backendRecorder{}
	chain := NewChain(failAuth())

	r := httptest.NewRequest(http.MethodGet, "http://upstream/", nil)
	r.Header.Set("X-Auth-User", "eve")

	rec := serve(chain, backend.handler(), r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
	if backend.called {
		t.Fatal("backend must not be reached on auth failure")
	}
	for _, h := range InternalIdentityHeaders {
		if got := rec.Header().Get(h); got != "" {
			t.Fatalf("auth failure response carries %s: %q", h, got)
		}
	}
}
