package netsec

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func mustNewHeader(t *testing.T, pairs ...string) http.Header {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("header key/value pairs must be balanced")
	}
	header := http.Header{}
	for i := 0; i < len(pairs); i += 2 {
		header.Add(pairs[i], pairs[i+1])
	}
	return header
}

func TestCheckUserOnlyPermissionGroupIsForbidden(t *testing.T) {
	requestor := UserInfo{Name: "alice"}
	authz := NewRuleAuthorizer(
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{"bob"}},
	)

	// Only the user is impersonated: allowed, with no groups carried over.
	impersonated, requested, err := Check(requestor, mustNewHeader(t, ImpersonateUserHeader, "bob"), authz)
	if err != nil || !requested {
		t.Fatalf("expected user-only impersonation to pass, got requested=%v err=%v", requested, err)
	}
	if impersonated.Name != "bob" || len(impersonated.Groups) != 0 {
		t.Fatalf("unexpected impersonated identity: %+v", impersonated)
	}

	// Attaching system:masters is a separate check and must fail even though
	// impersonating bob itself is permitted.
	header := mustNewHeader(t,
		ImpersonateUserHeader, "bob",
		ImpersonateGroupHeader, "system:masters",
	)
	_, requested, err = Check(requestor, header, authz)
	if !requested {
		t.Fatalf("expected impersonation to be detected")
	}
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("expected *ForbiddenError, got %T: %v", err, err)
	}
	if forbidden.Attrs.Resource != GroupResource || forbidden.Attrs.Name != "system:masters" {
		t.Fatalf("expected failure on groups/system:masters, got %+v", forbidden.Attrs)
	}

	// Granting exactly the requested non-privileged group makes the full
	// request pass, proving the checks are independent per object.
	authz.Add(Rule{
		Subject:       "alice",
		Verb:          ImpersonateVerb,
		Resource:      GroupResource,
		ResourceNames: []string{"developers"},
	})
	header = mustNewHeader(t,
		ImpersonateUserHeader, "bob",
		ImpersonateGroupHeader, "developers",
	)
	impersonated, _, err = Check(requestor, header, authz)
	if err != nil {
		t.Fatalf("expected explicitly granted group to pass: %v", err)
	}
	if got, want := impersonated.Groups, []string{"developers"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

func TestCheckExtraKeyPercentEncodingBypass(t *testing.T) {
	requestor := UserInfo{Name: "alice"}
	authz := NewRuleAuthorizer(
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{"bob"}},
	)

	// Raw header key and percent-encoded variants must resolve to the same
	// lowercased key, and all must be denied without an userextras rule.
	for _, header := range []http.Header{
		mustNewHeader(t,
			ImpersonateUserHeader, "bob",
			ImpersonateExtraPrefix+"Role", "admin",
		),
		mustNewHeader(t,
			ImpersonateUserHeader, "bob",
			ImpersonateExtraPrefix+"R%6Fle", "admin",
		),
		mustNewHeader(t,
			ImpersonateUserHeader, "bob",
			ImpersonateExtraPrefix+"RO%6C%45", "admin",
		),
	} {
		impersonated, requested, err := Check(requestor, header, authz)
		if !requested || err == nil {
			t.Fatalf("expected encoded extra key to be denied, got identity=%+v err=%v", impersonated, err)
		}
		var forbidden *ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Fatalf("expected *ForbiddenError, got %v", err)
		}
		if got := forbidden.Attrs.CombinedResource(); got != "userextras/role" {
			t.Fatalf("check target = %q, want userextras/role (decoded + lowercased)", got)
		}
	}

	// A rule on the normalized key must authorize every encoded spelling.
	authz.Add(Rule{
		Subject:     "alice",
		Verb:        ImpersonateVerb,
		Resource:    UserExtraResource,
		Subresource: "role",
	})
	header := mustNewHeader(t,
		ImpersonateUserHeader, "bob",
		ImpersonateExtraPrefix+"R%6Fle", "admin",
		ImpersonateExtraPrefix+"role", "auditor",
	)
	impersonated, _, err := Check(requestor, header, authz)
	if err != nil {
		t.Fatalf("expected normalized userextras/role rule to pass: %v", err)
	}
	if got, want := impersonated.Extra["role"], []string{"admin", "auditor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("extra role values = %v, want %v", got, want)
	}
}

func TestCheckGroupWithoutUserIsRejected(t *testing.T) {
	requestor := UserInfo{Name: "alice", Groups: []string{"team-a"}}
	authz := NewRuleAuthorizer(
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: GroupResource, ResourceNames: []string{"*"}},
	)

	_, requested, err := Check(requestor, mustNewHeader(t, ImpersonateGroupHeader, "system:masters"), authz)
	if !requested || !errors.Is(err, ErrGroupWithoutUser) {
		t.Fatalf("expected ErrGroupWithoutUser, got requested=%v err=%v", requested, err)
	}

	// Even an allowed group cannot be attached without an impersonated user.
	_, _, err = Check(requestor, mustNewHeader(t, ImpersonateGroupHeader, "team-b"), authz)
	if !errors.Is(err, ErrGroupWithoutUser) {
		t.Fatalf("expected ErrGroupWithoutUser for allowed group too, got %v", err)
	}
}

func TestCheckServiceAccountUIDAndResourceNames(t *testing.T) {
	requestor := UserInfo{Name: "alice"}
	authz := NewRuleAuthorizer(
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{
			"system:serviceaccount:payments:deploy",
		}},
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: ServiceAccountResource, ResourceNames: []string{
			"deploy",
		}},
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UIDResource, ResourceNames: []string{"uid-7"}},
	)

	header := mustNewHeader(t,
		ImpersonateUserHeader, "system:serviceaccount:payments:deploy",
		ImpersonateUIDHeader, "uid-7",
	)
	impersonated, _, err := Check(requestor, header, authz)
	if err != nil {
		t.Fatalf("expected service account + uid impersonation to pass: %v", err)
	}
	if impersonated.UID != "uid-7" {
		t.Fatalf("uid = %q, want uid-7", impersonated.UID)
	}

	// resourceNames must actually constrain the check: a different SA name is
	// denied even though the user rule exists.
	deniedHeader := mustNewHeader(t,
		ImpersonateUserHeader, "system:serviceaccount:payments:other-sa",
	)
	_, _, err = Check(requestor, deniedHeader, authz)
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) || forbidden.Attrs.Resource != UserResource {
		t.Fatalf("expected user resourceNames denial, got %v", err)
	}

	// Dropping the serviceaccounts rule makes the otherwise identical request
	// fail on the second, independent check.
	weakAuthz := NewRuleAuthorizer(
		Rule{Subject: "alice", Verb: ImpersonateVerb, Resource: UserResource, ResourceNames: []string{
			"system:serviceaccount:payments:deploy",
		}},
	)
	_, _, err = Check(requestor, header, weakAuthz)
	if !errors.As(err, &forbidden) {
		t.Fatalf("expected serviceaccounts check failure, got %v", err)
	}

	// UID resourceNames are enforced independently as well.
	_, _, err = Check(requestor, mustNewHeader(t, ImpersonateUIDHeader, "other-uid"), authz)
	if !errors.As(err, &forbidden) {
		t.Fatalf("expected uid resourceNames denial, got %v", err)
	}
}

func TestCheckMalformedExtraEncoding(t *testing.T) {
	requestor := UserInfo{Name: "alice"}
	header := mustNewHeader(t, ImpersonateExtraPrefix+"%zz", "v")

	_, requested, err := Check(requestor, header, NewRuleAuthorizer())
	if !requested || err == nil {
		t.Fatalf("expected malformed percent-encoding error, got requested=%v err=%v", requested, err)
	}
}

func TestCheckNoImpersonationPassthrough(t *testing.T) {
	requestor := UserInfo{Name: "alice"}
	impersonated, requested, err := Check(requestor, http.Header{}, NewRuleAuthorizer())
	if err != nil || requested || impersonated.Name != "" {
		t.Fatalf("expected no impersonation, got %+v requested=%v err=%v", impersonated, requested, err)
	}
}
