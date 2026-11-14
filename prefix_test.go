package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestWatchRejectsEmptyAndCrossTenantPrefix(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()

	if _, err := a.Watch(ctx, "tenantA", "", 0); !errors.Is(err, ErrEmptyPrefix) {
		t.Fatalf("empty prefix: got %v, want ErrEmptyPrefix", err)
	}

	cross := []string{
		"tenantB/",           // another tenant's namespace
		"tenant",             // short prefix spanning both namespaces
		"tenantA/../tenantB", // escape attempt
		"/tenantA/x",         // leading slash outside the namespace
		"tenantA2/",          // sibling namespace with a shared byte prefix
		"tenantA",            // missing trailing boundary, outside "tenantA/"
	}
	for _, prefix := range cross {
		if _, err := a.Watch(ctx, "tenantA", prefix, 0); !errors.Is(err, ErrPrefixOutsideNamespace) {
			t.Fatalf("prefix %q: got %v, want ErrPrefixOutsideNamespace", prefix, err)
		}
	}

	w, err := a.Watch(ctx, "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatalf("namespace-root watch: %v", err)
	}
	w2, err := a.Watch(ctx, "tenantA", "tenantA/orders/", 0)
	if err != nil {
		t.Fatalf("sub-prefix watch: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Watch(ctx, "unknown", "tenantA/", 0); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unknown tenant: got %v, want ErrUnknownTenant", err)
	}
}

func TestNamespaceRegistrationValidation(t *testing.T) {
	a := NewAuthorizer()
	defer a.Close()
	if err := a.RegisterTenant("", []string{"x/"}); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant: %v", err)
	}
	if err := a.RegisterTenant("c", nil); !errors.Is(err, ErrEmptyPrefix) {
		t.Fatalf("empty namespace: %v", err)
	}
	if err := a.RegisterTenant("c", []string{"c/", ""}); !errors.Is(err, ErrEmptyPrefix) {
		t.Fatalf("blank namespace entry: %v", err)
	}
	if err := a.RegisterTenant("c", []string{"c/"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterTenant("c", []string{"c/"}); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("duplicate tenant: %v", err)
	}
}
