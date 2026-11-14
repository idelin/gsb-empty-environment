package netsec

import (
	"errors"
	"testing"
)

func TestAuthorizeRejectsEmptyAndMalformedInputs(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead|ActionWrite, KindTopic, TypePrefix, "tenant-a"),
	})

	cases := []struct {
		name      string
		principal string
		resource  Resource
		actions   Action
	}{
		{"empty principal", "", Resource{Kind: KindTopic, Name: "tenant-a"}, ActionRead},
		{"empty name", "p", Resource{Kind: KindTopic, Name: ""}, ActionRead},
		{"leading delim", "p", Resource{Kind: KindTopic, Name: "-tenant-a"}, ActionRead},
		{"unknown kind", "p", Resource{Kind: ResourceKind(7), Name: "tenant-a"}, ActionRead},
		{"zero action", "p", Resource{Kind: KindTopic, Name: "tenant-a"}, 0},
		{"unknown action bit", "p", Resource{Kind: KindTopic, Name: "tenant-a"}, Action(1 << 7)},
	}
	for _, c := range cases {
		if err := a.Authorize(c.principal, c.resource, c.actions); !errors.Is(err, ErrNotAuthorized) {
			t.Errorf("%s: got %v, want ErrNotAuthorized", c.name, err)
		}
	}
}

func TestReverifyErrorWrapsSentinel(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL(tenantACL())
	_ = a.BindGroup("tenant-a", "tenant-a-group")
	sub, err := a.OpenSubscription("tenant-a", "tenant-a-orders", "tenant-a-group")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Revoke by switching to the other tenant's rules only.
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindTopic, TypePrefix, "tenant-b"),
		allowEntry("tenant-b", ActionRead|ActionAdmin, KindGroup, TypePrefix, "tenant-b"),
	})
	err = a.Reverify(sub)
	if !errors.Is(err, ErrSubscriptionStale) {
		t.Fatalf("got %v, want ErrSubscriptionStale", err)
	}
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("stale error should also expose ErrNotAuthorized, got %v", err)
	}

	// Losing group ownership surfaces the same stale sentinel.
	if err := a.ReleaseGroup("tenant-a", "tenant-a-group"); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, _ = a.ReplaceACL(tenantACL())
	if err := a.Reverify(sub); !errors.Is(err, ErrSubscriptionStale) {
		t.Errorf("ownership loss = %v, want stale", err)
	}
}

func TestTransactionAndGroupKindIsolation(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionWrite, KindTransaction, TypeLiteral, "tx-1"),
		allowEntry("p", ActionAdmin, KindGroup, TypeLiteral, "grp-1"),
	})
	if err := a.AuthorizeTransaction("p", "tx-1", ActionWrite); err != nil {
		t.Errorf("tx write: %v", err)
	}
	// A transaction rule never authorizes the same-named group/topic.
	if err := a.AuthorizeGroup("p", "tx-1", ActionAdmin); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("tx rule leaked into group kind: %v", err)
	}
	if err := a.AuthorizeTopic("p", "grp-1", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("group rule leaked into topic kind: %v", err)
	}
}

func TestBindGroupRejectsMalformedNames(t *testing.T) {
	a := NewAuthorizer('-')
	if err := a.BindGroup("p", ""); !errors.Is(err, ErrEmptyName) {
		t.Errorf("empty bind = %v", err)
	}
	if err := a.BindGroup("", "g"); !errors.Is(err, ErrEmptyPrincipal) {
		t.Errorf("empty principal bind = %v", err)
	}
	if err := a.BindGroup("p", "bad--name"); err == nil {
		t.Error("doubled delimiter group name should be rejected")
	}
}
