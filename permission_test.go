package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestPermissionChangeRevokesAndFilters(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()

	orders, err := a.Watch(ctx, "tenantA", "tenantA/orders/", 0)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := a.Watch(ctx, "tenantA", "tenantA/profile", 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := a.SetTenantPrefixes("tenantA", []string{"tenantA/orders/"}); err != nil {
		t.Fatal(err)
	}

	if _, ok := <-profile.Events(); ok {
		t.Fatal("revoked watch channel should be closed")
	}
	var revoked *ErrPermissionRevoked
	if !errors.As(profile.Err(), &revoked) {
		t.Fatalf("revoked watch err = %v, want *ErrPermissionRevoked", profile.Err())
	}
	if revoked.Tenant != "tenantA" || revoked.Prefix != "tenantA/profile" {
		t.Fatalf("revocation detail = %+v", revoked)
	}
	if count, _ := a.WatchCount("tenantA"); count != 1 {
		t.Fatalf("watch count after revoke = %d, want 1", count)
	}

	// Surviving watch still gets events inside the new namespace.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/orders/9", nil); err != nil {
		t.Fatal(err)
	}
	if got := keys(drain(orders.Events())); len(got) != 1 || got[0] != "tenantA/orders/9" {
		t.Fatalf("surviving watch = %v", got)
	}

	// Publishing outside the narrowed namespace is now rejected.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/profile", nil); !errors.Is(err, ErrPrefixOutsideNamespace) {
		t.Fatalf("post-change publish: got %v", err)
	}

	// Widening permissions again allows new watches but does not revive
	// the terminated one.
	if err := a.SetTenantPrefixes("tenantA", []string{"tenantA/"}); err != nil {
		t.Fatal(err)
	}
	if profile.Err() == nil {
		t.Fatal("terminated watch must stay terminated")
	}
	orders.Close()
}

func TestAuthorizerCloseTerminatesWatches(t *testing.T) {
	a := newTestAuthorizer(t)
	w, err := a.Watch(context.Background(), "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-w.Events(); ok {
		t.Fatal("watch channel should be closed after authorizer close")
	}
	if !errors.Is(w.Err(), ErrAuthorizerClosed) {
		t.Fatalf("watch err = %v, want ErrAuthorizerClosed", w.Err())
	}
	if _, err := a.Publish("tenantA", OpPut, "tenantA/x", nil); !errors.Is(err, ErrAuthorizerClosed) {
		t.Fatalf("publish after close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}
