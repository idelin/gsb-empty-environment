package netsec

import (
	"strings"
	"testing"
)

func TestErrorStringsAndSentinels(t *testing.T) {
	entry := ref(acctA, regionCN, "a1")
	other := ref(acctA, regionCN, "a2")
	cycle := &CycleError{Path: []AliasRef{entry, other, entry}}
	if !strings.Contains(cycle.Error(), ErrCycle.Error()) ||
		!strings.Contains(cycle.Error(), "a1 -> acct-a/cn-north-1/a2 -> acct-a/cn-north-1/a1") {
		t.Fatalf("unexpected cycle error: %q", cycle.Error())
	}

	auth := &AccountAuthError{Account: acctC, Ref: other}
	if !strings.Contains(auth.Error(), ErrAccountNotAuthorized.Error()) ||
		!strings.Contains(auth.Error(), string(acctC)) {
		t.Fatalf("unexpected auth error: %q", auth.Error())
	}
}

func TestStoreRejectsMalformedBindingsAndKeys(t *testing.T) {
	store := NewMemoryStore()

	if _, err := store.Rebind(AliasRef{Account: acctA, Name: "no-region"}, keyTarget("k")); err != ErrInvalidReference {
		t.Fatalf("err = %v, want ErrInvalidReference", err)
	}
	validRef := ref(acctA, regionCN, "bad-target")
	if _, err := store.Rebind(validRef, Target{Kind: TargetAlias}); err != ErrInvalidBinding {
		t.Fatalf("err = %v, want ErrInvalidBinding", err)
	}
	if _, err := store.Rebind(validRef, Target{Kind: TargetKey}); err != ErrInvalidBinding {
		t.Fatalf("err = %v, want ErrInvalidBinding", err)
	}
	if _, err := store.Rebind(validRef, Target{Kind: "bogus"}); err != ErrInvalidBinding {
		t.Fatalf("err = %v, want ErrInvalidBinding", err)
	}

	if _, err := store.PutKey(PublicKey{Account: acctA}); err != ErrInvalidKey {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}

	pub, err := store.PutKey(PublicKey{Account: acctA, ID: "k", Fingerprint: "fp-only"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := store.LookupKey(acctA, "k"); !ok || got.Fingerprint != pub.Fingerprint {
		t.Fatalf("LookupKey returned %+v ok=%v", got, ok)
	}
	if _, ok := store.LookupKey(acctA, "missing"); ok {
		t.Fatal("LookupKey unexpectedly found missing key")
	}

	mutated := pub
	mutated.DER = []byte("inspection-copy")
	if _, ok := store.LookupKey(acctA, "k"); !ok {
		t.Fatal("key disappeared")
	}
}

func TestSnapshotDefendsAgainstCallerMutation(t *testing.T) {
	store := NewMemoryStore()
	entry := ref(acctA, regionCN, aliasSign)
	next := ref(acctA, regionCN, "next")
	mustBind(t, store, next, keyTarget("k"))
	mustBind(t, store, entry, aliasTarget(next))

	snap, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := snap.LookupAlias(entry)
	if !ok {
		t.Fatal("snapshot missing binding")
	}
	binding.Target.Alias.Name = "mutated"

	again, ok := snap.LookupAlias(entry)
	if !ok || again.Target.Alias.Name != "next" {
		t.Fatalf("snapshot was mutable through returned binding: %+v", again)
	}
}
