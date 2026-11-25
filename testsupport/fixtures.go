// Package testsupport builds deterministic, real-signature fixtures for the
// authenticated patch component and its reference interpreter.
package testsupport

import (
	"crypto/ed25519"
	"encoding/hex"

	"prescreen.local/ddd-10561-v4/zoneauth"
)

// Fixed seeds: signatures are genuine Ed25519, yet byte-for-byte stable
// across runs, so the golden files pin the exact key ordering and hashes.
var seedSpecs = []string{
	"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
	"f0e1d2c3b4a5968778695a4b3c2d1e0f00112233445566778899aabbccddeeff",
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
}

// Keys bundles the fixed key material used by the fixtures.
type Keys struct {
	OldPriv ed25519.PrivateKey
	OldPub  ed25519.PublicKey
	NewPriv ed25519.PrivateKey
	NewPub  ed25519.PublicKey
}

// LoadKeys derives the fixed key pair set from constant seeds.
func LoadKeys() *Keys {
	oldSeed, _ := hex.DecodeString(seedSpecs[0])
	newSeed, _ := hex.DecodeString(seedSpecs[1])
	oldPriv := ed25519.NewKeyFromSeed(oldSeed)
	newPriv := ed25519.NewKeyFromSeed(newSeed)
	return &Keys{
		OldPriv: oldPriv,
		OldPub:  oldPriv.Public().(ed25519.PublicKey),
		NewPriv: newPriv,
		NewPub:  newPriv.Public().(ed25519.PublicKey),
	}
}

// QueryCase is one cached client query with its old-round answer and proof.
type QueryCase struct {
	Query  zoneauth.Name
	Answer *zoneauth.Answer
	Proof  *zoneauth.Proof
}

// Fixture is a complete, serializable scenario.
type Fixture struct {
	Name       string
	ZoneName   string
	Patch      *zoneauth.Patch
	OldRoot    *zoneauth.RootEnvelope
	NewRoot    *zoneauth.RootEnvelope
	Keys       *Keys
	Cached     []QueryCase
	ExpectKind map[string]zoneauth.VerdictKind
}

func ns(target string) []zoneauth.Record {
	return []zoneauth.Record{{Type: "NS", Value: "ns1." + target}, {Type: "NS", Value: "ns2." + target}}
}

func cacheQuery(z *zoneauth.Zone, query zoneauth.Name) QueryCase {
	proof := z.BuildProof(query)
	answer, err := proof.DeriveAnswer()
	if err != nil {
		panic(err)
	}
	return QueryCase{Query: query.Clone(), Answer: answer, Proof: proof}
}

// BuildHandoffFixture covers a cross-zone key handoff combined with an added
// delegation and wildcard work, all under a real new root signature.
func BuildHandoffFixture() *Fixture {
	keys := LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	must(z.PutRecords(zoneauth.Name{"org"}, zoneauth.Record{Type: "A", Value: "203.0.113.7"}))
	must(z.SetWildcard(zoneauth.Name{"org"}, []zoneauth.Record{{Type: "A", Value: "203.0.113.9"}}))
	must(z.PutRecords(zoneauth.Name{"net", "edge"}, zoneauth.Record{Type: "A", Value: "198.51.100.4"}))

	cached := []QueryCase{
		cacheQuery(z, zoneauth.Name{"org", "new"}),        // wildcard positive
		cacheQuery(z, zoneauth.Name{"org", "gone"}),       // wildcard positive pre-patch
		cacheQuery(z, zoneauth.Name{"org", "new", "sub"}), // nxdomain (depth)
		cacheQuery(z, zoneauth.Name{"net", "edge"}),       // positive side branch
		cacheQuery(z, zoneauth.Name{"com", "alpha"}),      // nxdomain at apex
	}

	b := zoneauth.NewPatchBuilder(z)
	must(b.AddDelegation(zoneauth.Name{"org", "new"}, ns("new.org")))
	b.RotateKey(keys.NewPriv)
	patch := b.Sign()
	b.AttachClientProofs(patch,
		zoneauth.Name{"org", "new"},
		zoneauth.Name{"org", "gone"},
		zoneauth.Name{"org", "new", "sub"},
		zoneauth.Name{"net", "edge"},
		zoneauth.Name{"com", "alpha"})

	return &Fixture{
		Name:     "handoff",
		ZoneName: "example",
		Patch:    patch,
		OldRoot:  patch.OldRoot,
		NewRoot:  patch.NewRoot,
		Keys:     keys,
		Cached:   cached,
		ExpectKind: map[string]zoneauth.VerdictKind{
			"org/new":     zoneauth.VerdictChanged,
			"org/gone":    zoneauth.VerdictReusable,
			"org/new/sub": zoneauth.VerdictChanged,
			"net/edge":    zoneauth.VerdictReusable,
			"com/alpha":   zoneauth.VerdictReusable,
		},
	}
}

// BuildWildcardOnlyFixture changes only a wildcard template; the root hash
// moves, but unrelated positive and negative answers remain reusable.
func BuildWildcardOnlyFixture() *Fixture {
	keys := LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	must(z.PutRecords(zoneauth.Name{"svc"}, zoneauth.Record{Type: "A", Value: "192.0.2.10"}))
	must(z.SetWildcard(zoneauth.Name{"svc"}, []zoneauth.Record{{Type: "A", Value: "192.0.2.99"}}))

	cached := []QueryCase{
		cacheQuery(z, zoneauth.Name{"svc", "a"}), // wildcard positive old
		cacheQuery(z, zoneauth.Name{"svc", "b"}), // wildcard positive old
		cacheQuery(z, zoneauth.Name{"svc"}),      // nodata anchor
	}

	b := zoneauth.NewPatchBuilder(z)
	must(b.SetWildcard(zoneauth.Name{"svc"}, []zoneauth.Record{{Type: "A", Value: "192.0.2.100"}}))
	patch := b.Sign()
	b.AttachClientProofs(patch,
		zoneauth.Name{"svc", "a"},
		zoneauth.Name{"svc", "b"},
		zoneauth.Name{"svc"})

	return &Fixture{
		Name:     "wildcard-only",
		ZoneName: "example",
		Patch:    patch,
		OldRoot:  patch.OldRoot,
		NewRoot:  patch.NewRoot,
		Keys:     keys,
		Cached:   cached,
		ExpectKind: map[string]zoneauth.VerdictKind{
			"svc/a": zoneauth.VerdictChanged,
			"svc/b": zoneauth.VerdictChanged,
			"svc":   zoneauth.VerdictReusable,
		},
	}
}

// BuildReexposeFixture deletes a near delegation so that a far-ancestor
// wildcard re-emerges for the subtree beneath it.
func BuildReexposeFixture() *Fixture {
	keys := LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	must(z.SetWildcard(zoneauth.Name{}, []zoneauth.Record{{Type: "TXT", Value: "apex-template"}}))
	must(z.AddDelegation(zoneauth.Name{"near"}, ns("near")))

	cached := []QueryCase{
		cacheQuery(z, zoneauth.Name{"near", "deep", "leaf"}), // referral at near
		cacheQuery(z, zoneauth.Name{"near"}),                 // referral exact
		cacheQuery(z, zoneauth.Name{"fresh"}),                // apex wildcard positive
		cacheQuery(z, zoneauth.Name{"other", "thing"}),       // apex wildcard positive
	}

	b := zoneauth.NewPatchBuilder(z)
	must(b.RemoveDelegation(zoneauth.Name{"near"}))
	patch := b.Sign()
	b.AttachClientProofs(patch,
		zoneauth.Name{"near", "deep", "leaf"},
		zoneauth.Name{"near"},
		zoneauth.Name{"other", "thing"},
		zoneauth.Name{"fresh"})

	return &Fixture{
		Name:     "reexpose",
		ZoneName: "example",
		Patch:    patch,
		OldRoot:  patch.OldRoot,
		NewRoot:  patch.NewRoot,
		Keys:     keys,
		Cached:   cached,
		ExpectKind: map[string]zoneauth.VerdictKind{
			"near/deep/leaf": zoneauth.VerdictChanged,
			"near":           zoneauth.VerdictChanged,
			"fresh":          zoneauth.VerdictReusable,
			"other/thing":    zoneauth.VerdictReusable,
		},
	}
}

// BuildSideBranchFixture changes the root via an unrelated subtree edit;
// queries in untouched side branches keep identical answers and stay usable.
func BuildSideBranchFixture() *Fixture {
	keys := LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	must(z.PutRecords(zoneauth.Name{"keep"}, zoneauth.Record{Type: "A", Value: "10.0.0.1"}))
	must(z.PutRecords(zoneauth.Name{"edit"}, zoneauth.Record{Type: "A", Value: "10.0.0.2"}))

	cached := []QueryCase{
		cacheQuery(z, zoneauth.Name{"keep"}),             // positive untouched
		cacheQuery(z, zoneauth.Name{"keep", "child"}),    // nxdomain untouched
		cacheQuery(z, zoneauth.Name{"edit"}),             // positive edited branch
		cacheQuery(z, zoneauth.Name{"kee", "lookalike"}), // string-prefix trap
	}

	b := zoneauth.NewPatchBuilder(z)
	must(b.AddDelegation(zoneauth.Name{"edit", "cut"}, ns("cut.edit")))
	patch := b.Sign()
	b.AttachClientProofs(patch,
		zoneauth.Name{"keep"},
		zoneauth.Name{"keep", "child"},
		zoneauth.Name{"edit"},
		zoneauth.Name{"kee", "lookalike"})

	return &Fixture{
		Name:     "side-branch",
		ZoneName: "example",
		Patch:    patch,
		OldRoot:  patch.OldRoot,
		NewRoot:  patch.NewRoot,
		Keys:     keys,
		Cached:   cached,
		ExpectKind: map[string]zoneauth.VerdictKind{
			"keep":          zoneauth.VerdictReusable,
			"keep/child":    zoneauth.VerdictReusable,
			"edit":          zoneauth.VerdictReusable,
			"kee/lookalike": zoneauth.VerdictReusable,
		},
	}
}

// AllFixtures returns every fixed scenario in stable order.
func AllFixtures() []*Fixture {
	return []*Fixture{
		BuildHandoffFixture(),
		BuildWildcardOnlyFixture(),
		BuildReexposeFixture(),
		BuildSideBranchFixture(),
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// QueryKey renders a name as the stable key used by expectation maps.
func QueryKey(n zoneauth.Name) string {
	key := ""
	for i, label := range n {
		if i > 0 {
			key += "/"
		}
		key += label
	}
	return key
}
