package netsec

import (
	"errors"
	"sync"
	"testing"
)

func allowEntry(principal string, actions Action, kind ResourceKind, typ PatternType, value string) Entry {
	return Entry{
		Principal: principal,
		Effect:    EffectAllow,
		Actions:   actions,
		Pattern:   Pattern{Kind: kind, Type: typ, Value: value},
	}
}

func denyEntry(principal string, actions Action, kind ResourceKind, typ PatternType, value string) Entry {
	return Entry{
		Principal: principal,
		Effect:    EffectDeny,
		Actions:   actions,
		Pattern:   Pattern{Kind: kind, Type: typ, Value: value},
	}
}

func TestHeadlineTenantPrefixAttackBlocked(t *testing.T) {
	a := NewAuthorizer('-')
	gen, err := a.ReplaceACL([]Entry{
		// Attacker's legitimate tenancy is exactly the "tenant-a" topic and
		// its own sub-namespace. A literal cannot reach a longer string.
		allowEntry("attacker", ActionRead, KindTopic, TypeLiteral, "tenant-a"),
		allowEntry("attacker", ActionRead, KindTopic, TypePrefix, "tenant-a-app"),
		// Victim owns its own secret topic.
		allowEntry("victim", ActionRead, KindTopic, TypePrefix, "tenant-a-secret"),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if gen != 1 {
		t.Fatalf("first generation = %d, want 1", gen)
	}

	if err := a.AuthorizeTopic("attacker", "tenant-a", ActionRead); err != nil {
		t.Errorf("legitimate own topic denied: %v", err)
	}
	if err := a.AuthorizeTopic("attacker", "tenant-a-app-queue", ActionRead); err != nil {
		t.Errorf("legitimate sub-namespace denied: %v", err)
	}
	err = a.AuthorizeTopic("attacker", "tenant-a-secret", ActionRead)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("cross-tenant read = %v, want ErrNotAuthorized", err)
	}
	// Similar-prefix names never match regardless of the rule shape.
	if err := a.AuthorizeTopic("attacker", "tenant-aapp-x", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("similar prefix leaked: %v", err)
	}
	if err := a.AuthorizeTopic("victim", "tenant-a-secret", ActionRead); err != nil {
		t.Errorf("same-tenant victim read denied: %v", err)
	}
}

func TestSegmentBoundaryReplacesStringContains(t *testing.T) {
	// Reproduces the exact vulnerable rule shape and proves the matching
	// engine itself never does a bare string-contains comparison. A broad
	// prefix matches only on a delimiter boundary, never on "tenant-ab" or
	// other similar strings; an explicit deny still wins over the allow.
	a := NewAuthorizer('-')
	_, err := a.ReplaceACL([]Entry{
		allowEntry("attacker", ActionRead, KindTopic, TypePrefix, "tenant-a-"),
		denyEntry("attacker", ActionRead, KindTopic, TypeLiteral, "tenant-a-secret"),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := a.AuthorizeTopic("attacker", "tenant-a-orders", ActionRead); err != nil {
		t.Errorf("boundary child should be allowed: %v", err)
	}
	if err := a.AuthorizeTopic("attacker", "tenant-a", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("namespace-only prefix matched the bare name: %v", err)
	}
	if err := a.AuthorizeTopic("attacker", "tenant-ab", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("string-contains style match leaked: %v", err)
	}
	if err := a.AuthorizeTopic("attacker", "tenant-a-secret", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("deny did not win over broad prefix: %v", err)
	}
}

func TestDenyWinsOverAllow(t *testing.T) {
	a := NewAuthorizer('-')
	_, err := a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypePrefix, "tenant-a"),
		denyEntry("p", ActionRead, KindTopic, TypeLiteral, "tenant-a-secret"),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := a.AuthorizeTopic("p", "tenant-a-orders", ActionRead); err != nil {
		t.Errorf("allowed prefix scope denied: %v", err)
	}
	if err := a.AuthorizeTopic("p", "tenant-a-secret", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("deny entry not honored: %v", err)
	}
}

func TestDefaultDenyAndActionCoverage(t *testing.T) {
	a := NewAuthorizer('-')
	if _, err := a.ReplaceACL(nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := a.AuthorizeTopic("p", "tenant-a", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("empty ACL should default deny, got %v", err)
	}

	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypeLiteral, "t"),
	})
	if err := a.AuthorizeTopic("p", "t", ActionRead); err != nil {
		t.Errorf("read should be allowed: %v", err)
	}
	// Read-only grant must not cover write.
	if err := a.AuthorizeTopic("p", "t", ActionWrite); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("write should be denied by read-only grant: %v", err)
	}
	if err := a.AuthorizeTopic("p", "t", ActionRead|ActionWrite); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("combined action must require full coverage: %v", err)
	}
}

func TestInvalidACLRejectedAtomically(t *testing.T) {
	a := NewAuthorizer('-')
	gen, err := a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypeLiteral, "good"),
	})
	if err != nil || gen != 1 {
		t.Fatalf("seed replace gen=%d err=%v", gen, err)
	}

	_, err = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypeLiteral, "good"),
		allowEntry("", ActionRead, KindTopic, TypeLiteral, "bad"),
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry, got %v", err)
	}
	if a.Generation() != 1 {
		t.Errorf("generation advanced after failed commit: %d", a.Generation())
	}
	if err := a.AuthorizeTopic("p", "good", ActionRead); err != nil {
		t.Errorf("old ACL no longer in force after failed commit: %v", err)
	}

	_, err = a.ReplaceACL([]Entry{
		{Principal: "p", Effect: Effect(99), Actions: ActionRead, Pattern: Pattern{Kind: KindTopic, Type: TypeLiteral, Value: "x"}},
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("bad effect: %v", err)
	}
}

func TestGenerationBumpReverifiesAndCacheExpires(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypePrefix, "tenant-a"),
	})

	// Prime the cache (both directions).
	if err := a.AuthorizeTopic("p", "tenant-a-orders", ActionRead); err != nil {
		t.Fatalf("initial allow: %v", err)
	}
	if err := a.AuthorizeTopic("p", "tenant-b-x", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("initial deny expected, got %v", err)
	}

	// New generation revokes access entirely.
	gen2, err := a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypeLiteral, "other"),
	})
	if err != nil || gen2 != 2 {
		t.Fatalf("second replace gen=%d err=%v", gen2, err)
	}

	// A cached allow must not survive the generation bump.
	if err := a.AuthorizeTopic("p", "tenant-a-orders", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("stale cached allow extended past generation: %v", err)
	}

	// An in-flight subscription opened under gen 1 must fail re-verification.
	sub := &Subscription{
		principal:  "p",
		topic:      Resource{Kind: KindTopic, Name: "tenant-a-orders"},
		generation: 1,
	}
	if err := a.Reverify(sub); !errors.Is(err, ErrSubscriptionStale) {
		t.Errorf("reverify after revoke = %v, want stale", err)
	}

	// Re-grant and confirm the subscription reverifies cleanly and tracks the
	// new generation.
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypePrefix, "tenant-a"),
	})
	if err := a.Reverify(sub); err != nil {
		t.Errorf("reverify after re-grant: %v", err)
	}
	if sub.Generation() != a.Generation() {
		t.Errorf("subscription generation %d != authorizer %d", sub.Generation(), a.Generation())
	}
}

func TestCacheDeniesAlsoExpire(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL(nil)
	// Deny cached under gen 1.
	if err := a.AuthorizeTopic("p", "t", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("expected initial deny: %v", err)
	}
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypeLiteral, "t"),
	})
	if err := a.AuthorizeTopic("p", "t", ActionRead); err != nil {
		t.Errorf("cached deny extended past generation: %v", err)
	}
}

func TestConcurrentACLUpdatesAndChecks(t *testing.T) {
	a := NewAuthorizer('-')
	_, _ = a.ReplaceACL([]Entry{
		allowEntry("p", ActionRead, KindTopic, TypePrefix, "tenant-a"),
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers keep committing new generations.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				entries := []Entry{
					allowEntry("p", ActionRead, KindTopic, TypePrefix, "tenant-a"),
				}
				if i%2 == 0 {
					entries = append(entries, denyEntry("p", ActionRead, KindTopic, TypeLiteral, "tenant-a-secret"))
				}
				if _, err := a.ReplaceACL(entries); err != nil {
					t.Errorf("replace: %v", err)
					return
				}
			}
		}()
	}

	// Readers continuously check; the cross-tenant secret must never be read
	// while a deny entry is present, and no data race/panic may occur.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = a.AuthorizeTopic("p", "tenant-a-orders", ActionRead)
				_ = a.AuthorizeTopic("p", "tenant-a-secret", ActionRead)
			}
		}()
	}

	// A re-verifier exercises the subscription path concurrently as well.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sub := &Subscription{
			principal:  "p",
			topic:      Resource{Kind: KindTopic, Name: "tenant-a-orders"},
			generation: a.Generation(),
		}
		for i := 0; i < 200; i++ {
			_ = a.Reverify(sub)
		}
	}()

	// Readers poll the stop channel on every iteration; closing it lets the
	// whole WaitGroup drain once the writers finish their work.
	close(stop)
	wg.Wait()

	// Final invariant: with an even last iteration the secret is denied.
	// Regardless of parity, the similar-prefix tenant-b name never matches.
	if err := a.AuthorizeTopic("p", "tenant-b-secret", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("tenant boundary crossed: %v", err)
	}
}
