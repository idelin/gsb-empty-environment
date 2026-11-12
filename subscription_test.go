package netsec

import (
	"context"
	"errors"
	"net"
	"testing"
)

func newTestRegistry(t *testing.T) (*Registry, *Guard) {
	t.Helper()
	resolver := fakeResolver{hosts: map[string][]net.IPAddr{
		"push.example.com": {mustIPAddr(t, "93.184.216.34")},
		"intranet.local":   {mustIPAddr(t, "10.0.0.5")},
	}}
	guard := testGuard(t, resolver, "push.example.com", "intranet.local")
	return NewRegistry(guard), guard
}

func TestRegistryCreateBindsTenantAndFingerprint(t *testing.T) {
	registry, _ := newTestRegistry(t)
	fingerprint := fingerprintForTest(t)

	sub, err := registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://push.example.com/push/aaa",
		VAPIDFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if sub.ID == "" || sub.TenantID != "tenant-a" || sub.VAPIDFingerprint != fingerprint {
		t.Fatalf("subscription not bound correctly: %+v", sub)
	}

	got, err := registry.Get("tenant-a", sub.ID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if got.Endpoint.String() != "https://push.example.com/push/aaa" {
		t.Fatalf("endpoint = %q", got.Endpoint.String())
	}
}

func TestRegistryRejectsIntranetEndpoint(t *testing.T) {
	registry, _ := newTestRegistry(t)
	_, err := registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://intranet.local/push",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	assertErrorIs(t, err, ErrReservedIP)
}

func TestRegistryTenantIsolation(t *testing.T) {
	registry, _ := newTestRegistry(t)
	sub, err := registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://push.example.com/push/aaa",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 跨租户读取统一按“不存在”处理，避免泄漏订阅存在性。
	if _, err := registry.Get("tenant-b", sub.ID); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("cross-tenant get: error = %v, want ErrUnknownSubscription", err)
	}
	if err := registry.Remove("tenant-b", sub.ID); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("cross-tenant remove: error = %v, want ErrUnknownSubscription", err)
	}
	if err := registry.Remove("tenant-a", sub.ID); err != nil {
		t.Fatalf("owner remove: %v", err)
	}
	if _, err := registry.Get("tenant-a", sub.ID); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("subscription should be gone: %v", err)
	}
}

func TestRegistryRejectsMissingBinding(t *testing.T) {
	registry, _ := newTestRegistry(t)
	if _, err := registry.Create(context.Background(), CreateSubscriptionInput{
		RawEndpoint:      "https://push.example.com/push",
		VAPIDFingerprint: fingerprintForTest(t),
	}); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("missing tenant: %v", err)
	}
	if _, err := registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:    "tenant-a",
		RawEndpoint: "https://push.example.com/push",
	}); !errors.Is(err, ErrEmptyVAPIDFingerprint) {
		t.Fatalf("missing fingerprint: %v", err)
	}
}

func fingerprintForTest(t *testing.T) [32]byte {
	t.Helper()
	credentials, err := NewVAPIDCredentials("mailto:ops@example.com")
	if err != nil {
		t.Fatalf("new credentials: %v", err)
	}
	fingerprint, err := VAPIDFingerprint(&credentials.PrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fingerprint
}
