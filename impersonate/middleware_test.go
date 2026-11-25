package impersonate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureHandler records the identity visible downstream after the middleware
// has run.
type captureHandler struct {
	called bool
	user   UserInfo
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.called = true
	h.user, _ = UserFrom(req.Context())
	w.WriteHeader(http.StatusOK)
}

// memorySink collects every audit event.
type memorySink struct {
	events []AuditEvent
}

func (s *memorySink) Record(_ context.Context, event AuditEvent) {
	s.events = append(s.events, event)
}

func requester() UserInfo {
	return &DefaultUserInfo{
		Name:   "dev",
		Groups: []string{"dev-team"},
		Extra:  map[string][]string{"scope": {"build"}},
	}
}

func serve(auth Authorizer, sink AuditSink, header func(*http.Request)) (*httptest.ResponseRecorder, *captureHandler, *memorySink) {
	handler := &captureHandler{}
	if sink == nil {
		sink = &memorySink{}
	}
	recorded := sink.(*memorySink)
	wrapped := Middleware(auth, recorded)(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	req = req.WithContext(WithUser(context.Background(), requester()))
	header(req)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	return rec, handler, recorded
}

func userOnlyAuthorizer() *RuleAuthorizer {
	return &RuleAuthorizer{Rules: []Rule{
		{Resource: ResourceUsers, Name: "victim"},
	}}
}

// A privilege-escalation attempt: allowed to impersonate the user but not the
// privileged system:masters group. The whole request must be denied.
func TestUserAllowedButGroupDenied(t *testing.T) {
	rec, handler, sink := serve(userOnlyAuthorizer(), nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "victim")
		req.Header.Add(ImpersonateGroupHeader, "system:masters")
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if handler.called {
		t.Fatal("downstream handler must not run when an impersonation check fails")
	}
	if len(sink.events) != 1 || sink.events[0].Allowed {
		t.Fatalf("expected one deny audit event, got %+v", sink.events)
	}
	if sink.events[0].Requester.GetUserName() != "dev" {
		t.Fatalf("audit must retain the original requester, got %+v", sink.events[0].Requester)
	}
}

// Repeated Impersonate-User headers and comma-merged values both conflict and
// must be rejected before any authorization happens.
func TestConflictingImpersonateUserHeaders(t *testing.T) {
	t.Run("repeated headers", func(t *testing.T) {
		rec, handler, _ := serve(userOnlyAuthorizer(), nil, func(req *http.Request) {
			req.Header[ImpersonateUserHeader] = []string{"victim", "someone-else"}
		})
		if rec.Code != http.StatusForbidden || handler.called {
			t.Fatalf("expected 403 without downstream call, got code=%d called=%v", rec.Code, handler.called)
		}
	})

	t.Run("comma merged values", func(t *testing.T) {
		rec, handler, _ := serve(userOnlyAuthorizer(), nil, func(req *http.Request) {
			req.Header.Set(ImpersonateUserHeader, "victim,someone-else")
		})
		if rec.Code != http.StatusForbidden || handler.called {
			t.Fatalf("expected 403 without downstream call, got code=%d called=%v", rec.Code, handler.called)
		}
	})
}

// The raw header carries a percent sign itself: it was encoded twice so that
// naive one-step decoding yields a different key than the one authorized.
func TestDoubleEncodedExtraKeyRejected(t *testing.T) {
	auth := &RuleAuthorizer{Rules: []Rule{
		{Resource: ResourceUsers, Name: "victim"},
		{Resource: ResourceUserExtras, Subresource: "admin"},
	}}

	t.Run("double encoding cannot smuggle a key", func(t *testing.T) {
		// "%256164" decodes once to "%6164", not to "admin".
		rec, handler, _ := serve(auth, nil, func(req *http.Request) {
			req.Header.Set(ImpersonateUserHeader, "victim")
			req.Header.Set("Impersonate-Extra-%256164", "true")
		})
		if rec.Code != http.StatusForbidden || handler.called {
			t.Fatalf("double-encoded key must be rejected, got code=%d called=%v", rec.Code, handler.called)
		}
	})

	t.Run("illegal percent sequence is rejected", func(t *testing.T) {
		rec, handler, _ := serve(auth, nil, func(req *http.Request) {
			req.Header.Set(ImpersonateUserHeader, "victim")
			req.Header.Set("Impersonate-Extra-ad%zzmin", "true")
		})
		if rec.Code != http.StatusForbidden || handler.called {
			t.Fatalf("invalid percent sequence must be rejected, got code=%d called=%v", rec.Code, handler.called)
		}
	})
}

// Group, uid and extra headers are meaningless without Impersonate-User and
// must be rejected outright.
func TestImpersonationWithoutUser(t *testing.T) {
	cases := map[string]func(*http.Request){
		"group": func(req *http.Request) {
			req.Header.Set(ImpersonateGroupHeader, "system:masters")
		},
		"uid": func(req *http.Request) {
			req.Header.Set(ImpersonateUIDHeader, "u-1234")
		},
		"extra": func(req *http.Request) {
			req.Header.Set("Impersonate-Extra-scope", "admin")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rec, handler, _ := serve(userOnlyAuthorizer(), nil, mutate)
			if rec.Code != http.StatusForbidden || handler.called {
				t.Fatalf("expected 403 without downstream call, got code=%d called=%v", rec.Code, handler.called)
			}
		})
	}
}

// On success downstream authorization sees only the impersonated identity:
// the requester's groups and extras never carry over.
func TestNoPrivilegeStackingOnSuccess(t *testing.T) {
	auth := &RuleAuthorizer{Rules: []Rule{
		{Resource: ResourceUsers, Name: "victim"},
		{Resource: ResourceGroups, Name: "ops"},
		{Resource: ResourceUserExtras, Subresource: "region"},
	}}

	rec, handler, sink := serve(auth, nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "victim")
		req.Header.Add(ImpersonateGroupHeader, "ops")
		req.Header.Set("Impersonate-Extra-REGION", "cn-east")
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !handler.called || handler.user == nil {
		t.Fatal("downstream handler should run with the impersonated identity")
	}

	got := handler.user
	if got.GetUserName() != "victim" {
		t.Fatalf("expected impersonated user victim, got %q", got.GetUserName())
	}
	if len(got.GetGroups()) != 1 || got.GetGroups()[0] != "ops" {
		t.Fatalf("groups must be exactly the impersonated set, got %v", got.GetGroups())
	}
	for _, group := range got.GetGroups() {
		if group == "dev-team" {
			t.Fatal("requester group must not be merged into the impersonated identity")
		}
	}
	if _, ok := got.GetExtra()["scope"]; ok {
		t.Fatal("requester extras must not be merged into the impersonated identity")
	}
	// Keys are lower-cased after percent-decoding.
	if vals := got.GetExtra()["region"]; len(vals) != 1 || vals[0] != "cn-east" {
		t.Fatalf("expected lower-cased extra region=[cn-east], got %v", got.GetExtra())
	}

	if len(sink.events) != 1 || !sink.events[0].Allowed {
		t.Fatalf("expected one allow audit event, got %+v", sink.events)
	}
	event := sink.events[0]
	if event.Requester.GetUserName() != "dev" || event.Impersonate.GetUserName() != "victim" {
		t.Fatalf("audit must record both identities, got %+v", event)
	}
}

// Each impersonated object maps to the documented resource and verb.
func TestResourceAndResourceNameChecks(t *testing.T) {
	auth := &RuleAuthorizer{Rules: []Rule{
		{Namespace: "platform", Resource: ResourceServiceAccounts, Name: "deploy-bot"},
		{Resource: ResourceGroups, Name: "ops"},
		{Resource: ResourceUIDs, Name: "uid-9"},
		{Resource: ResourceUserExtras, Subresource: "region"},
	}}

	var seen []Attributes
	spy := authorizerFunc(func(_ context.Context, attrs Attributes) (Decision, string, error) {
		seen = append(seen, attrs)
		return auth.Authorize(context.Background(), attrs)
	})

	rec, _, _ := serve(spy, nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "system:serviceaccount:platform:deploy-bot")
		req.Header.Add(ImpersonateGroupHeader, "ops,ops")
		req.Header.Set(ImpersonateUIDHeader, "uid-9")
		req.Header.Set("Impersonate-Extra-Reg%69on", "cn-east")
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	want := []Attributes{
		{Verb: VerbImpersonate, APIGroup: ServiceAccountAPIGroup, Namespace: "platform", Resource: ResourceServiceAccounts, Name: "deploy-bot"},
		{Verb: VerbImpersonate, Resource: ResourceGroups, Name: "ops"},
		{Verb: VerbImpersonate, Resource: ResourceUIDs, Name: "uid-9"},
		{Verb: VerbImpersonate, Resource: ResourceUserExtras, Subresource: "region"},
	}
	if len(seen) != len(want) {
		t.Fatalf("expected %d independent checks, got %d: %+v", len(want), len(seen), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("check %d: expected %+v got %+v", i, want[i], seen[i])
		}
	}
}

// When no impersonation headers are present the request passes through with
// the original identity untouched.
func TestNoImpersonationPassthrough(t *testing.T) {
	rec, handler, sink := serve(userOnlyAuthorizer(), nil, func(*http.Request) {})
	if rec.Code != http.StatusOK || !handler.called {
		t.Fatalf("expected passthrough 200, got code=%d called=%v", rec.Code, handler.called)
	}
	if handler.user.GetUserName() != "dev" {
		t.Fatalf("original identity must be preserved, got %v", handler.user)
	}
	if len(sink.events) != 0 {
		t.Fatalf("passthrough must not emit audit events, got %+v", sink.events)
	}
}

// resourceName restrictions: a rule scoped to another name does not match.
func TestResourceNameRestriction(t *testing.T) {
	auth := &RuleAuthorizer{Rules: []Rule{
		{Resource: ResourceUsers, Name: "allowed-user"},
	}}
	rec, handler, _ := serve(auth, nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "other-user")
	})
	if rec.Code != http.StatusForbidden || handler.called {
		t.Fatalf("resourceName-scoped rule must deny other users, got code=%d called=%v", rec.Code, handler.called)
	}
}

// authorizerFunc adapts a function into an Authorizer for test spying.
type authorizerFunc func(ctx context.Context, attrs Attributes) (Decision, string, error)

func (f authorizerFunc) Authorize(ctx context.Context, attrs Attributes) (Decision, string, error) {
	return f(ctx, attrs)
}

// A request without an authenticated identity in context cannot impersonate
// anyone and is rejected.
func TestMissingAuthenticatedUser(t *testing.T) {
	auth := userOnlyAuthorizer()
	handler := &captureHandler{}
	wrapped := Middleware(auth, nil)(handler)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(ImpersonateUserHeader, "victim")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || handler.called {
		t.Fatalf("expected 403 without downstream call, got code=%d called=%v", rec.Code, handler.called)
	}
}

// Several raw header spellings normalize to the same decoded, lower-cased key:
// values merge into one extra entry and only one userextras check is issued.
func TestExtraKeysMergeAfterNormalization(t *testing.T) {
	var checks []Attributes
	auth := authorizerFunc(func(_ context.Context, attrs Attributes) (Decision, string, error) {
		checks = append(checks, attrs)
		return DecisionAllow, "", nil
	})

	rec, handler, _ := serve(auth, nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "victim")
		req.Header.Add("Impersonate-Extra-Scope", "a")
		req.Header.Add("Impersonate-Extra-SCOPE", "b,c")
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	extraChecks := 0
	for _, check := range checks {
		if check.Resource == ResourceUserExtras {
			extraChecks++
			if check.Subresource != "scope" {
				t.Fatalf("expected normalized key scope, got %q", check.Subresource)
			}
		}
	}
	if extraChecks != 1 {
		t.Fatalf("expected one userextras check, got %d: %+v", extraChecks, checks)
	}
	got := handler.user.GetExtra()["scope"]
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("expected merged extra scope=[a b c], got %v", got)
	}
}

// An explicit deny decision is treated the same as no opinion.
func TestExplicitDenyRejected(t *testing.T) {
	auth := authorizerFunc(func(_ context.Context, _ Attributes) (Decision, string, error) {
		return DecisionDeny, "blocked by policy", nil
	})
	rec, handler, sink := serve(auth, nil, func(req *http.Request) {
		req.Header.Set(ImpersonateUserHeader, "victim")
	})
	if rec.Code != http.StatusForbidden || handler.called {
		t.Fatalf("explicit deny must reject, got code=%d called=%v", rec.Code, handler.called)
	}
	if len(sink.events) != 1 || sink.events[0].Allowed || sink.events[0].Reason != "blocked by policy" {
		t.Fatalf("expected deny reason in audit, got %+v", sink.events)
	}
}

func TestSplitServiceAccountUsername(t *testing.T) {
	namespace, name, err := splitServiceAccountUsername("system:serviceaccount:platform:deploy-bot")
	if err != nil || namespace != "platform" || name != "deploy-bot" {
		t.Fatalf("unexpected result: %q %q %v", namespace, name, err)
	}
	if _, _, err := splitServiceAccountUsername("plain-user"); err == nil {
		t.Fatal("plain username must not parse as a service account")
	}
	if _, _, err := splitServiceAccountUsername("system:serviceaccount:only-two"); err == nil {
		t.Fatal("malformed service account username must error")
	}
}
