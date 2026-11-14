package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLegalWatchReceivesOnlyAuthorizedKeys(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()

	root, err := a.Watch(ctx, "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatalf("root watch: %v", err)
	}
	sub, err := a.Watch(ctx, "tenantA", "tenantA/orders/", 0)
	if err != nil {
		t.Fatalf("sub-prefix watch: %v", err)
	}
	other, err := a.Watch(ctx, "tenantB", "tenantB/", 0)
	if err != nil {
		t.Fatalf("tenantB watch: %v", err)
	}

	if _, err := a.Publish("tenantA", OpPut, "tenantA/orders/1", []byte("o1")); err != nil {
		t.Fatalf("publish order: %v", err)
	}
	if _, err := a.Publish("tenantA", OpPut, "tenantA/profile", []byte("p")); err != nil {
		t.Fatalf("publish profile: %v", err)
	}
	if _, err := a.Publish("tenantB", OpPut, "tenantB/secret", []byte("s")); err != nil {
		t.Fatalf("publish tenantB: %v", err)
	}

	if got := keys(drain(root.Events())); len(got) != 2 || got[0] != "tenantA/orders/1" || got[1] != "tenantA/profile" {
		t.Fatalf("root watch keys = %v", got)
	}
	if got := keys(drain(sub.Events())); len(got) != 1 || got[0] != "tenantA/orders/1" {
		t.Fatalf("sub-prefix watch keys = %v", got)
	}
	if got := keys(drain(other.Events())); len(got) != 1 || got[0] != "tenantB/secret" {
		t.Fatalf("tenantB watch keys = %v", got)
	}

	if _, err := a.Publish("tenantA", OpPut, "tenantB/leak", nil); !errors.Is(err, ErrPrefixOutsideNamespace) {
		t.Fatalf("cross-tenant publish: got %v, want ErrPrefixOutsideNamespace", err)
	}
	if got := drain(root.Events()); len(got) != 0 {
		t.Fatalf("rejected publish leaked %d events", len(got))
	}

	root.Close()
	sub.Close()
	other.Close()
	if count, _ := a.WatchCount("tenantA"); count != 0 {
		t.Fatalf("watch count after close = %d, want 0", count)
	}
}

func TestWatchContextCancellation(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx, cancel := context.WithCancel(context.Background())

	w, err := a.Watch(ctx, "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	deadline := make(chan struct{})
	go func() {
		for range w.Events() {
		}
		close(deadline)
	}()
	select {
	case <-deadline:
	case <-time.After(time.Second):
		t.Fatal("watch channel was not closed after context cancellation")
	}
	if w.Err() == nil {
		t.Fatal("canceled watch should report context error")
	}
	if count, _ := a.WatchCount("tenantA"); count != 0 {
		t.Fatalf("watch count after cancel = %d", count)
	}
}
