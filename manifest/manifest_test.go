package manifest_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"prescreen.local/ps-d4797ec147/manifest"
)

const testModel = "dev-model-X"

type keyPair struct {
	priv ed25519.PrivateKey
	pub  []byte
}

func genKey(t *testing.T) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return keyPair{priv: priv, pub: []byte(pub)}
}

type testEnv struct {
	t        *testing.T
	host     *manifest.Host
	store    *manifest.FileStore
	now      time.Time
	rootPriv map[string]ed25519.PrivateKey
	roots    map[string]manifest.Root
	fw       map[string]keyPair
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	rootPriv := make(map[string]ed25519.PrivateKey, 3)
	roots := make(map[string]manifest.Root, 3)
	for _, id := range []string{"root-a", "root-b", "root-c"} {
		kp := genKey(t)
		rootPriv[id] = kp.priv
		roots[id] = manifest.Root{ID: id, PubKey: kp.pub}
	}

	fw := map[string]keyPair{
		"fw-1": genKey(t),
		"fw-2": genKey(t),
		"fw-3": genKey(t),
		"fw-4": genKey(t),
	}
	// fw-2-alias is a distinct keyID backed by exactly fw-2's key material.
	fw["fw-2-alias"] = fw["fw-2"]

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	store, err := manifest.NewFileStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	host, err := manifest.NewHost(manifest.Config{
		DeviceModel:          testModel,
		Roots:                roots,
		RootThreshold:        2,
		MinFirmwareThreshold: 2,
		Now:                  func() time.Time { return now },
	}, store)
	if err != nil {
		t.Fatalf("new host: %v", err)
	}

	return &testEnv{
		t:        t,
		host:     host,
		store:    store,
		now:      now,
		rootPriv: rootPriv,
		roots:    roots,
		fw:       fw,
	}
}

// entry builds a firmware-purposed key entry active for generations [start, stop).
func (e *testEnv) entry(id string, start, stop uint64, purposes ...string) manifest.KeyEntry {
	if len(purposes) == 0 {
		purposes = []string{manifest.PurposeFirmware}
	}
	return manifest.KeyEntry{
		KeyID:    id,
		PubKey:   e.fw[id].pub,
		Purposes: purposes,
		GenStart: start,
		GenStop:  stop,
	}
}

// baseManifest builds a well-formed manifest at gen with fw-1..fw-3 active.
func (e *testEnv) baseManifest(gen uint64) *manifest.Manifest {
	return &manifest.Manifest{
		DeviceModel: testModel,
		Generation:  gen,
		NotBefore:   e.now.Add(-time.Hour),
		NotAfter:    e.now.Add(time.Hour),
		Threshold:   2,
		Entries: []manifest.KeyEntry{
			e.entry("fw-1", 1, 100),
			e.entry("fw-2", 1, 100),
			e.entry("fw-3", 1, 100),
		},
	}
}

func (e *testEnv) sign(m *manifest.Manifest, rootIDs ...string) *manifest.SignedManifest {
	sigs := make([]manifest.RootSignature, 0, len(rootIDs))
	for _, id := range rootIDs {
		sigs = append(sigs, manifest.SignManifest(m, id, e.rootPriv[id]))
	}
	return &manifest.SignedManifest{Manifest: m, Signatures: sigs}
}

func (e *testEnv) acceptBase(gen uint64, rootIDs ...string) *manifest.SignedManifest {
	e.t.Helper()
	sm := e.sign(e.baseManifest(gen), rootIDs...)
	if err := e.host.Accept(sm); err != nil {
		e.t.Fatalf("accept gen %d: %v", gen, err)
	}
	return sm
}

func (e *testEnv) image(gen uint64, keyIDs ...string) *manifest.FirmwareImage {
	img := &manifest.FirmwareImage{
		DeviceModel: testModel,
		ImageDigest: []byte("sha256-image-digest"),
		Version:     "1.2.3",
		Generation:  gen,
	}
	for _, id := range keyIDs {
		img.Signatures = append(img.Signatures, manifest.SignFirmware(img, id, e.fw[id].priv))
	}
	return img
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

// --- Canonical encoding -----------------------------------------------------

func TestCanonicalEncodingDeterministic(t *testing.T) {
	env := newTestEnv(t)
	m := env.baseManifest(1)

	first := manifest.CanonicalEncoding(m)
	if !bytes.Equal(first, manifest.CanonicalEncoding(m)) {
		t.Fatal("canonical encoding not stable across calls")
	}

	// Reordering entries and the revocation set must not change the digest.
	reordered := env.baseManifest(1)
	reordered.Entries[0], reordered.Entries[2] = reordered.Entries[2], reordered.Entries[0]
	reordered.RevokedKeyIDs = []string{"z", "a", "m"}
	m.RevokedKeyIDs = []string{"m", "a", "z"}
	if !bytes.Equal(manifest.ManifestDigest(m), manifest.ManifestDigest(reordered)) {
		t.Fatal("collection ordering changed canonical digest")
	}

	// Any semantic change must change the digest.
	tampered := env.baseManifest(1)
	tampered.Generation = 2
	if bytes.Equal(manifest.ManifestDigest(m), manifest.ManifestDigest(tampered)) {
		t.Fatal("different manifests produced the same digest")
	}
}

// --- Happy path -------------------------------------------------------------

func TestAcceptAndBoot(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	state, err := env.host.CurrentState()
	if err != nil {
		t.Fatalf("current state: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("stored generation = %d, want 1", state.Generation)
	}

	if err := env.host.BootCheck(env.image(1, "fw-1", "fw-2")); err != nil {
		t.Fatalf("boot with two keys: %v", err)
	}

	// One signature is below threshold t=2.
	assertErrIs(t, env.host.BootCheck(env.image(1, "fw-1")), manifest.ErrFirmwareQuorum)
}

// --- Root quorum semantics --------------------------------------------------

func TestRootQuorum(t *testing.T) {
	t.Run("single known root below threshold", func(t *testing.T) {
		env := newTestEnv(t)
		assertErrIs(t, env.host.Accept(env.sign(env.baseManifest(1), "root-a")), manifest.ErrRootQuorum)
	})

	t.Run("unknown root never counts", func(t *testing.T) {
		env := newTestEnv(t)
		unknown := genKey(t)
		sm := env.sign(env.baseManifest(1), "root-a")
		sm.Signatures = append(sm.Signatures, manifest.SignManifest(sm.Manifest, "root-unknown", unknown.priv))
		assertErrIs(t, env.host.Accept(sm), manifest.ErrRootQuorum)
	})

	t.Run("only unknown roots", func(t *testing.T) {
		env := newTestEnv(t)
		unknown := genKey(t)
		m := env.baseManifest(1)
		sm := &manifest.SignedManifest{
			Manifest:   m,
			Signatures: []manifest.RootSignature{manifest.SignManifest(m, "root-unknown", unknown.priv)},
		}
		assertErrIs(t, env.host.Accept(sm), manifest.ErrUnknownRoot)
	})

	t.Run("forged signature from known root", func(t *testing.T) {
		env := newTestEnv(t)
		attacker := genKey(t)
		m := env.baseManifest(1)
		sm := &manifest.SignedManifest{
			Manifest: m,
			Signatures: []manifest.RootSignature{
				manifest.SignManifest(m, "root-a", env.rootPriv["root-a"]),
				{RootID: "root-b", Signature: ed25519.Sign(attacker.priv, manifest.CanonicalEncoding(m))},
			},
		}
		assertErrIs(t, env.host.Accept(sm), manifest.ErrRootSignature)
	})

	t.Run("identical duplicate attestation", func(t *testing.T) {
		env := newTestEnv(t)
		m := env.baseManifest(1)
		sig := manifest.SignManifest(m, "root-a", env.rootPriv["root-a"])
		sm := &manifest.SignedManifest{Manifest: m, Signatures: []manifest.RootSignature{sig, sig}}
		assertErrIs(t, env.host.Accept(sm), manifest.ErrDuplicateRoot)
	})

	t.Run("conflicting attestations from same root", func(t *testing.T) {
		env := newTestEnv(t)
		m := env.baseManifest(1)
		good := manifest.SignManifest(m, "root-a", env.rootPriv["root-a"])
		// A second, genuinely valid Ed25519 signature from the same root but
		// over different content: both verify under root-a, yet disagree.
		other := env.baseManifest(1)
		other.NotAfter = env.now.Add(2 * time.Hour)
		bad := manifest.RootSignature{
			RootID:    "root-a",
			Signature: ed25519.Sign(env.rootPriv["root-a"], manifest.CanonicalEncoding(other)),
		}
		sm := &manifest.SignedManifest{Manifest: m, Signatures: []manifest.RootSignature{good, bad}}
		assertErrIs(t, env.host.Accept(sm), manifest.ErrRootConflict)
	})

	t.Run("two distinct known roots accepted", func(t *testing.T) {
		env := newTestEnv(t)
		env.acceptBase(1, "root-b", "root-c")
	})
}

// --- Replay and forks -------------------------------------------------------

func TestOldManifestReplay(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")
	env.acceptBase(2, "root-a", "root-b")

	// Replaying the old generation-1 manifest must not roll state back.
	old := env.sign(env.baseManifest(1), "root-a", "root-b")
	assertErrIs(t, env.host.Accept(old), manifest.ErrReplayedManifest)

	state, err := env.host.CurrentState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Generation != 2 {
		t.Fatalf("generation = %d after replay, want 2", state.Generation)
	}

	// A firmware image at the stale generation is rejected too.
	assertErrIs(t, env.host.BootCheck(env.image(1, "fw-1", "fw-2")), manifest.ErrReplayedManifest)
}

func TestSameGenerationFork(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	fork := env.baseManifest(1)
	fork.NotAfter = env.now.Add(2 * time.Hour) // different canonical content, same gen
	assertErrIs(t, env.host.Accept(env.sign(fork, "root-a", "root-b")), manifest.ErrForkedManifest)

	// Identical re-submission is idempotent and does not touch storage.
	same := env.sign(env.baseManifest(1), "root-a", "root-b")
	if err := env.host.Accept(same); err != nil {
		t.Fatalf("identical resubmission: %v", err)
	}
}

// --- Revocation and key continuity ------------------------------------------

func TestRevocationRollback(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	gen2 := env.baseManifest(2)
	gen2.RevokedKeyIDs = []string{"fw-3"}
	if err := env.host.Accept(env.sign(gen2, "root-a", "root-b")); err != nil {
		t.Fatalf("accept revoking gen2: %v", err)
	}

	// gen3 silently un-revokes fw-3.
	gen3 := env.baseManifest(3)
	assertErrIs(t, env.host.Accept(env.sign(gen3, "root-a", "root-b")), manifest.ErrRevocationRollback)

	// gen3 re-adds the revoked key ID as an entry: also a rollback.
	gen3b := env.baseManifest(3)
	gen3b.RevokedKeyIDs = []string{"fw-3"}
	assertErrIs(t, env.host.Accept(env.sign(gen3b, "root-a", "root-b")), manifest.ErrRevocationRollback)

	// Keeping the revocation set monotonic succeeds.
	gen3c := env.baseManifest(3)
	gen3c.RevokedKeyIDs = []string{"fw-3"}
	gen3c.Entries = []manifest.KeyEntry{env.entry("fw-1", 1, 100), env.entry("fw-2", 1, 100)}
	if err := env.host.Accept(env.sign(gen3c, "root-a", "root-b")); err != nil {
		t.Fatalf("accept monotonic gen3: %v", err)
	}

	// The revoked key can no longer vote; the surviving two keys still can.
	assertErrIs(t, env.host.BootCheck(env.image(3, "fw-1", "fw-3")), manifest.ErrFirmwareSignature)
	if err := env.host.BootCheck(env.image(3, "fw-1", "fw-2")); err != nil {
		t.Fatalf("boot with surviving keys: %v", err)
	}
}

func TestKeyMaterialChange(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	replacement := genKey(t)
	gen2 := env.baseManifest(2)
	for i := range gen2.Entries {
		if gen2.Entries[i].KeyID == "fw-1" {
			gen2.Entries[i].PubKey = replacement.pub
		}
	}
	assertErrIs(t, env.host.Accept(env.sign(gen2, "root-a", "root-b")), manifest.ErrKeyMaterialChanged)

	// A brand-new key ID with fresh material is fine; the retained fw-1/fw-2
	// entries keep their original material.
	gen2ok := env.baseManifest(2)
	gen2ok.Entries = append(gen2ok.Entries, manifest.KeyEntry{
		KeyID:    "fw-new",
		PubKey:   replacement.pub,
		Purposes: []string{manifest.PurposeFirmware},
		GenStart: 2,
		GenStop:  100,
	})
	if err := env.host.Accept(env.sign(gen2ok, "root-a", "root-b")); err != nil {
		t.Fatalf("accept new key ID: %v", err)
	}
}

func TestThresholdLowering(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	gen2 := env.baseManifest(2)
	gen2.Threshold = 1 // below host minimum of 2
	assertErrIs(t, env.host.Accept(env.sign(gen2, "root-a", "root-b")), manifest.ErrThresholdBelowMin)

	// State must remain at generation 1 with threshold 2.
	state, err := env.host.CurrentState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Generation != 1 || state.SignedManifest.Manifest.Threshold != 2 {
		t.Fatalf("state changed after rejected threshold lowering: gen=%d", state.Generation)
	}
}

// --- Alias keys and firmware admission --------------------------------------

func TestAliasKeysCountOnce(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	// Add a second keyID whose public key material is identical to fw-2's.
	gen2 := env.baseManifest(2)
	gen2.Entries = append(gen2.Entries, env.entry("fw-2-alias", 1, 100))
	if err := env.host.Accept(env.sign(gen2, "root-a", "root-b")); err != nil {
		t.Fatalf("accept alias manifest: %v", err)
	}

	// fw-2 and fw-2-alias share one public key: two signatures, one voter,
	// so threshold t=2 is not reached.
	assertErrIs(t, env.host.BootCheck(env.image(2, "fw-2", "fw-2-alias")), manifest.ErrFirmwareQuorum)

	// One genuinely distinct second key reaches the threshold.
	if err := env.host.BootCheck(env.image(2, "fw-1", "fw-2-alias")); err != nil {
		t.Fatalf("boot with distinct material: %v", err)
	}
}

func TestFirmwareEligibilityFilters(t *testing.T) {
	env := newTestEnv(t)
	m := env.baseManifest(1)

	// Entry whose window closes before generation 2; eligible now, expired later.
	m.Entries = append(m.Entries, env.entry("fw-4", 1, 2))
	sm := env.sign(m, "root-a", "root-b")
	if err := env.host.Accept(sm); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := env.host.BootCheck(env.image(1, "fw-1", "fw-4")); err != nil {
		t.Fatalf("in-window fw-4 should vote: %v", err)
	}

	// At generation 2 the [1,2) window has closed: fw-4 drops out of eligibility.
	gen2 := env.baseManifest(2)
	gen2.Entries = append(gen2.Entries, env.entry("fw-4", 1, 2))
	if err := env.host.Accept(env.sign(gen2, "root-a", "root-b")); err != nil {
		t.Fatalf("accept gen2: %v", err)
	}
	assertErrIs(t, env.host.BootCheck(env.image(2, "fw-1", "fw-4")), manifest.ErrFirmwareSignature)
	if got := manifest.EligibleFirmwareKeys(gen2); len(got) != 3 {
		t.Fatalf("eligible keys = %v, want the three in-window entries", got)
	}

	// Non-firmware purpose cannot vote.
	gen3 := env.baseManifest(3)
	gen3.Entries = []manifest.KeyEntry{
		env.entry("fw-1", 1, 100),
		env.entry("fw-2", 1, 100, "debug"),
		env.entry("fw-3", 1, 100),
	}
	if err := env.host.Accept(env.sign(gen3, "root-a", "root-b")); err != nil {
		t.Fatalf("accept gen3: %v", err)
	}
	assertErrIs(t, env.host.BootCheck(env.image(3, "fw-1", "fw-2")), manifest.ErrFirmwareSignature)

	// A signature over altered firmware bytes fails verification.
	img := env.image(3, "fw-1", "fw-3")
	img.Version = "9.9.9-attacker"
	assertErrIs(t, env.host.BootCheck(img), manifest.ErrFirmwareSignature)

	// Wrong model on the image.
	badModel := env.image(3, "fw-1", "fw-3")
	badModel.DeviceModel = "other-device"
	assertErrIs(t, env.host.BootCheck(badModel), manifest.ErrModelMismatch)
}

// --- Validity window (closed start, open end) -------------------------------

func TestValidityWindowHalfOpen(t *testing.T) {
	env := newTestEnv(t)
	m := env.baseManifest(1)
	m.NotBefore = env.now
	m.NotAfter = env.now.Add(time.Minute)

	// At NotBefore exactly: inside the window.
	inside := env.sign(m, "root-a", "root-b")
	if err := env.host.Accept(inside); err != nil {
		t.Fatalf("accept at NotBefore: %v", err)
	}

	// A fresh store with a clock at NotAfter exactly: the right end is open.
	env2 := newTestEnv(t)
	m2 := env2.baseManifest(1)
	m2.NotBefore = env2.now.Add(-time.Minute)
	m2.NotAfter = env2.now
	assertErrIs(t, env2.host.Accept(env2.sign(m2, "root-a", "root-b")), manifest.ErrOutsideValidity)
}

// --- Persistence: atomicity and failure injection ---------------------------

// failStore wraps a real Store and fails the next Commit, delegating Load to
// the underlying file store. It models "the last persistence write fails".
type failStore struct {
	manifest.Store
	failNext bool
}

func (f *failStore) Commit(state *manifest.State) error {
	if f.failNext {
		f.failNext = false
		return errors.New("injected disk failure")
	}
	return f.Store.Commit(state)
}

func TestCommitFailureKeepsOldState(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	failing := &failStore{Store: env.store, failNext: true}
	host, err := manifest.NewHost(manifest.Config{
		DeviceModel:          testModel,
		Roots:                env.roots,
		RootThreshold:        2,
		MinFirmwareThreshold: 2,
		Now:                  func() time.Time { return env.now },
	}, failing)
	if err != nil {
		t.Fatalf("new host: %v", err)
	}

	gen2 := env.baseManifest(2)
	if err := host.Accept(env.sign(gen2, "root-a", "root-b")); err == nil {
		t.Fatal("expected commit failure, got nil")
	}

	// The old generation-1 state must remain fully observable and bootable:
	// no new generation paired with old (or partial) content.
	state, err := env.host.CurrentState()
	if err != nil {
		t.Fatalf("load after failed commit: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("generation = %d after failed commit, want 1", state.Generation)
	}
	if err := env.host.BootCheck(env.image(1, "fw-1", "fw-2")); err != nil {
		t.Fatalf("old manifest no longer boots: %v", err)
	}

	// Retrying after the fault clears succeeds and advances to generation 2.
	if err := host.Accept(env.sign(gen2, "root-a", "root-b")); err != nil {
		t.Fatalf("retry accept: %v", err)
	}
	state, _ = host.CurrentState()
	if state.Generation != 2 {
		t.Fatalf("generation = %d after retry, want 2", state.Generation)
	}
	if err := host.BootCheck(env.image(2, "fw-1", "fw-2")); err != nil {
		t.Fatalf("boot gen2 after retry: %v", err)
	}
}

func TestFileStoreRoundTripAndCrashArtifacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store, err := manifest.NewFileStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if _, err := store.Load(); !errors.Is(err, manifest.ErrNoState) {
		t.Fatalf("empty store Load: want ErrNoState, got %v", err)
	}

	env := newTestEnv(t)
	sm := env.sign(env.baseManifest(7), "root-a", "root-c")
	want := &manifest.State{
		Generation:     7,
		Digest:         manifest.ManifestDigest(sm.Manifest),
		SignedManifest: sm,
	}
	if err := store.Commit(want); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Generation != want.Generation || !bytes.Equal(got.Digest, want.Digest) {
		t.Fatal("round-tripped state mismatch")
	}

	// Simulate a crash between temp-file write and rename: a stale temp file
	// must be swept and never shadow the committed state.
	if err := writeJunk(filepath.Join(dir, "manifest-state.gob.tmp")); err != nil {
		t.Fatalf("seed stale temp: %v", err)
	}
	reopened, err := manifest.NewFileStore(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	again, err := reopened.Load()
	if err != nil {
		t.Fatalf("load with stale temp: %v", err)
	}
	if again.Generation != 7 {
		t.Fatalf("generation = %d, want 7", again.Generation)
	}
}

func TestTamperedStateDigestDetected(t *testing.T) {
	env := newTestEnv(t)
	env.acceptBase(1, "root-a", "root-b")

	state, err := env.store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Corrupt the in-storage manifest without updating the persisted digest.
	state.SignedManifest.Manifest.Threshold = 1
	if err := env.store.Commit(state); err != nil {
		t.Fatalf("commit tampered state: %v", err)
	}
	_, err = env.host.CurrentState()
	assertErrIs(t, err, manifest.ErrDigestMismatch)
	assertErrIs(t, env.host.BootCheck(env.image(1, "fw-1", "fw-2")), manifest.ErrDigestMismatch)
}

func writeJunk(path string) error {
	return os.WriteFile(path, []byte("not-a-gob"), 0o600)
}

// --- Stateless verification and configuration checks ------------------------

func TestVerifyFirmwareStateless(t *testing.T) {
	env := newTestEnv(t)
	sm := env.sign(env.baseManifest(1), "root-a", "root-b")

	// No state in storage, yet VerifyFirmware re-checks the root quorum and
	// admits the image under the supplied manifest.
	if _, err := env.store.Load(); !errors.Is(err, manifest.ErrNoState) {
		t.Fatalf("store should be empty, got %v", err)
	}
	if err := env.host.VerifyFirmware(sm, env.image(1, "fw-1", "fw-2")); err != nil {
		t.Fatalf("stateless verify: %v", err)
	}
	assertErrIs(t, env.host.VerifyFirmware(env.sign(env.baseManifest(1), "root-a"), env.image(1, "fw-1")),
		manifest.ErrRootQuorum)
	assertErrIs(t, env.host.VerifyFirmware(nil, env.image(1, "fw-1")), manifest.ErrInvalidManifest)
	assertErrIs(t, env.host.VerifyFirmware(sm, nil), manifest.ErrInvalidManifest)
}

func TestNewHostConfigValidation(t *testing.T) {
	env := newTestEnv(t)
	good := manifest.Config{
		DeviceModel:          testModel,
		Roots:                env.roots,
		RootThreshold:        2,
		MinFirmwareThreshold: 2,
	}

	if _, err := manifest.NewHost(good, nil); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("nil store: %v", err)
	}
	store, _ := manifest.NewFileStore(filepath.Join(t.TempDir(), "s"))

	bad := good
	bad.DeviceModel = ""
	if _, err := manifest.NewHost(bad, store); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("empty model: %v", err)
	}

	bad = good
	bad.RootThreshold = 0
	if _, err := manifest.NewHost(bad, store); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("zero threshold: %v", err)
	}

	bad = good
	bad.RootThreshold = 99
	if _, err := manifest.NewHost(bad, store); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("threshold above root count: %v", err)
	}

	bad = good
	bad.MinFirmwareThreshold = 0
	if _, err := manifest.NewHost(bad, store); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("zero min firmware threshold: %v", err)
	}

	bad = good
	bad.Roots = map[string]manifest.Root{"mismatch": {ID: "other", PubKey: env.roots["root-a"].PubKey}}
	if _, err := manifest.NewHost(bad, store); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("mismatched root ID: %v", err)
	}

	if _, err := manifest.NewFileStore(""); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("empty store dir: %v", err)
	}
}

func TestMalformedManifests(t *testing.T) {
	env := newTestEnv(t)

	cases := map[string]func(*manifest.Manifest){
		"nil body":         func(m *manifest.Manifest) { *m = manifest.Manifest{} },
		"zero generation":  func(m *manifest.Manifest) { m.Generation = 0 },
		"inverted window":  func(m *manifest.Manifest) { m.NotAfter = m.NotBefore },
		"zero threshold":   func(m *manifest.Manifest) { m.Threshold = 0 },
		"empty key ID":     func(m *manifest.Manifest) { m.Entries[0].KeyID = "" },
		"duplicate entry":  func(m *manifest.Manifest) { m.Entries = append(m.Entries, m.Entries[0]) },
		"empty window":     func(m *manifest.Manifest) { m.Entries[0].GenStop = m.Entries[0].GenStart },
		"no purposes":      func(m *manifest.Manifest) { m.Entries[0].Purposes = nil },
		"duplicate revoke": func(m *manifest.Manifest) { m.RevokedKeyIDs = []string{"x", "x"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var m *manifest.Manifest
			if name == "nil body" {
				empty := manifest.Manifest{}
				sm := &manifest.SignedManifest{Manifest: &empty}
				assertErrIs(t, env.host.Accept(sm), manifest.ErrInvalidManifest)
				return
			}
			m = env.baseManifest(1)
			mutate(m)
			assertErrIs(t, env.host.Accept(env.sign(m, "root-a", "root-b")), manifest.ErrInvalidManifest)
		})
	}

	assertErrIs(t, env.host.Accept(nil), manifest.ErrInvalidManifest)
	assertErrIs(t, env.host.BootCheck(nil), manifest.ErrInvalidManifest)
}

func TestBootBeforeAnyManifest(t *testing.T) {
	env := newTestEnv(t)
	assertErrIs(t, env.host.BootCheck(env.image(1, "fw-1", "fw-2")), manifest.ErrNoState)
}
