package netsec

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

const (
	regionCN  Region    = "cn-north-1"
	regionUS  Region    = "us-east-1"
	acctA     AccountID = "acct-a"
	acctB     AccountID = "acct-b"
	acctC     AccountID = "acct-c"
	aliasSign AliasName = "signing-key"
)

func testKey(t *testing.T, store *MemoryStore, account AccountID, id KeyID) (PublicKey, Fingerprint) {
	t.Helper()
	pub := PublicKey{
		Account: account,
		ID:      id,
		Region:  regionCN,
		DER:     []byte(fmt.Sprintf("der/%s/%s", account, id)),
	}
	stored, err := store.PutKey(pub)
	if err != nil {
		t.Fatalf("PutKey: %v", err)
	}
	return stored, stored.Fingerprint
}

func mustBind(t *testing.T, store *MemoryStore, ref AliasRef, target Target) Binding {
	t.Helper()
	binding, err := store.Rebind(ref, target)
	if err != nil {
		t.Fatalf("Rebind %s: %v", ref.Name, err)
	}
	return binding
}

func ref(account AccountID, region Region, name AliasName) AliasRef {
	return AliasRef{Account: account, Region: region, Name: name}
}

func keyTarget(id KeyID) Target     { return Target{Kind: TargetKey, Key: id} }
func aliasTarget(r AliasRef) Target { return Target{Kind: TargetAlias, Alias: &r} }

func TestResolveMultiHopChain(t *testing.T) {
	store := NewMemoryStore()
	_, fpA := testKey(t, store, acctA, "key-a")

	entry := ref(acctA, regionCN, aliasSign)
	mid := ref(acctA, regionCN, "rotated-key")
	mustBind(t, store, mid, keyTarget("key-a"))
	firstVersion := mustBind(t, store, entry, aliasTarget(mid))

	caller := NewCallerIdentity("builder-1", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	res, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.FinalFingerprint != fpA {
		t.Fatalf("fingerprint = %x, want %x", res.FinalFingerprint, fpA)
	}
	if res.FinalAccount != acctA || res.FinalKey != "key-a" {
		t.Fatalf("unexpected terminus: %+v", res)
	}
	if res.EntryVersion != firstVersion.Version || len(res.Hops) != 2 {
		t.Fatalf("unexpected hops/version: %+v", res)
	}
	if res.Hops[0].Version != 1 || res.Hops[1].Version != 1 {
		t.Fatalf("versions not recorded per hop: %+v", res.Hops)
	}
}

func TestSameNameDifferentAccountsResolvesDifferentKeys(t *testing.T) {
	store := NewMemoryStore()
	_, fpA := testKey(t, store, acctA, "key-a")
	_, fpB := testKey(t, store, acctB, "key-b")
	if fpA == fpB {
		t.Fatal("test keys must have distinct fingerprints")
	}

	mustBind(t, store, ref(acctA, regionCN, aliasSign), keyTarget("key-a"))
	mustBind(t, store, ref(acctB, regionCN, aliasSign), keyTarget("key-b"))

	caller := NewCallerIdentity("builder-2", []AccountID{acctA, acctB})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	resA, err := txn.Resolve(context.Background(), ref(acctA, regionCN, aliasSign))
	if err != nil {
		t.Fatalf("Resolve A: %v", err)
	}
	resB, err := txn.Resolve(context.Background(), ref(acctB, regionCN, aliasSign))
	if err != nil {
		t.Fatalf("Resolve B: %v", err)
	}
	if resA.FinalFingerprint != fpA || resB.FinalFingerprint != fpB {
		t.Fatalf("alias name treated as global identity: A=%x B=%x",
			resA.FinalFingerprint, resB.FinalFingerprint)
	}
	if resA.FinalFingerprint == resB.FinalFingerprint {
		t.Fatal("same alias name in two accounts must not collapse")
	}
}

func TestSameNameDifferentRegionsAreDistinctRefs(t *testing.T) {
	store := NewMemoryStore()
	keyCN := PublicKey{Account: acctA, ID: "k-cn", Region: regionCN, DER: []byte("der-cn")}
	keyUS := PublicKey{Account: acctA, ID: "k-us", Region: regionUS, DER: []byte("der-us")}
	pubCN, err := store.PutKey(keyCN)
	if err != nil {
		t.Fatal(err)
	}
	pubUS, err := store.PutKey(keyUS)
	if err != nil {
		t.Fatal(err)
	}

	mustBind(t, store, ref(acctA, regionCN, aliasSign), keyTarget("k-cn"))
	mustBind(t, store, ref(acctA, regionUS, aliasSign), keyTarget("k-us"))

	caller := NewCallerIdentity("builder-3", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	resCN, err := txn.Resolve(context.Background(), ref(acctA, regionCN, aliasSign))
	if err != nil {
		t.Fatalf("Resolve CN: %v", err)
	}
	resUS, err := txn.Resolve(context.Background(), ref(acctA, regionUS, aliasSign))
	if err != nil {
		t.Fatalf("Resolve US: %v", err)
	}
	if resCN.FinalFingerprint != pubCN.Fingerprint ||
		resUS.FinalFingerprint != pubUS.Fingerprint {
		t.Fatalf("region scope not honored: cn=%x us=%x",
			resCN.FinalFingerprint, resUS.FinalFingerprint)
	}
}

func TestCycleDetected(t *testing.T) {
	store := NewMemoryStore()
	first := ref(acctA, regionCN, "a1")
	second := ref(acctA, regionCN, "a2")
	third := ref(acctA, regionCN, "a3")
	mustBind(t, store, first, aliasTarget(second))
	mustBind(t, store, second, aliasTarget(third))
	mustBind(t, store, third, aliasTarget(first))

	caller := NewCallerIdentity("builder-cycle", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, err = txn.Resolve(context.Background(), first)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	cycleErr := &CycleError{}
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *CycleError, got %T", err)
	}
	if len(cycleErr.Path) != 4 || cycleErr.Path[0] != first || cycleErr.Path[3] != first {
		t.Fatalf("unexpected cycle path: %+v", cycleErr.Path)
	}
}

func TestSelfLoopDetected(t *testing.T) {
	store := NewMemoryStore()
	entry := ref(acctA, regionCN, "loop")
	mustBind(t, store, entry, aliasTarget(entry))

	caller := NewCallerIdentity("builder-loop", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.Resolve(context.Background(), entry); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
}

func TestMaxHopsEnforced(t *testing.T) {
	store := NewMemoryStore()
	const chainLen = 10
	var first, previous AliasRef
	for i := 0; i < chainLen; i++ {
		current := ref(acctA, regionCN, AliasName(fmt.Sprintf("hop-%d", i)))
		if i == 0 {
			first = current
		} else {
			mustBind(t, store, previous, aliasTarget(current))
		}
		previous = current
	}
	mustBind(t, store, previous, keyTarget("key-a"))
	_, _ = testKey(t, store, acctA, "key-a")

	caller := NewCallerIdentity("builder-limit", []AccountID{acctA})
	resolver := NewResolver(store, WithMaxHops(5))
	txn, err := resolver.Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.Resolve(context.Background(), first); !errors.Is(err, ErrMaxHops) {
		t.Fatalf("err = %v, want ErrMaxHops", err)
	}
}

func TestUnauthorizedCrossAccountRejected(t *testing.T) {
	store := NewMemoryStore()
	_, _ = testKey(t, store, acctC, "key-c")

	entry := ref(acctA, regionCN, aliasSign)
	target := ref(acctC, regionCN, "foreign-key")
	mustBind(t, store, target, keyTarget("key-c"))
	mustBind(t, store, entry, aliasTarget(target))

	caller := NewCallerIdentity("builder-a-only", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}

	_, err = txn.Resolve(context.Background(), entry)
	if !errors.Is(err, ErrAccountNotAuthorized) {
		t.Fatalf("err = %v, want ErrAccountNotAuthorized", err)
	}
	authErr := &AccountAuthError{}
	if !errors.As(err, &authErr) || authErr.Account != acctC {
		t.Fatalf("expected account %s in error, got %v", acctC, err)
	}
}

func TestAuthorizedCrossAccountAllowed(t *testing.T) {
	store := NewMemoryStore()
	_, fpB := testKey(t, store, acctB, "key-b")

	entry := ref(acctA, regionCN, aliasSign)
	target := ref(acctB, regionCN, "b-key")
	mustBind(t, store, target, keyTarget("key-b"))
	mustBind(t, store, entry, aliasTarget(target))

	caller := NewCallerIdentity("builder-ab", []AccountID{acctA, acctB})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	res, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.FinalFingerprint != fpB || res.FinalAccount != acctB {
		t.Fatalf("authorized cross-account hop failed: %+v", res)
	}
}

func TestEntryAccountAuthorizationChecked(t *testing.T) {
	store := NewMemoryStore()
	caller := NewCallerIdentity("builder-b-only", []AccountID{acctB})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	_, err = txn.Resolve(context.Background(), ref(acctA, regionCN, aliasSign))
	if !errors.Is(err, ErrAccountNotAuthorized) {
		t.Fatalf("err = %v, want ErrAccountNotAuthorized", err)
	}
}

func TestAliasAndKeyNotFound(t *testing.T) {
	store := NewMemoryStore()
	caller := NewCallerIdentity("builder", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.Resolve(context.Background(), ref(acctA, regionCN, "missing")); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("err = %v, want ErrAliasNotFound", err)
	}

	dangling := ref(acctA, regionCN, "dangling")
	mustBind(t, store, dangling, keyTarget("ghost"))
	txn2, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn2.Resolve(context.Background(), dangling); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
}

func TestInvalidReferenceRejected(t *testing.T) {
	store := NewMemoryStore()
	caller := NewCallerIdentity("builder", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.Resolve(context.Background(), AliasRef{Account: acctA, Name: aliasSign}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("err = %v, want ErrInvalidReference", err)
	}
}
