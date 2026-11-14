package netsec

import (
	"errors"
	"testing"
)

func tenantACL() []Entry {
	return []Entry{
		allowEntry("tenant-a", ActionRead|ActionAdmin, KindTopic, TypePrefix, "tenant-a"),
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindTopic, TypePrefix, "tenant-b"),
		allowEntry("tenant-a", ActionRead|ActionAdmin, KindGroup, TypePrefix, "tenant-a"),
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindGroup, TypePrefix, "tenant-b"),
		allowEntry("tenant-a", ActionWrite, KindTransaction, TypePrefix, "tenant-a"),
	}
}

func TestGroupOwnershipCannotBeReusedAcrossTenants(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL(tenantACL())

	if err := a.BindGroup("tenant-a", "tenant-a-consumers"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Same owner re-binding is idempotent.
	if err := a.BindGroup("tenant-a", "tenant-a-consumers"); err != nil {
		t.Errorf("idempotent rebind: %v", err)
	}
	// Another tenant cannot take the same name.
	err := a.BindGroup("tenant-b", "tenant-a-consumers")
	if !errors.Is(err, ErrGroupOwnerConflict) {
		t.Errorf("cross-tenant bind = %v, want conflict", err)
	}

	// Rename within the same tenant works and keeps the boundary.
	if err := a.RenameGroup("tenant-a", "tenant-a-consumers", "tenant-a-consumers-v2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, ok := a.GroupOwner("tenant-a-consumers"); ok {
		t.Error("old name still bound after rename")
	}
	if owner, ok := a.GroupOwner("tenant-a-consumers-v2"); !ok || owner != "tenant-a" {
		t.Errorf("new name owner = %q %v", owner, ok)
	}

	// Renaming onto a name owned by another tenant is rejected.
	_ = a.BindGroup("tenant-b", "tenant-b-consumers")
	err = a.RenameGroup("tenant-a", "tenant-a-consumers-v2", "tenant-b-consumers")
	if !errors.Is(err, ErrGroupOwnerConflict) {
		t.Errorf("rename onto foreign group = %v, want conflict", err)
	}

	// A foreign tenant cannot rename or release a group it does not own.
	if err := a.RenameGroup("tenant-b", "tenant-a-consumers-v2", "tenant-b-x"); !errors.Is(err, ErrGroupNotFound) {
		t.Errorf("foreign rename = %v, want not found for caller", err)
	}
	if err := a.ReleaseGroup("tenant-b", "tenant-a-consumers-v2"); !errors.Is(err, ErrGroupOwnerConflict) {
		t.Errorf("foreign release = %v, want conflict", err)
	}
}

func TestRenameRequiresCurrentGenerationAuthorization(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL(tenantACL())
	_ = a.BindGroup("tenant-a", "tenant-a-g1")

	// Revoke group admin for tenant-a.
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("tenant-a", ActionRead, KindGroup, TypePrefix, "tenant-a"),
	})
	if err := a.RenameGroup("tenant-a", "tenant-a-g1", "tenant-a-g2"); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("rename after revoke = %v, want not authorized", err)
	}
	if _, ok := a.GroupOwner("tenant-a-g1"); !ok {
		t.Error("failed rename must leave the original binding intact")
	}
}

func TestSubscriptionLifecycleAndReverification(t *testing.T) {
	a := NewAuthorizer('-')
	gen, _ := a.ReplaceACL(tenantACL())
	_ = a.BindGroup("tenant-a", "tenant-a-group")

	sub, err := a.OpenSubscription("tenant-a", "tenant-a-orders", "tenant-a-group")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sub.Generation() != gen {
		t.Errorf("sub generation %d != %d", sub.Generation(), gen)
	}

	// Same-tenant subscription stays inside its boundary.
	if err := a.Reverify(sub); err != nil {
		t.Errorf("reverify unchanged ACL: %v", err)
	}

	// A subscription on a cross-tenant topic can never be opened.
	if _, err := a.OpenSubscription("tenant-a", "tenant-b-secret", "tenant-a-group"); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("cross-tenant open = %v, want not authorized", err)
	}
	// Using another tenant's group is blocked by ownership.
	_ = a.BindGroup("tenant-b", "tenant-b-group")
	if _, err := a.OpenSubscription("tenant-a", "tenant-a-orders", "tenant-b-group"); !errors.Is(err, ErrGroupOwnerConflict) {
		t.Errorf("foreign group open = %v, want conflict", err)
	}

	// After the ACL generation changes, the in-flight subscription is
	// re-checked and forced closed when the rule disappears.
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindTopic, TypePrefix, "tenant-b"),
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindGroup, TypePrefix, "tenant-b"),
	})
	if err := a.Reverify(sub); !errors.Is(err, ErrSubscriptionStale) {
		t.Errorf("reverify after revocation = %v, want stale", err)
	}

	// Restoring access lets a replacement subscription verify again.
	_, _ = a.ReplaceACL(tenantACL())
	sub2, err := a.OpenSubscription("tenant-a", "tenant-a-orders", "tenant-a-group")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := a.Reverify(sub2); err != nil {
		t.Errorf("reverify restored: %v", err)
	}
}

func TestLegitimateSameTenantTrafficStaysInsideBoundary(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL(tenantACL())

	allowed := []struct {
		principal string
		resource  Resource
		action    Action
	}{
		{"tenant-a", Resource{Kind: KindTopic, Name: "tenant-a"}, ActionRead},
		{"tenant-a", Resource{Kind: KindTopic, Name: "tenant-a-orders-1"}, ActionRead | ActionAdmin},
		{"tenant-a", Resource{Kind: KindGroup, Name: "tenant-a-group"}, ActionAdmin},
		{"tenant-a", Resource{Kind: KindTransaction, Name: "tenant-a-tx-1"}, ActionWrite},
		{"tenant-b", Resource{Kind: KindTopic, Name: "tenant-b-secret"}, ActionRead},
	}
	for _, c := range allowed {
		if err := a.Authorize(c.principal, c.resource, c.action); err != nil {
			t.Errorf("legit %+v denied: %v", c, err)
		}
	}

	denied := []struct {
		principal string
		resource  Resource
	}{
		// Similar prefixes must not cross tenant lines; matching is segment
		// boundary based and case sensitive.
		{"tenant-a", Resource{Kind: KindTopic, Name: "tenant-ab-x"}},
		{"tenant-a", Resource{Kind: KindGroup, Name: "tenant-b-group"}},
		{"tenant-a", Resource{Kind: KindTransaction, Name: "tenant-b-tx"}},
		{"tenant-b", Resource{Kind: KindTopic, Name: "tenant-a"}},
		{"tenant-b", Resource{Kind: KindTopic, Name: "tenant-a-secret"}},
		// Case changes must not bypass matching.
		{"tenant-a", Resource{Kind: KindTopic, Name: "TENANT-A-orders"}},
	}
	for _, c := range denied {
		if err := a.Authorize(c.principal, c.resource, ActionRead); !errors.Is(err, ErrNotAuthorized) {
			t.Errorf("cross-boundary %+v = %v, want deny", c, err)
		}
	}
}
