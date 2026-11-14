package netsec

import (
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func newTestEnforcer() (*Enforcer, *MemorySink) {
	sink := &MemorySink{}
	e := NewEnforcer(sink)
	e.SetClock(func() time.Time { return testNow })
	return e, sink
}

// seed installs an admin token (policy:write in "prod") and a low-priv
// token that may only read the "web" service in "prod".
func seed(e *Enforcer) {
	e.policies["admin-prod"] = Policy{
		ID:              "admin-prod",
		Namespace:       "prod",
		ServiceIdentity: WildcardIdentity,
		Operations:      []Operation{OpPolicyWrite},
	}
	e.policies["web-read"] = Policy{
		ID:              "web-read",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operations:      []Operation{OpServiceRead},
	}
	e.RegisterToken(Token{
		ID:        "admin-token",
		PolicyIDs: []string{"admin-prod"},
		ExpiresAt: testNow.Add(time.Hour),
	})
	e.RegisterToken(Token{
		ID:        "low-token",
		PolicyIDs: []string{"web-read"},
		ExpiresAt: testNow.Add(time.Hour),
	})
}

func TestPrivilegeEscalationDenied(t *testing.T) {
	e, sink := newTestEnforcer()
	seed(e)

	executed := false
	decision, err := e.Execute(Intent{
		TokenID:         "low-token",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceDeregister,
	}, func() error { executed = true; return nil })

	if decision.Allowed || err == nil {
		t.Fatalf("expected denial, got %+v err=%v", decision, err)
	}
	if decision.Reason != DenyPrivilegeEscal {
		t.Fatalf("expected %s, got %s", DenyPrivilegeEscal, decision.Reason)
	}
	if executed {
		t.Fatal("denied intent must not execute")
	}
	assertAudited(t, sink, AuditIntent, false)
}

func TestCrossNamespaceDenied(t *testing.T) {
	e, sink := newTestEnforcer()
	seed(e)

	decision := e.Authorize(Intent{
		TokenID:         "low-token",
		Namespace:       "staging",
		ServiceIdentity: "web",
		Operation:       OpServiceRead,
	})

	if decision.Allowed {
		t.Fatalf("expected cross-namespace denial, got %+v", decision)
	}
	if decision.Reason != DenyNamespace {
		t.Fatalf("expected %s, got %s", DenyNamespace, decision.Reason)
	}
	assertAudited(t, sink, AuditIntent, false)
}

func TestExpiredTokenDenied(t *testing.T) {
	e, sink := newTestEnforcer()
	seed(e)
	e.RegisterToken(Token{
		ID:        "stale-token",
		PolicyIDs: []string{"web-read"},
		ExpiresAt: testNow.Add(-time.Minute),
	})

	decision := e.Authorize(Intent{
		TokenID:         "stale-token",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceRead,
	})

	if decision.Allowed {
		t.Fatalf("expected expired-token denial, got %+v", decision)
	}
	if decision.Reason != DenyTokenExpired {
		t.Fatalf("expected %s, got %s", DenyTokenExpired, decision.Reason)
	}
	assertAudited(t, sink, AuditIntent, false)
}

func TestLegitimateIntentAllowed(t *testing.T) {
	e, sink := newTestEnforcer()
	seed(e)

	executed := false
	decision, err := e.Execute(Intent{
		TokenID:         "low-token",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceRead,
	}, func() error { executed = true; return nil })

	if err != nil || !decision.Allowed {
		t.Fatalf("expected allow, got %+v err=%v", decision, err)
	}
	if !executed {
		t.Fatal("allowed intent must execute")
	}
	assertAudited(t, sink, AuditIntent, true)
}

func TestPolicyUpdateAuthorizedAndAudited(t *testing.T) {
	e, sink := newTestEnforcer()
	seed(e)

	// Low-priv token must not be able to change policies.
	denied := e.UpsertPolicy("low-token", Policy{
		ID:              "web-admin",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operations:      []Operation{OpServiceRegister, OpServiceDeregister},
	})
	if denied.Allowed {
		t.Fatal("low-priv token must not update policies")
	}
	if _, ok := e.policies["web-admin"]; ok {
		t.Fatal("rejected policy change must not take effect")
	}

	// Admin token updates the policy; the new grants apply immediately.
	allowed := e.UpsertPolicy("admin-token", Policy{
		ID:              "web-admin",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operations:      []Operation{OpServiceRegister},
	})
	if !allowed.Allowed {
		t.Fatalf("admin policy update denied: %+v", allowed)
	}
	e.RegisterToken(Token{
		ID:        "ops-token",
		PolicyIDs: []string{"web-admin"},
		ExpiresAt: testNow.Add(time.Hour),
	})
	decision := e.Authorize(Intent{
		TokenID:         "ops-token",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceRegister,
	})
	if !decision.Allowed {
		t.Fatalf("updated policy must grant the new operation: %+v", decision)
	}

	// Both the rejected and the applied change must be audited.
	var changes []AuditEvent
	for _, ev := range sink.Events() {
		if ev.Kind == AuditPolicyChange {
			changes = append(changes, ev)
		}
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 policy-change audit events, got %d", len(changes))
	}
	if changes[0].Change != "upsert_rejected" || changes[0].Decision.Allowed {
		t.Fatalf("first change should be a rejected upsert: %+v", changes[0])
	}
	if changes[1].Change != "upsert" || !changes[1].Decision.Allowed {
		t.Fatalf("second change should be an applied upsert: %+v", changes[1])
	}
}

func TestRevokedAndUnknownTokenDenied(t *testing.T) {
	e, _ := newTestEnforcer()
	seed(e)
	e.RevokeToken("low-token")

	if d := e.Authorize(Intent{
		TokenID:         "low-token",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceRead,
	}); d.Allowed || d.Reason != DenyTokenRevoked {
		t.Fatalf("expected revoked denial, got %+v", d)
	}
	if d := e.Authorize(Intent{
		TokenID:         "ghost",
		Namespace:       "prod",
		ServiceIdentity: "web",
		Operation:       OpServiceRead,
	}); d.Allowed || d.Reason != DenyTokenUnknown {
		t.Fatalf("expected unknown-token denial, got %+v", d)
	}
}

func assertAudited(t *testing.T, sink *MemorySink, kind AuditEventKind, allowed bool) {
	t.Helper()
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("expected audit events, got none")
	}
	last := events[len(events)-1]
	if last.Kind != kind {
		t.Fatalf("expected audit kind %s, got %s", kind, last.Kind)
	}
	if last.Decision == nil || last.Decision.Allowed != allowed {
		t.Fatalf("audit decision mismatch: %+v", last)
	}
}
