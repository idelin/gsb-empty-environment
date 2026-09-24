package canonkey

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
)

func req(tenant, cfg string, params any) Request {
	return Request{Tenant: tenant, ConfigVersion: cfg, Params: params}
}

func TestKeyIncludesTenantAndConfigVersion(t *testing.T) {
	k := NewKeyer(Limits{})
	params := map[string]any{"q": "x"}
	base := k.MustBuildKey(req("acme", "cfg-v1", params))
	otherTenant := k.MustBuildKey(req("globex", "cfg-v1", params))
	otherCfg := k.MustBuildKey(req("acme", "cfg-v2", params))
	if base.Digest == otherTenant.Digest {
		t.Fatal("tenant not reflected in key")
	}
	if base.Digest == otherCfg.Digest {
		t.Fatal("config version not reflected in key")
	}
	// Tenant isolation is fail-closed.
	if _, err := k.BuildKey(req("", "cfg-v1", params)); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("empty tenant: %v", err)
	}
	if _, err := k.BuildKey(req("acme", "", params)); !errors.Is(err, ErrMissingConfigVersion) {
		t.Fatalf("empty config version: %v", err)
	}
}

// TestConfigUpgradeIsolation covers the required sample: a key scheme
// that omits the config version produces a wrong hit after an upgrade,
// while versioned keys stay isolated.
func TestConfigUpgradeIsolation(t *testing.T) {
	params := map[string]any{"mode": "fast"}

	// Legacy hazard: versionless keys collide across config upgrades.
	legacyKey := func(r Request) Key {
		st := newCState(DefaultLimits())
		st.buf.WriteString("k0(")
		if err := st.encString(r.Tenant); err != nil {
			t.Fatal(err)
		}
		if err := st.encode(r.Params, 1); err != nil {
			t.Fatal(err)
		}
		st.buf.WriteByte(')')
		c := append([]byte(nil), st.buf.Bytes()...)
		return Key{Digest: sha256.Sum256(c), Canon: c}
	}
	legacy := NewStore()
	defer legacy.Close()
	oldReq := req("acme", "cfg-v1", params)
	newReq := req("acme", "cfg-v2", params)
	if err := legacy.Put(legacyKey(oldReq), "result-under-cfg-v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := legacy.Get(legacyKey(newReq)); !ok || v != "result-under-cfg-v1" {
		t.Fatal("expected the legacy scheme to wrongly hit the stale entry")
	}

	// Fixed scheme: old keys stay isolated after the upgrade.
	k := NewKeyer(Limits{})
	store := NewStore()
	defer store.Close()
	oldKey := k.MustBuildKey(oldReq)
	if err := store.Put(oldKey, "result-under-cfg-v1"); err != nil {
		t.Fatal(err)
	}
	newKey := k.MustBuildKey(newReq)
	if oldKey.Digest == newKey.Digest {
		t.Fatal("config upgrade must change the key")
	}
	if _, ok := store.Get(newKey); ok {
		t.Fatal("versioned key wrongly hit an entry from the old config")
	}
	if v, ok := store.Get(oldKey); !ok || v != "result-under-cfg-v1" {
		t.Fatal("old entry must remain intact and reachable by its own key")
	}
}

// TestForcedDigestCollisionReverified forces every digest to a constant
// and proves lookups re-verify the original canonical bytes.
func TestForcedDigestCollisionReverified(t *testing.T) {
	k := &Keyer{
		Hash:   func([]byte) [32]byte { return [32]byte{0xde, 0xad} },
		Limits: DefaultLimits(),
	}
	store := NewStore()
	defer store.Close()

	r1 := req("acme", "cfg-v1", map[string]any{"n": json.Number("1")})
	r2 := req("acme", "cfg-v1", map[string]any{"n": json.Number("2")})
	k1 := k.MustBuildKey(r1)
	k2 := k.MustBuildKey(r2)
	if k1.Digest != k2.Digest {
		t.Fatal("test requires forced equal digests")
	}
	if bytes.Equal(k1.Canon, k2.Canon) {
		t.Fatal("test requires different canonical bytes")
	}
	if err := store.Put(k1, "one"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(k2, "two"); err != nil {
		t.Fatal(err)
	}
	if v, ok := store.Get(k1); !ok || v != "one" {
		t.Fatalf("collision corrupted k1: %v %v", v, ok)
	}
	if v, ok := store.Get(k2); !ok || v != "two" {
		t.Fatalf("collision corrupted k2: %v %v", v, ok)
	}
	// A third, unseen request with the same forced digest must miss.
	k3 := k.MustBuildKey(req("acme", "cfg-v1", map[string]any{"n": json.Number("3")}))
	if _, ok := store.Get(k3); ok {
		t.Fatal("digest match without canonical-byte match must not hit")
	}
}

func TestResolverInjectables(t *testing.T) {
	var calls int
	var failures []error
	boom := errors.New("source exploded")
	res := &Resolver{
		Keyer: NewKeyer(Limits{}),
		Store: NewStore(),
		Source: func(ctx context.Context, r Request) (any, error) {
			calls++
			if r.Tenant == "bad" {
				return nil, boom
			}
			return "fresh", nil
		},
		OnError: func(err error) { failures = append(failures, err) },
	}
	defer res.Store.Close()
	ctx := context.Background()

	good := req("acme", "cfg-v1", map[string]any{"x": json.Number("1")})
	if v, err := res.Resolve(ctx, good); err != nil || v != "fresh" {
		t.Fatalf("first resolve: %v %v", v, err)
	}
	if v, err := res.Resolve(ctx, good); err != nil || v != "fresh" {
		t.Fatalf("second resolve: %v %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("source called %d times, want 1 (local cache)", calls)
	}
	if _, err := res.Resolve(ctx, req("bad", "cfg-v1", nil)); !errors.Is(err, boom) {
		t.Fatalf("source failure must propagate: %v", err)
	}
	if len(failures) != 1 || !errors.Is(failures[0], boom) {
		t.Fatalf("failure hook not invoked: %v", failures)
	}
	if res.Store.Len() != 1 {
		t.Fatal("failed resolutions must not be cached")
	}
}
