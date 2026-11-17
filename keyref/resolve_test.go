package keyref

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func newPublicKeyDER(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return der
}

func ref(account, region, alias string) Ref {
	return Ref{Account: account, Region: region, Alias: alias}
}

func aliasTarget(account, region, alias string) AliasTarget {
	r := ref(account, region, alias)
	return AliasTarget{Alias: &r}
}

func keyTarget(keyID string) AliasTarget {
	return AliasTarget{KeyID: keyID}
}

// 同名异钥: the same alias name in different accounts resolves to
// different key material; the alias is never a global identity.
func TestSameAliasDifferentAccounts(t *testing.T) {
	store := NewStore()
	keyA := store.PutKey("acct-a", "cn-north-1", "key-1", newPublicKeyDER(t))
	keyB := store.PutKey("acct-b", "cn-north-1", "key-1", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "release-signer"), keyTarget("key-1"))
	store.BindAlias(ref("acct-b", "cn-north-1", "release-signer"), keyTarget("key-1"))

	r := NewResolver(store, Policy{})
	tx := store.Begin()

	resA, err := r.Resolve(tx, ref("acct-a", "cn-north-1", "release-signer"))
	if err != nil {
		t.Fatalf("resolve acct-a: %v", err)
	}
	resB, err := r.Resolve(tx, ref("acct-b", "cn-north-1", "release-signer"))
	if err != nil {
		t.Fatalf("resolve acct-b: %v", err)
	}

	if resA.Fingerprint == resB.Fingerprint {
		t.Fatal("same alias in different accounts must not collapse to one identity")
	}
	if resA.Fingerprint != Fingerprint(keyA.PublicKey) {
		t.Fatalf("acct-a fingerprint mismatch: got %s", resA.Fingerprint)
	}
	if resB.Fingerprint != Fingerprint(keyB.PublicKey) {
		t.Fatalf("acct-b fingerprint mismatch: got %s", resB.Fingerprint)
	}
	if resA.Account != "acct-a" || resB.Account != "acct-b" {
		t.Fatalf("resolution must stay bound to the requesting account: %s, %s",
			resA.Account, resB.Account)
	}
}

// 多跳: alias -> alias -> alias -> key within one account.
func TestMultiHopChainSameAccount(t *testing.T) {
	store := NewStore()
	key := store.PutKey("acct-a", "cn-north-1", "key-root", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "c"), keyTarget("key-root"))
	store.BindAlias(ref("acct-a", "cn-north-1", "b"), aliasTarget("acct-a", "cn-north-1", "c"))
	store.BindAlias(ref("acct-a", "cn-north-1", "a"), aliasTarget("acct-a", "cn-north-1", "b"))

	r := NewResolver(store, Policy{})
	res, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "a"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Fingerprint != Fingerprint(key.PublicKey) {
		t.Fatalf("fingerprint mismatch: got %s", res.Fingerprint)
	}
	if len(res.Chain) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(res.Chain))
	}
	if res.AliasVersion != 1 {
		t.Fatalf("expected first-hop version 1, got %d", res.AliasVersion)
	}
}

// 跨账号: allowed only with an explicit grant.
func TestCrossAccountAuthorization(t *testing.T) {
	build := func() *Store {
		store := NewStore()
		store.PutKey("acct-b", "cn-north-1", "key-9", newPublicKeyDER(t))
		store.BindAlias(ref("acct-b", "cn-north-1", "shared"), keyTarget("key-9"))
		store.BindAlias(ref("acct-a", "cn-north-1", "delegated"),
			aliasTarget("acct-b", "cn-north-1", "shared"))
		return store
	}

	t.Run("rejected without grant", func(t *testing.T) {
		store := build()
		r := NewResolver(store, Policy{})
		_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "delegated"))
		if !errors.Is(err, ErrCrossAccount) {
			t.Fatalf("expected ErrCrossAccount, got %v", err)
		}
	})

	t.Run("allowed with grant", func(t *testing.T) {
		store := build()
		r := NewResolver(store, Policy{Grants: map[string][]string{"acct-a": {"acct-b"}}})
		res, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "delegated"))
		if err != nil {
			t.Fatalf("resolve with grant: %v", err)
		}
		if res.Account != "acct-b" {
			t.Fatalf("expected terminal account acct-b, got %s", res.Account)
		}
	})
}

// 环: cycles must be detected instead of looping forever.
func TestAliasCycle(t *testing.T) {
	t.Run("two-node ring", func(t *testing.T) {
		store := NewStore()
		store.BindAlias(ref("acct-a", "cn-north-1", "x"), aliasTarget("acct-a", "cn-north-1", "y"))
		store.BindAlias(ref("acct-a", "cn-north-1", "y"), aliasTarget("acct-a", "cn-north-1", "x"))
		r := NewResolver(store, Policy{})
		_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "x"))
		if !errors.Is(err, ErrAliasCycle) {
			t.Fatalf("expected ErrAliasCycle, got %v", err)
		}
	})

	t.Run("self loop", func(t *testing.T) {
		store := NewStore()
		store.BindAlias(ref("acct-a", "cn-north-1", "x"), aliasTarget("acct-a", "cn-north-1", "x"))
		r := NewResolver(store, Policy{})
		_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "x"))
		if !errors.Is(err, ErrAliasCycle) {
			t.Fatalf("expected ErrAliasCycle, got %v", err)
		}
	})

	t.Run("cross-account ring with mutual grants", func(t *testing.T) {
		store := NewStore()
		store.BindAlias(ref("acct-a", "cn-north-1", "x"), aliasTarget("acct-b", "cn-north-1", "x"))
		store.BindAlias(ref("acct-b", "cn-north-1", "x"), aliasTarget("acct-a", "cn-north-1", "x"))
		r := NewResolver(store, Policy{Grants: map[string][]string{
			"acct-a": {"acct-b"},
			"acct-b": {"acct-a"},
		}})
		_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "x"))
		if !errors.Is(err, ErrAliasCycle) {
			t.Fatalf("expected ErrAliasCycle, got %v", err)
		}
	})
}

// 同一事务固定: rebinding after Begin must not change results inside the
// transaction; a new transaction observes the new binding.
func TestTransactionPinsResolution(t *testing.T) {
	store := NewStore()
	oldKey := store.PutKey("acct-a", "cn-north-1", "key-old", newPublicKeyDER(t))
	newKey := store.PutKey("acct-a", "cn-north-1", "key-new", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "release-signer"), keyTarget("key-old"))

	r := NewResolver(store, Policy{})
	tx := store.Begin()

	first, err := r.Resolve(tx, ref("acct-a", "cn-north-1", "release-signer"))
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Rebind the alias to a different key outside the transaction.
	store.BindAlias(ref("acct-a", "cn-north-1", "release-signer"), keyTarget("key-new"))

	second, err := r.Resolve(tx, ref("acct-a", "cn-north-1", "release-signer"))
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("resolutions inside one transaction must be pinned")
	}
	if second.Fingerprint != Fingerprint(oldKey.PublicKey) {
		t.Fatalf("transaction must still see old key, got %s", second.Fingerprint)
	}

	fresh, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "release-signer"))
	if err != nil {
		t.Fatalf("fresh resolve: %v", err)
	}
	if fresh.Fingerprint != Fingerprint(newKey.PublicKey) {
		t.Fatalf("new transaction must see new key, got %s", fresh.Fingerprint)
	}
	if fresh.AliasVersion != 2 {
		t.Fatalf("expected alias version 2 after rebind, got %d", fresh.AliasVersion)
	}
}

// 解析途中重绑定: a concurrent rebind in the middle of chain resolution
// must not affect the in-flight resolution or the signing request built
// from it.
func TestRebindDuringResolution(t *testing.T) {
	store := NewStore()
	goodKey := store.PutKey("acct-a", "cn-north-1", "key-good", newPublicKeyDER(t))
	evilKey := store.PutKey("acct-a", "cn-north-1", "key-evil", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "leaf"), keyTarget("key-good"))
	store.BindAlias(ref("acct-a", "cn-north-1", "root"), aliasTarget("acct-a", "cn-north-1", "leaf"))

	// After the first alias read (root), an attacker rebinds root to point
	// directly at key-evil, hoping the in-flight resolution picks it up.
	rebound := false
	store.OnAliasRead = func(r Ref) {
		if !rebound && r.Alias == "root" {
			rebound = true
			store.BindAlias(ref("acct-a", "cn-north-1", "root"), keyTarget("key-evil"))
		}
	}

	resolver := NewResolver(store, Policy{})
	tx := store.Begin()
	req, err := resolver.NewSigningRequest(tx, ref("acct-a", "cn-north-1", "root"), []byte("artifact-digest"))
	if err != nil {
		t.Fatalf("signing request: %v", err)
	}
	if !rebound {
		t.Fatal("test hook did not fire; rebind was not simulated")
	}
	if req.Fingerprint != Fingerprint(goodKey.PublicKey) {
		t.Fatalf("in-flight resolution was hijacked: got fingerprint %s", req.Fingerprint)
	}
	if req.Fingerprint == Fingerprint(evilKey.PublicKey) {
		t.Fatal("resolution must never observe the mid-resolution rebind")
	}
	if req.KeyID != "key-good" || req.Account != "acct-a" || req.Region != "cn-north-1" {
		t.Fatalf("unexpected signing request identity: %+v", req)
	}
	if req.AliasVersion != 1 {
		t.Fatalf("expected pinned alias version 1, got %d", req.AliasVersion)
	}
	wantHash := sha256.Sum256([]byte("artifact-digest"))
	if req.PayloadHash != wantHash {
		t.Fatal("payload hash mismatch")
	}
}

// 签名请求对象: the object carries the fully resolved identity.
func TestSigningRequestFields(t *testing.T) {
	store := NewStore()
	key := store.PutKey("acct-a", "cn-north-1", "key-1", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "step-2"), keyTarget("key-1"))
	store.BindAlias(ref("acct-a", "cn-north-1", "step-1"), aliasTarget("acct-a", "cn-north-1", "step-2"))

	resolver := NewResolver(store, Policy{})
	req, err := resolver.NewSigningRequest(store.Begin(),
		ref("acct-a", "cn-north-1", "step-1"), []byte("payload"))
	if err != nil {
		t.Fatalf("signing request: %v", err)
	}
	if req.Alias != "step-1" {
		t.Fatalf("expected original alias step-1, got %s", req.Alias)
	}
	if req.KeyID != key.KeyID || req.KeyVersion != key.Version {
		t.Fatalf("unexpected key identity: %s v%d", req.KeyID, req.KeyVersion)
	}
	if req.Fingerprint != Fingerprint(key.PublicKey) {
		t.Fatalf("unexpected fingerprint %s", req.Fingerprint)
	}
	if len(req.Chain) != 2 {
		t.Fatalf("expected 2-hop chain, got %d", len(req.Chain))
	}
}

func TestMaxHopsExceeded(t *testing.T) {
	store := NewStore()
	// Build a long chain a0 -> a1 -> ... -> a10 -> key.
	store.PutKey("acct-a", "cn-north-1", "key-1", newPublicKeyDER(t))
	store.BindAlias(ref("acct-a", "cn-north-1", "a10"), keyTarget("key-1"))
	for i := 9; i >= 0; i-- {
		next := ref("acct-a", "cn-north-1", fmt.Sprintf("a%d", i+1))
		store.BindAlias(ref("acct-a", "cn-north-1", fmt.Sprintf("a%d", i)),
			AliasTarget{Alias: &next})
	}
	r := NewResolver(store, Policy{MaxHops: 4})
	_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "a0"))
	if !errors.Is(err, ErrMaxHopsExceeded) {
		t.Fatalf("expected ErrMaxHopsExceeded, got %v", err)
	}
}

func TestAliasNotFound(t *testing.T) {
	store := NewStore()
	r := NewResolver(store, Policy{})
	_, err := r.Resolve(store.Begin(), ref("acct-a", "cn-north-1", "missing"))
	if !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("expected ErrAliasNotFound, got %v", err)
	}
}
