package sshchannel

import (
	"errors"
	"testing"
)

func TestRulePolicyAuthorizeAndDeny(t *testing.T) {
	policy := NewRulePolicy([]Rule{{HostName: "10.0.0.1", PortNum: 80}})

	grant, err := policy.Authorize("10.0.0.1", 80)
	if err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	if err := policy.Revoked(grant); err != nil {
		t.Fatalf("fresh grant unexpectedly revoked: %v", err)
	}
	if got := grant.Target().Host(); got != "10.0.0.1" {
		t.Fatalf("pinned host = %q", got)
	}

	if _, err := policy.Authorize("10.0.0.1", 22); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
	if err := policy.Revoked(nil); !errors.Is(err, ErrDenied) {
		t.Fatalf("nil grant must be denied, got %v", err)
	}
}

func TestRulePolicyPerGrantRevocation(t *testing.T) {
	policy := NewRulePolicy([]Rule{{HostName: "db.internal", PortNum: 5432}})

	g1, err := policy.Authorize("db.internal", 5432)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := policy.Authorize("db.internal", 5432)
	if err != nil {
		t.Fatal(err)
	}

	g1.Revoke()
	g1.Revoke() // idempotent

	if err := policy.Revoked(g1); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked grant must fail recheck, got %v", err)
	}
	if err := policy.Revoked(g2); err != nil {
		t.Fatalf("unrelated grant must stay valid, got %v", err)
	}
}

func TestRulePolicyGenerationBumpInvalidatesGrants(t *testing.T) {
	policy := NewRulePolicy([]Rule{{HostName: "svc", PortNum: 443}})

	old, err := policy.Authorize("svc", 443)
	if err != nil {
		t.Fatal(err)
	}

	// Same rules, but replacing them bumps the generation.
	policy.SetRules([]Rule{{HostName: "svc", PortNum: 443}})
	if err := policy.Revoked(old); !errors.Is(err, ErrDenied) {
		t.Fatalf("old-generation grant must be invalid, got %v", err)
	}

	fresh, err := policy.Authorize("svc", 443)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation == old.Generation {
		t.Fatalf("generation must advance: old=%d new=%d", old.Generation, fresh.Generation)
	}

	policy.RevokeAll()
	if err := policy.Revoked(fresh); !errors.Is(err, ErrDenied) {
		t.Fatalf("RevokeAll must invalidate current grants, got %v", err)
	}

	// A foreign grant (different policy pointer) can never pass the recheck.
	foreign := NewRulePolicy(nil)
	if err := foreign.Revoked(fresh); !errors.Is(err, ErrDenied) {
		t.Fatalf("foreign-policy grant must be denied, got %v", err)
	}
}
