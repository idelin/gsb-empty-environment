package zoneauth_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"testing"

	"prescreen.local/ddd-10561-v4/testsupport"
	"prescreen.local/ddd-10561-v4/zoneauth"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..")
}

func loadGolden(t *testing.T, name string) *testsupport.Fixture {
	t.Helper()
	fixture, err := testsupport.ReadGolden(testdataDir(), name)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return fixture
}

func trustedKeys(f *testsupport.Fixture) map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{
		zoneauth.KeyIDFor(f.Keys.OldPub): f.Keys.OldPub,
	}
}

func TestGoldenFixturesVerify(t *testing.T) {
	for _, name := range []string{"handoff", "wildcard-only", "reexpose", "side-branch"} {
		fixture := loadGolden(t, name)
		t.Run(name, func(t *testing.T) {
			trusted := trustedKeys(fixture)

			if err := zoneauth.VerifyEnvelope(fixture.OldRoot, trusted); err != nil {
				t.Fatalf("old root signature: %v", err)
			}

			entries := make([]zoneauth.CachedQuery, 0, len(fixture.Cached))
			for _, entry := range fixture.Cached {
				entries = append(entries, zoneauth.CachedQuery{
					Query:  entry.Query,
					Answer: entry.Answer,
					Proof:  entry.Proof,
				})
			}
			verdicts, verified, err := zoneauth.Reevaluate(
				fixture.Patch, fixture.OldRoot, trusted, entries)
			if err != nil {
				t.Fatalf("reevaluate: %v", err)
			}
			if err := zoneauth.VerifyEnvelope(fixture.NewRoot, verified.TrustedKeys); err != nil {
				t.Fatalf("new root signature after trust extension: %v", err)
			}

			for _, entry := range fixture.Cached {
				if err := entry.Proof.VerifyStructural(fixture.OldRoot.RootHash); err != nil {
					t.Fatalf("cached proof structural: %v", err)
				}
				derived, err := entry.Proof.DeriveAnswer()
				if err != nil {
					t.Fatalf("derive cached answer: %v", err)
				}
				if derived.Kind != entry.Answer.Kind {
					t.Fatalf("cached answer kind mismatch: got %s want %s",
						derived.Kind, entry.Answer.Kind)
				}
			}

			if len(verdicts) != len(entries) {
				t.Fatalf("verdict count: got %d want %d", len(verdicts), len(entries))
			}
			for _, verdict := range verdicts {
				want := fixture.ExpectKind[testsupport.QueryKey(verdict.Query)]
				if verdict.Kind != want {
					t.Errorf("query %s: got verdict %s (%s), want %s",
						testsupport.QueryKey(verdict.Query), verdict.Kind, verdict.Reason, want)
				}
			}

			if verified.Envelope.Sequence != fixture.OldRoot.Sequence+1 {
				t.Fatalf("sequence did not advance by one")
			}
			if name == "handoff" {
				assertHandoff(t, fixture, verified)
			} else if verified.Envelope.KeyID != fixture.OldRoot.KeyID {
				t.Fatalf("non-handoff patch rotated the key id")
			}
		})
	}
}

func assertHandoff(t *testing.T, f *testsupport.Fixture, verified *zoneauth.VerifiedRoot) {
	t.Helper()
	patch := f.Patch
	if patch.Handoff == nil {
		t.Fatal("expected handoff envelope")
	}
	if err := zoneauth.VerifyHandoff(patch.Handoff, f.Keys.OldPub); err != nil {
		t.Fatalf("handoff signature: %v", err)
	}
	if patch.NewRoot.KeyID != zoneauth.KeyIDFor(f.Keys.NewPub) {
		t.Fatal("new root not signed by new key")
	}
	if patch.NewRoot.PreviousKeyID != zoneauth.KeyIDFor(f.Keys.OldPub) {
		t.Fatal("new root does not record the previous key id")
	}
	if _, ok := verified.TrustedKeys[zoneauth.KeyIDFor(f.Keys.OldPub)]; !ok {
		t.Fatal("old key dropped from trusted set")
	}
	if _, ok := verified.TrustedKeys[zoneauth.KeyIDFor(f.Keys.NewPub)]; !ok {
		t.Fatal("new key missing from trusted set")
	}
	if hex.EncodeToString(f.Keys.OldPub) == hex.EncodeToString(f.Keys.NewPub) {
		t.Fatal("handoff keys must differ")
	}
}

// TestPinnedSignatures locks real-signature determinism over the fixed keys
// and requires old/new root hashes to differ once a revision commits.
func TestPinnedSignatures(t *testing.T) {
	fixture := loadGolden(t, "handoff")
	if len(fixture.OldRoot.Signature) != ed25519.SignatureSize {
		t.Fatalf("old signature size %d", len(fixture.OldRoot.Signature))
	}
	if len(fixture.NewRoot.Signature) != ed25519.SignatureSize {
		t.Fatalf("new signature size %d", len(fixture.NewRoot.Signature))
	}
	if hex.EncodeToString(fixture.OldRoot.RootHash) ==
		hex.EncodeToString(fixture.NewRoot.RootHash) {
		t.Fatal("handoff root hashes unexpectedly equal")
	}

	resigned := *fixture.OldRoot
	zoneauth.SignRootEnvelope(&resigned, fixture.Keys.OldPriv)
	if hex.EncodeToString(resigned.Signature) != hex.EncodeToString(fixture.OldRoot.Signature) {
		t.Fatal("signature is not deterministic over the fixed key")
	}
}

// TestPinnedKeyOrdering pins the fixed-seed key identifiers and requires the
// handoff chain to present the old key before the new key. This is the
// "fixed old-key ordering" guarantee of the deterministic fixtures.
func TestPinnedKeyOrdering(t *testing.T) {
	keys := testsupport.LoadKeys()
	oldID := zoneauth.KeyIDFor(keys.OldPub)
	newID := zoneauth.KeyIDFor(keys.NewPub)

	wantOld := "56475aa75463474c0285df5dbf2bcab73da651358839e9b77481b2eab107708c"
	wantNew := "d1a16f2482990d2b130977740cf1b9a4f2a55e33cb54f84a5778337fb029dc07"
	if oldID != wantOld {
		t.Fatalf("old key id drifted:\n got %s\nwant %s", oldID, wantOld)
	}
	if newID != wantNew {
		t.Fatalf("new key id drifted:\n got %s\nwant %s", newID, wantNew)
	}

	fixture := loadGolden(t, "handoff")
	if fixture.Patch.Handoff.OldKeyID != oldID || fixture.Patch.Handoff.NewKeyID != newID {
		t.Fatal("handoff key ordering must be old-before-new")
	}
	if fixture.NewRoot.KeyID != newID || fixture.NewRoot.PreviousKeyID != oldID {
		t.Fatal("new root must chain previous key id back to the old key")
	}
}
