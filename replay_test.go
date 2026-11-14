package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestHistoricalReplayTrimmedByPermission(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()

	v1, err := a.Publish("tenantA", OpPut, "tenantA/orders/1", []byte("o1"))
	if err != nil {
		t.Fatal(err)
	}
	if v1 != 1 {
		t.Fatalf("first version = %d, want 1", v1)
	}
	v2, _ := a.Publish("tenantA", OpPut, "tenantA/profile", []byte("p"))
	v3, _ := a.Publish("tenantA", OpDelete, "tenantA/orders/2", nil)
	if _, err := a.Publish("tenantB", OpPut, "tenantB/secret", []byte("s")); err != nil {
		t.Fatal(err)
	}

	root, err := a.Watch(ctx, "tenantA", "tenantA/", v2)
	if err != nil {
		t.Fatalf("replay watch: %v", err)
	}
	got := drain(root.Events())
	if len(got) != 2 || got[0].Key != "tenantA/profile" || got[1].Key != "tenantA/orders/2" {
		t.Fatalf("replay from v2 = %+v", got)
	}
	if got[0].Version != v2 || got[1].Version != v3 || got[1].Op != OpDelete {
		t.Fatalf("replay metadata wrong: %+v", got)
	}

	sub, err := a.Watch(ctx, "tenantA", "tenantA/orders/", 1)
	if err != nil {
		t.Fatalf("sub-prefix replay: %v", err)
	}
	if got := keys(drain(sub.Events())); len(got) != 2 || got[0] != "tenantA/orders/1" || got[1] != "tenantA/orders/2" {
		t.Fatalf("sub-prefix replay = %v", got)
	}

	bw, err := a.Watch(ctx, "tenantB", "tenantB/", 1)
	if err != nil {
		t.Fatalf("tenantB replay: %v", err)
	}
	if got := keys(drain(bw.Events())); len(got) != 1 || got[0] != "tenantB/secret" {
		t.Fatalf("tenantB replay leaked cross-tenant events: %v", got)
	}

	if _, err := a.Watch(ctx, "tenantA", "tenantA/", a.LatestVersion()+1); !errors.Is(err, ErrStartInFuture) {
		t.Fatalf("future start: got %v, want ErrStartInFuture", err)
	}

	// Replay continues seamlessly into live delivery after registration.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/orders/3", []byte("o3")); err != nil {
		t.Fatal(err)
	}
	if got := keys(drain(sub.Events())); len(got) != 1 || got[0] != "tenantA/orders/3" {
		t.Fatalf("live after replay = %v", got)
	}

	root.Close()
	sub.Close()
	bw.Close()
}

func TestReplayStartClampedToRetainedHorizon(t *testing.T) {
	a := newTestAuthorizer(t, WithHistoryCapacity(2))
	ctx := context.Background()

	v1, _ := a.Publish("tenantA", OpPut, "tenantA/1", nil)
	v2, _ := a.Publish("tenantA", OpPut, "tenantA/2", nil)
	v3, _ := a.Publish("tenantA", OpPut, "tenantA/3", nil)

	w, err := a.Watch(ctx, "tenantA", "tenantA/", v1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	got := drain(w.Events())
	if len(got) != 2 || got[0].Version != v2 || got[1].Version != v3 {
		t.Fatalf("clamped replay = %+v", got)
	}
}

func TestReplayTrimmedAfterPermissionNarrowing(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()

	if _, err := a.Publish("tenantA", OpPut, "tenantA/orders/1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish("tenantA", OpPut, "tenantA/profile", nil); err != nil {
		t.Fatal(err)
	}

	if err := a.SetTenantPrefixes("tenantA", []string{"tenantA/orders/"}); err != nil {
		t.Fatal(err)
	}

	w, err := a.Watch(ctx, "tenantA", "tenantA/orders/", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if got := keys(drain(w.Events())); len(got) != 1 || got[0] != "tenantA/orders/1" {
		t.Fatalf("permission-trimmed replay = %v", got)
	}

	if _, err := a.Watch(ctx, "tenantA", "tenantA/profile", 0); !errors.Is(err, ErrPrefixOutsideNamespace) {
		t.Fatalf("watch outside narrowed namespace: got %v", err)
	}
}
