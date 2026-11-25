package zoneinterp_test

import (
	"crypto/ed25519"
	"testing"

	"prescreen.local/ddd-10561-v4/zoneauth"
	"prescreen.local/ddd-10561-v4/zoneinterp"
)

// buildEquivalentTrees applies the same logical mutations to both the full
// interpreter tree and the authenticated materialized tree.
func buildEquivalentTrees(t *testing.T, key ed25519.PrivateKey) (*zoneinterp.Tree, *zoneauth.Zone) {
	t.Helper()
	interp := zoneinterp.NewTree("example")
	auth := zoneauth.NewZone("example", key)

	put := func(name zoneinterp.Name, rr ...zoneinterp.Record) {
		t.Helper()
		if err := interp.PutRecords(name, rr...); err != nil {
			t.Fatal(err)
		}
		authRecs := make([]zoneauth.Record, len(rr))
		for i, r := range rr {
			authRecs[i] = zoneauth.Record{Type: r.Type, Value: r.Value}
		}
		if err := auth.PutRecords(zoneauth.Name(name), authRecs...); err != nil {
			t.Fatal(err)
		}
	}
	delegate := func(name zoneinterp.Name, target string) {
		t.Helper()
		ns := []zoneinterp.Record{{Type: "NS", Value: "ns1." + target}}
		if err := interp.AddDelegation(name, ns); err != nil {
			t.Fatal(err)
		}
		if err := auth.AddDelegation(zoneauth.Name(name),
			[]zoneauth.Record{{Type: "NS", Value: "ns1." + target}}); err != nil {
			t.Fatal(err)
		}
	}
	wildcard := func(parent zoneinterp.Name, value string) {
		t.Helper()
		rr := []zoneinterp.Record{{Type: "A", Value: value}}
		if err := interp.SetWildcard(parent, rr); err != nil {
			t.Fatal(err)
		}
		if err := auth.SetWildcard(zoneauth.Name(parent),
			[]zoneauth.Record{{Type: "A", Value: value}}); err != nil {
			t.Fatal(err)
		}
	}

	put(zoneinterp.Name{"org"}, zoneinterp.Record{Type: "A", Value: "203.0.113.7"})
	wildcard(zoneinterp.Name{"org"}, "203.0.113.9")
	delegate(zoneinterp.Name{"near"}, "near")
	put(zoneinterp.Name{"net", "edge"}, zoneinterp.Record{Type: "A", Value: "198.51.100.4"})
	put(zoneinterp.Name{"keep"}, zoneinterp.Record{Type: "TXT", Value: "side"})

	return interp, auth
}

func kindMap() map[zoneinterp.Kind]zoneauth.AnswerKind {
	return map[zoneinterp.Kind]zoneauth.AnswerKind{
		zoneinterp.Positive: zoneauth.AnswerPositive,
		zoneinterp.Referral: zoneauth.AnswerReferral,
		zoneinterp.NoData:   zoneauth.AnswerNoData,
		zoneinterp.NXDomain: zoneauth.AnswerNXDomain,
	}
}

// TestParityWithAuthenticatedTree requires the standalone interpreter and the
// authenticated partial-proof resolver to reach identical conclusions over a
// wide query matrix, including wildcard and delegation edges.
func TestParityWithAuthenticatedTree(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 7
	key := ed25519.NewKeyFromSeed(seed)

	interp, auth := buildEquivalentTrees(t, key)
	env := auth.Commit()

	queries := []zoneinterp.Name{
		{},
		{"org"},
		{"org", "alpha"},
		{"org", "alpha", "sub"},
		{"near"},
		{"near", "deep", "leaf"},
		{"net"},
		{"net", "edge"},
		{"net", "missing"},
		{"keep"},
		{"kee", "lookalike"},
		{"com", "x", "y"},
	}

	kinds := kindMap()
	for _, query := range queries {
		want := interp.Resolve(query)
		proof := auth.BuildProof(zoneauth.Name(query))
		if err := proof.VerifyStructural(env.RootHash); err != nil {
			t.Fatalf("query %v proof: %v", query, err)
		}
		got, err := proof.DeriveAnswer()
		if err != nil {
			t.Fatalf("query %v derive: %v", query, err)
		}
		if got.Kind != kinds[want.Kind] {
			t.Errorf("query %v: auth kind %s, interp kind %s", query, got.Kind, want.Kind)
		}
		if got.ViaWildcard != want.ViaWildcard {
			t.Errorf("query %v: wildcard flag %v, want %v", query, got.ViaWildcard, want.ViaWildcard)
		}
		if !sameName(got.Closest, want.Closest) {
			t.Errorf("query %v: closest %v, want %v", query, got.Closest, want.Closest)
		}
		if got.Kind == zoneauth.AnswerReferral && !sameName(got.Cut, want.Cut) {
			t.Errorf("query %v: cut %v, want %v", query, got.Cut, want.Cut)
		}
	}
}

// TestParityAfterDelegationRemoval repeats the comparison after a patch-like
// mutation, including far-ancestor wildcard re-exposure.
func TestParityAfterDelegationRemoval(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 9
	key := ed25519.NewKeyFromSeed(seed)

	interp, auth := buildEquivalentTrees(t, key)
	if err := interp.SetWildcard(zoneinterp.Name{},
		[]zoneinterp.Record{{Type: "TXT", Value: "apex-template"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.SetWildcard(zoneauth.Name{},
		[]zoneauth.Record{{Type: "TXT", Value: "apex-template"}}); err != nil {
		t.Fatal(err)
	}
	if err := interp.RemoveDelegation(zoneinterp.Name{"near"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.RemoveDelegation(zoneauth.Name{"near"}); err != nil {
		t.Fatal(err)
	}
	env := auth.Commit()

	for _, query := range []zoneinterp.Name{{"near"}, {"near", "deep"}, {"other"}} {
		want := interp.Resolve(query)
		proof := auth.BuildProof(zoneauth.Name(query))
		if err := proof.VerifyStructural(env.RootHash); err != nil {
			t.Fatal(err)
		}
		got, err := proof.DeriveAnswer()
		if err != nil {
			t.Fatal(err)
		}
		kinds := kindMap()
		if got.Kind != kinds[want.Kind] || got.ViaWildcard != want.ViaWildcard {
			t.Errorf("query %v: auth=%s/%v interp=%s/%v",
				query, got.Kind, got.ViaWildcard, want.Kind, want.ViaWildcard)
		}
	}
}

func sameName(a zoneauth.Name, b zoneinterp.Name) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
