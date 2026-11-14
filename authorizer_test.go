package netsec

import (
	"errors"
	"testing"
)

func TestAuthorizeBinding(t *testing.T) {
	t.Parallel()

	authorizer, err := NewAuthorizerWithRules(
		Rule{
			Tenant:   "tenant-a",
			Exchange: "orders",
			Patterns: []string{"tenant-a.orders.*"},
		},
		Rule{
			Tenant:   "tenant-b",
			Exchange: "orders",
			Bindings: []string{"tenant-b.orders.created"},
		},
	)
	if err != nil {
		t.Fatalf("NewAuthorizerWithRules: %v", err)
	}

	tests := []struct {
		name     string
		tenant   string
		exchange string
		binding  string
		wantErr  error
	}{
		{name: "narrow exact under wildcard prefix", tenant: "tenant-a", exchange: "orders", binding: "tenant-a.orders.created"},
		{name: "same wildcard pattern", tenant: "tenant-a", exchange: "orders", binding: "tenant-a.orders.*"},
		{name: "exact rule", tenant: "tenant-b", exchange: "orders", binding: "tenant-b.orders.created"},
		{name: "hash escalation", tenant: "tenant-a", exchange: "orders", binding: "#", wantErr: ErrPattern},
		{name: "tenant hash escalation", tenant: "tenant-a", exchange: "orders", binding: "tenant-a.#", wantErr: ErrPattern},
		{name: "prefix expansion", tenant: "tenant-a", exchange: "orders", binding: "tenant-a.orders.#", wantErr: ErrPattern},
		{name: "hash under exact rule", tenant: "tenant-b", exchange: "orders", binding: "tenant-b.orders.#", wantErr: ErrPattern},
		{name: "cross exchange", tenant: "tenant-a", exchange: "payments", binding: "tenant-a.orders.created", wantErr: ErrExchange},
		{name: "cross tenant", tenant: "tenant-c", exchange: "orders", binding: "tenant-a.orders.created", wantErr: ErrTenant},
		{name: "empty tenant", tenant: "", exchange: "orders", binding: "tenant-a.orders.created", wantErr: ErrTenant},
		{name: "empty exchange", tenant: "tenant-a", exchange: "", binding: "tenant-a.orders.created", wantErr: ErrExchange},
		{name: "malformed binding", tenant: "tenant-a", exchange: "orders", binding: "tenant-a..orders", wantErr: ErrInvalidPattern},
		{name: "partial wildcard", tenant: "tenant-a", exchange: "orders", binding: "tenant-a.orders*", wantErr: ErrInvalidPattern},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			generation, err := authorizer.AuthorizeBinding(test.tenant, test.exchange, test.binding)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("AuthorizeBinding: %v", err)
				}
				if generation == 0 {
					t.Fatal("authorized binding returned generation 0")
				}
				return
			}

			if err == nil {
				t.Fatal("AuthorizeBinding unexpectedly succeeded")
			}
			if !errors.Is(err, ErrDenied) {
				t.Errorf("error %v does not match ErrDenied", err)
			}
			if !errors.Is(err, test.wantErr) {
				t.Errorf("error %v does not match %v", err, test.wantErr)
			}
		})
	}
}

func TestACLGeneration(t *testing.T) {
	t.Parallel()

	authorizer := NewAuthorizer()
	if got := authorizer.Generation(); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}

	generation, err := authorizer.SetRules([]Rule{{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created"},
	}})
	if err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if generation != 1 {
		t.Fatalf("generation after SetRules = %d, want 1", generation)
	}

	generation, err = authorizer.Grant("tenant-a", "orders", "tenant-a.orders.cancelled")
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if generation != 2 {
		t.Fatalf("generation after Grant = %d, want 2", generation)
	}
	if err := authorizer.CheckBinding("tenant-a", "orders", "tenant-a.orders.cancelled"); err != nil {
		t.Fatalf("granted binding denied: %v", err)
	}

	generation, changed := authorizer.RevokePattern("tenant-a", "orders", "tenant-a.orders.cancelled")
	if !changed || generation != 3 {
		t.Fatalf("RevokePattern = (%d, %v), want (3, true)", generation, changed)
	}

	generation, changed = authorizer.Revoke("tenant-a", "missing")
	if changed || generation != 3 {
		t.Fatalf("Revoke missing = (%d, %v), want (3, false)", generation, changed)
	}

	if _, err := authorizer.SetRules([]Rule{{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a..orders"},
	}}); err == nil {
		t.Fatal("SetRules accepted malformed pattern")
	}
	if got := authorizer.Generation(); got != 3 {
		t.Fatalf("invalid SetRules changed generation to %d, want 3", got)
	}
	if err := authorizer.CheckBinding("tenant-a", "orders", "tenant-a.orders.created"); err != nil {
		t.Fatalf("previous ACL did not survive invalid update: %v", err)
	}
}
