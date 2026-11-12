package netsec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type memoryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (s *memoryAuditSink) RecordAudit(_ context.Context, event AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *memoryAuditSink) snapshot() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEvent(nil), s.events...)
}

// resourceAuthorizer combines impersonation RBAC with ordinary resource
// permissions so the test can prove that only one identity is evaluated
// downstream.
type resourceAuthorizer struct {
	impersonation *RuleAuthorizer
	allowed       map[string]map[string]bool // user -> resource -> allowed
}

func (a *resourceAuthorizer) Authorize(attrs Attributes) (Decision, string, error) {
	if attrs.Verb == ImpersonateVerb {
		return a.impersonation.Authorize(attrs)
	}
	if resources, ok := a.allowed[attrs.User.GetName()]; ok && resources[attrs.Resource] {
		return DecisionAllow, "", nil
	}
	return DecisionDeny, "resource access denied", nil
}

func newStackingServer(t *testing.T) (http.Handler, *memoryAuditSink, *resourceAuthorizer) {
	t.Helper()
	authz := &resourceAuthorizer{
		impersonation: NewRuleAuthorizer(
			Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{"bob"}},
		),
		// alice can read pods; bob can read secrets. If permissions stacked,
		// an alice-impersonating-bob request could read both.
		allowed: map[string]map[string]bool{
			"alice": {"pods": true},
			"bob":   {"secrets": true},
		},
	}
	sink := &memoryAuditSink{}

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFrom(r.Context())
		resource := r.URL.Path[1:] // /pods or /secrets
		decision, _, _ := authz.Authorize(Attributes{
			User:     user,
			Verb:     "get",
			Resource: resource,
		})
		if !decision.Allowed() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Acting-User", user.GetName())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return Middleware{Authorizer: authz, Sink: sink}.Wrap(downstream), sink, authz
}

func performRequest(t *testing.T, handler http.Handler, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/pods", nil)
	req.Header = header
	req = req.WithContext(WithUser(req.Context(), UserInfo{Name: "alice", Groups: []string{"team-a"}}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMiddlewarePermissionDoesNotStack(t *testing.T) {
	handler, sink, _ := newStackingServer(t)

	// Impersonation request accepted: downstream runs as bob only.
	req := httptest.NewRequest(http.MethodGet, "/secrets", nil)
	req.Header.Set(ImpersonateUserHeader, "bob")
	req = req.WithContext(WithUser(req.Context(), UserInfo{Name: "alice"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Acting-User") != "bob" {
		t.Fatalf("expected bob to read secrets, got code=%d headers=%v", rec.Code, rec.Header())
	}

	// bob cannot read pods even though the original requestor alice can.
	req = httptest.NewRequest(http.MethodGet, "/pods", nil)
	req.Header.Set(ImpersonateUserHeader, "bob")
	req = req.WithContext(WithUser(req.Context(), UserInfo{Name: "alice"}))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected impersonated bob to be denied pods (no permission stacking), got %d", rec.Code)
	}

	// Raw impersonation headers must not survive into the downstream handler.
	var observed http.Header
	inspecting := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
	})
	mw := Middleware{Authorizer: &resourceAuthorizer{
		impersonation: NewRuleAuthorizer(
			Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{"bob"}},
		),
	}}.Wrap(inspecting)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(ImpersonateUserHeader, "bob")
	req.Header.Set(ImpersonateGroupHeader, "developers")
	req.Header.Set(ImpersonateExtraPrefix+"role", "admin")
	req = req.WithContext(WithUser(req.Context(), UserInfo{Name: "alice"}))
	mw.ServeHTTP(httptest.NewRecorder(), req)
	for header := range observed {
		if len(header) >= len("Impersonate") && header[:len("Impersonate")] == "Impersonate" {
			t.Fatalf("impersonation header leaked downstream: %s=%v", header, observed.Values(header))
		}
	}

	events := sink.snapshot()
	if len(events) < 1 {
		t.Fatalf("expected at least one audit event")
	}
	allowed := events[0]
	if !allowed.Allowed || allowed.Requestor.Name != "alice" ||
		allowed.Impersonated == nil || allowed.Impersonated.Name != "bob" {
		t.Fatalf("audit must record both original and impersonated identity: %+v", allowed)
	}
}

func TestMiddlewareRejectedImpersonationReturns403AndAudits(t *testing.T) {
	handler, sink, _ := newStackingServer(t)

	// alice may impersonate bob but has no group permission.
	rec := performRequest(t, handler, mustNewHeader(t,
		ImpersonateUserHeader, "bob",
		ImpersonateGroupHeader, "system:masters",
	))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body forbiddenStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON error body: %v", err)
	}
	if body.Code != http.StatusForbidden || body.Status != "Failure" {
		t.Fatalf("unexpected status body: %+v", body)
	}

	// Group without user is also a 403 rather than a silent pass.
	rec = performRequest(t, handler, mustNewHeader(t, ImpersonateGroupHeader, "system:masters"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("group-without-user status = %d, want 403", rec.Code)
	}

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected two denial audit events, got %d", len(events))
	}
	for _, event := range events {
		if event.Allowed || event.Requestor.Name != "alice" || event.Impersonated != nil {
			t.Fatalf("denial event must keep requestor and no substituted identity: %+v", event)
		}
		if event.Reason == "" {
			t.Fatalf("denial event must explain the failed check")
		}
	}
}

func TestMiddlewareUnauthenticatedRequest(t *testing.T) {
	handler, _, _ := newStackingServer(t)
	req := httptest.NewRequest(http.MethodGet, "/pods", nil)
	req.Header.Set(ImpersonateUserHeader, "bob")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
