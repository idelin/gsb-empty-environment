package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	testApp     = "app/v1"
	testContent = "the quick brown fox jumps over the lazy dog"
)

type testKey struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   string
	key  Key
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestKey(t *testing.T) *testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	key := Key{Type: KeyTypeEd25519, Value: hex.EncodeToString(pub)}
	id, err := KeyID(key)
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	return &testKey{priv: priv, pub: pub, id: id, key: key}
}

// sign builds an envelope whose signed payload is the canonical JSON of v.
func sign(t *testing.T, v any, signers ...*testKey) []byte {
	t.Helper()
	canon, err := canonical(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	env := Envelope{Signed: json.RawMessage(canon)}
	for _, k := range signers {
		env.Signatures = append(env.Signatures, Signature{
			KeyID: k.id,
			Sig:   hex.EncodeToString(ed25519.Sign(k.priv, canon)),
		})
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func pInt64(v int64) *int64 { return &v }

func fileMeta(version int64, data []byte) FileMeta {
	h, err := hashHex("sha256", data)
	if err != nil {
		panic(err)
	}
	return FileMeta{
		Hashes:  map[string]string{"sha256": h},
		Length:  pInt64(int64(len(data))),
		Version: pInt64(version),
	}
}

type fixture struct {
	t      *testing.T
	clock  *fakeClock
	expire time.Time
	keys   map[string][]*testKey
	thr    map[string]int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f := &fixture{
		t:      t,
		clock:  &fakeClock{t: now},
		expire: now.Add(30 * 24 * time.Hour),
		keys:   map[string][]*testKey{},
		thr:    map[string]int64{},
	}
	for _, role := range []string{RootRole, TimestampRole, SnapshotRole, TargetsRole} {
		f.keys[role] = []*testKey{newTestKey(t)}
		f.thr[role] = 1
	}
	return f
}

func (f *fixture) key(role string) *testKey { return f.keys[role][0] }

func (f *fixture) rootEnvelope(version int64, extra ...*testKey) []byte {
	keys := map[string]Key{}
	roles := map[string]Role{}
	for _, role := range []string{RootRole, TimestampRole, SnapshotRole, TargetsRole} {
		ids := make([]string, 0, len(f.keys[role]))
		for _, k := range f.keys[role] {
			keys[k.id] = k.key
			ids = append(ids, k.id)
		}
		roles[role] = Role{KeyIDs: ids, Threshold: f.thr[role]}
	}
	r := Root{
		SignedCommon: SignedCommon{Type: RootRole, Version: version, Expires: f.expire},
		Keys:         keys,
		Roles:        roles,
	}
	signers := append([]*testKey{}, f.keys[RootRole]...)
	signers = append(signers, extra...)
	return sign(f.t, r, signers...)
}

func (f *fixture) bootstrap() *Store {
	store, err := NewStore(f.rootEnvelope(1), f.clock.now)
	if err != nil {
		f.t.Fatalf("bootstrap: %v", err)
	}
	return store
}

// chain holds a consistent set of linked metadata at the given versions.
type chain struct {
	targets []byte
	snap    []byte
	ts      []byte
}

func (f *fixture) buildChain(tsVer, snapVer, targVer int64) *chain {
	targs := Targets{
		SignedCommon: SignedCommon{Type: TargetsRole, Version: targVer, Expires: f.expire},
		Targets: map[string]FileMeta{
			testApp: fileMeta(targVer, []byte(testContent)),
		},
	}
	targEnv := sign(f.t, targs, f.key(TargetsRole))

	snap := Snapshot{
		SignedCommon: SignedCommon{Type: SnapshotRole, Version: snapVer, Expires: f.expire},
		Meta:         map[string]FileMeta{TargetsFile: fileMeta(targVer, targEnv)},
	}
	snapEnv := sign(f.t, snap, f.key(SnapshotRole))

	ts := Timestamp{
		SignedCommon: SignedCommon{Type: TimestampRole, Version: tsVer, Expires: f.expire},
		Meta:         map[string]FileMeta{SnapshotFile: fileMeta(snapVer, snapEnv)},
	}
	tsEnv := sign(f.t, ts, f.key(TimestampRole))

	return &chain{targets: targEnv, snap: snapEnv, ts: tsEnv}
}

func (f *fixture) publish(store *Store, c *chain) {
	f.t.Helper()
	if err := store.UpdateTimestamp(c.ts); err != nil {
		f.t.Fatalf("update timestamp: %v", err)
	}
	if err := store.UpdateSnapshot(c.snap); err != nil {
		f.t.Fatalf("update snapshot: %v", err)
	}
	if err := store.UpdateTargets(TargetsFile, c.targets); err != nil {
		f.t.Fatalf("update targets: %v", err)
	}
}

func TestFullHappyPathAndTargetVerification(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	c := f.buildChain(1, 1, 1)
	f.publish(store, c)

	if err := store.VerifyTarget(testApp, []byte(testContent)); err != nil {
		t.Fatalf("verify good target: %v", err)
	}
	if err := store.VerifyTarget(testApp, []byte("tampered payload")); !errors.Is(err, ErrHash) {
		t.Fatalf("tampered target: want ErrHash, got %v", err)
	}
	if err := store.VerifyTarget("missing/file", []byte(testContent)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target: want ErrNotFound, got %v", err)
	}

	// Equal-version refresh is allowed; a subsequent higher version installs.
	f.publish(store, f.buildChain(1, 1, 1))
	f.publish(store, f.buildChain(2, 2, 2))
	if got := store.TimestampVersion(); got != 2 {
		t.Fatalf("timestamp version = %d, want 2", got)
	}
}

func TestRollbackProtection(t *testing.T) {
	t.Run("root replay of current version", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		// Re-signing the same version (even with a fresh signature) must not advance.
		if err := store.UpdateRoot(f.rootEnvelope(1)); !errors.Is(err, ErrVersion) {
			t.Fatalf("want ErrVersion, got %v", err)
		}
		if store.RootVersion() != 1 {
			t.Fatalf("root version changed to %d", store.RootVersion())
		}
	})

	t.Run("root version skips ahead", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		if err := store.UpdateRoot(f.rootEnvelope(3)); !errors.Is(err, ErrVersion) {
			t.Fatalf("want ErrVersion, got %v", err)
		}
	})

	t.Run("timestamp snapshot targets monotonic", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		f.publish(store, f.buildChain(2, 2, 2))

		// A replayed timestamp v1 must be rejected.
		if err := store.UpdateTimestamp(f.buildChain(1, 2, 2).ts); !errors.Is(err, ErrRollback) {
			t.Fatalf("timestamp rollback: want ErrRollback, got %v", err)
		}

		// Snapshot v1 pinned by a fresh timestamp v3: snapshot version regresses.
		old := f.buildChain(3, 1, 2)
		if err := store.UpdateTimestamp(old.ts); err != nil {
			t.Fatalf("timestamp v3: %v", err)
		}
		if err := store.UpdateSnapshot(old.snap); !errors.Is(err, ErrRollback) {
			t.Fatalf("snapshot rollback: want ErrRollback, got %v", err)
		}

		// Targets v1 pinned by fresh timestamp/snapshot v3: targets version regresses.
		replay := f.buildChain(3, 3, 1)
		if err := store.UpdateTimestamp(replay.ts); err != nil {
			t.Fatalf("timestamp v3 again: %v", err)
		}
		if err := store.UpdateSnapshot(replay.snap); err != nil {
			t.Fatalf("snapshot v3: %v", err)
		}
		if err := store.UpdateTargets(TargetsFile, replay.targets); !errors.Is(err, ErrRollback) {
			t.Fatalf("targets rollback: want ErrRollback, got %v", err)
		}
	})
}

// expiredEnvelope re-signs the given role metadata with an expiry in the past,
// keeping every other field identical so expiry is the only failing check.
func expiredEnvelope(t *testing.T, role string, version int64, key *testKey, expire time.Time) []byte {
	t.Helper()
	common := SignedCommon{Type: role, Version: version, Expires: expire}
	var payload any
	switch role {
	case RootRole:
		payload = Root{SignedCommon: common}
	case TimestampRole:
		payload = Timestamp{SignedCommon: common, Meta: map[string]FileMeta{}}
	case SnapshotRole:
		payload = Snapshot{SignedCommon: common, Meta: map[string]FileMeta{}}
	case TargetsRole:
		payload = Targets{SignedCommon: common, Targets: map[string]FileMeta{}}
	default:
		t.Fatalf("unknown role %q", role)
	}
	return sign(t, payload, key)
}

func TestExpiredMetadataIsRejected(t *testing.T) {
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("timestamp", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		err := store.UpdateTimestamp(
			expiredEnvelope(t, TimestampRole, 1, f.key(TimestampRole), past))
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
		if store.HasTimestamp() {
			t.Fatal("expired timestamp was installed")
		}
	})

	t.Run("snapshot", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		f.publish(store, f.buildChain(1, 1, 1))
		err := store.UpdateSnapshot(
			expiredEnvelope(t, SnapshotRole, 2, f.key(SnapshotRole), past))
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
	})

	t.Run("targets", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		f.publish(store, f.buildChain(1, 1, 1))
		err := store.UpdateTargets(TargetsFile,
			expiredEnvelope(t, TargetsRole, 2, f.key(TargetsRole), past))
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
	})

	t.Run("root update", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		f.expire = past
		if err := store.UpdateRoot(f.rootEnvelope(2)); !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
		if store.RootVersion() != 1 {
			t.Fatal("expired root was installed")
		}
	})

	t.Run("bootstrap root", func(t *testing.T) {
		f := newFixture(t)
		f.expire = past
		if _, err := NewStore(f.rootEnvelope(1), f.clock.now); !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
	})

	t.Run("freeze: clock past expiry rejects refresh", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		f.publish(store, f.buildChain(1, 1, 1))
		// The mirror stops publishing; the client clock moves past the expiry.
		f.clock.t = f.expire.Add(time.Minute)
		if err := store.UpdateTimestamp(f.buildChain(1, 1, 1).ts); !errors.Is(err, ErrExpired) {
			t.Fatalf("want ErrExpired under advanced clock, got %v", err)
		}
	})
}

// rootPayload builds the typed root payload the fixture would emit for a
// version, without producing any signatures.
func (f *fixture) rootPayload(version int64) Root {
	keys := map[string]Key{}
	roles := map[string]Role{}
	for _, role := range []string{RootRole, TimestampRole, SnapshotRole, TargetsRole} {
		ids := make([]string, 0, len(f.keys[role]))
		for _, k := range f.keys[role] {
			keys[k.id] = k.key
			ids = append(ids, k.id)
		}
		roles[role] = Role{KeyIDs: ids, Threshold: f.thr[role]}
	}
	return Root{
		SignedCommon: SignedCommon{Type: RootRole, Version: version, Expires: f.expire},
		Keys:         keys,
		Roles:        roles,
	}
}

func TestRootSignedOnlyByNewKeysIsRejected(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	oldRootKey := f.key(RootRole)
	newRootKey := newTestKey(t)

	// The new root delegates the root role exclusively to the new key.
	f.keys[RootRole] = []*testKey{newRootKey}
	payload := f.rootPayload(2)

	// Signed only with the new key: old root's threshold is not reached.
	newOnly := sign(t, payload, newRootKey)
	if err := store.UpdateRoot(newOnly); !errors.Is(err, ErrSignature) {
		t.Fatalf("new-key-only root: want ErrSignature, got %v", err)
	}
	if store.RootVersion() != 1 {
		t.Fatal("unauthorized root was installed")
	}

	// Signed only with the old key: new root's own threshold is not reached.
	oldOnly := sign(t, payload, oldRootKey)
	if err := store.UpdateRoot(oldOnly); !errors.Is(err, ErrSignature) {
		t.Fatalf("old-key-only root: want ErrSignature, got %v", err)
	}
	if store.RootVersion() != 1 {
		t.Fatal("unauthorized root was installed")
	}
}

func TestLegalRootRotationDiscardsTimestampAndSnapshot(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	f.publish(store, f.buildChain(1, 1, 1))
	if !store.HasTimestamp() || !store.HasSnapshot() {
		t.Fatal("pre-rotation metadata missing")
	}

	oldRootKey := f.key(RootRole)
	newRootKey := newTestKey(t)
	f.keys[RootRole] = []*testKey{newRootKey}

	// The v2 root delegates to the new key but is signed by BOTH key sets.
	v2 := sign(t, f.rootPayload(2), oldRootKey, newRootKey)
	if err := store.UpdateRoot(v2); err != nil {
		t.Fatalf("legal rotation: %v", err)
	}
	if store.RootVersion() != 2 {
		t.Fatalf("root version = %d, want 2", store.RootVersion())
	}
	if store.HasTimestamp() || store.HasSnapshot() {
		t.Fatal("timestamp and snapshot must be discarded after root rotation")
	}

	// Ordered refresh is enforced again after rotation.
	if err := store.UpdateSnapshot(f.buildChain(2, 2, 2).snap); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("snapshot before timestamp: want ErrUnavailable, got %v", err)
	}
	f.publish(store, f.buildChain(2, 2, 2))

	// Replaying the old v1 root after rotation is rejected.
	f.keys[RootRole] = []*testKey{oldRootKey}
	if err := store.UpdateRoot(f.rootEnvelope(1)); !errors.Is(err, ErrVersion) {
		t.Fatalf("post-rotation replay: want ErrVersion, got %v", err)
	}
}

func TestSignatureThreshold(t *testing.T) {
	f := newFixture(t)
	f.keys[TimestampRole] = []*testKey{newTestKey(t), newTestKey(t)}
	f.thr[TimestampRole] = 2
	store := f.bootstrap()

	ts := Timestamp{
		SignedCommon: SignedCommon{Type: TimestampRole, Version: 1, Expires: f.expire},
		Meta:         map[string]FileMeta{},
	}
	oneSig := sign(t, ts, f.keys[TimestampRole][0])
	if err := store.UpdateTimestamp(oneSig); !errors.Is(err, ErrSignature) {
		t.Fatalf("one of two signatures: want ErrSignature, got %v", err)
	}

	twoSig := sign(t, ts, f.keys[TimestampRole][0], f.keys[TimestampRole][1])
	if err := store.UpdateTimestamp(twoSig); err != nil {
		t.Fatalf("threshold reached: %v", err)
	}

	// A signature from a key not delegated to timestamp is ignored.
	stranger := newTestKey(t)
	strangerOnly := sign(t, ts, stranger)
	if err := store.UpdateTimestamp(strangerOnly); !errors.Is(err, ErrSignature) {
		t.Fatalf("unauthorized key: want ErrSignature, got %v", err)
	}
}

func TestTimestampSnapshotHashPin(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	c := f.buildChain(1, 1, 1)

	if err := store.UpdateTimestamp(c.ts); err != nil {
		t.Fatalf("timestamp: %v", err)
	}

	// Append an extra signature without touching the signed payload: the bytes
	// no longer match the hash pinned by timestamp.
	var env Envelope
	if err := json.Unmarshal(c.snap, &env); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	stranger := newTestKey(t)
	canon, err := canonical(Snapshot{
		SignedCommon: SignedCommon{Type: SnapshotRole, Version: 1, Expires: f.expire},
		Meta:         map[string]FileMeta{TargetsFile: fileMeta(1, c.targets)},
	})
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	env.Signatures = append(env.Signatures, Signature{
		KeyID: stranger.id,
		Sig:   hex.EncodeToString(ed25519.Sign(stranger.priv, canon)),
	})
	tampered, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal tampered snapshot: %v", err)
	}
	if err := store.UpdateSnapshot(tampered); !errors.Is(err, ErrHash) {
		t.Fatalf("snapshot hash pin: want ErrHash, got %v", err)
	}

	// The genuine snapshot and targets still install cleanly.
	if err := store.UpdateSnapshot(c.snap); err != nil {
		t.Fatalf("genuine snapshot: %v", err)
	}
	if err := store.UpdateTargets(TargetsFile, c.targets); err != nil {
		t.Fatalf("targets: %v", err)
	}
}

func TestSnapshotTargetsVersionPin(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	c := f.buildChain(1, 1, 1)
	f.publish(store, c)

	// Snapshot claims targets v9 while the actual targets payload is v2.
	next := f.buildChain(2, 2, 2)
	snap := Snapshot{
		SignedCommon: SignedCommon{Type: SnapshotRole, Version: 2, Expires: f.expire},
		Meta:         map[string]FileMeta{TargetsFile: fileMeta(9, next.targets)},
	}
	next.snap = sign(t, snap, f.key(SnapshotRole))
	// Rebuild timestamp so its snapshot pin matches the new snapshot bytes.
	next.ts = sign(t, Timestamp{
		SignedCommon: SignedCommon{Type: TimestampRole, Version: 2, Expires: f.expire},
		Meta:         map[string]FileMeta{SnapshotFile: fileMeta(2, next.snap)},
	}, f.key(TimestampRole))

	if err := store.UpdateTimestamp(next.ts); err != nil {
		t.Fatalf("timestamp: %v", err)
	}
	if err := store.UpdateSnapshot(next.snap); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := store.UpdateTargets(TargetsFile, next.targets); !errors.Is(err, ErrRollback) {
		t.Fatalf("targets version pin: want ErrRollback, got %v", err)
	}
}

func TestUpdateOrderEnforced(t *testing.T) {
	f := newFixture(t)
	store := f.bootstrap()
	c := f.buildChain(1, 1, 1)

	if err := store.UpdateSnapshot(c.snap); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("snapshot without timestamp: want ErrUnavailable, got %v", err)
	}
	if err := store.UpdateTargets(TargetsFile, c.targets); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("targets without snapshot: want ErrUnavailable, got %v", err)
	}
	if err := store.VerifyTarget(testApp, []byte(testContent)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("verify without targets: want ErrUnavailable, got %v", err)
	}

	if err := store.UpdateTimestamp([]byte("{not json")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("garbage envelope: want ErrMalformed, got %v", err)
	}
	wrongType := sign(t, Snapshot{
		SignedCommon: SignedCommon{Type: SnapshotRole, Version: 1, Expires: f.expire},
		Meta:         map[string]FileMeta{},
	}, f.key(TimestampRole))
	if err := store.UpdateTimestamp(wrongType); !errors.Is(err, ErrType) {
		t.Fatalf("wrong role type: want ErrType, got %v", err)
	}
}

func TestMalformedBootstrapAndUnsupportedAlgorithms(t *testing.T) {
	t.Run("garbage and empty bootstrap root", func(t *testing.T) {
		clock := func() time.Time { return time.Now() }
		if _, err := NewStore([]byte("nonsense"), clock); !errors.Is(err, ErrMalformed) {
			t.Fatalf("garbage root: want ErrMalformed, got %v", err)
		}
		if _, err := NewStore([]byte(`{"signatures":[],"signed":{}}`), clock); !errors.Is(err, ErrMalformed) {
			t.Fatalf("empty root: want ErrMalformed, got %v", err)
		}
	})

	t.Run("root missing a role definition", func(t *testing.T) {
		f := newFixture(t)
		raw := f.rootEnvelope(1)
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		var root Root
		if err := json.Unmarshal(env.Signed, &root); err != nil {
			t.Fatalf("unmarshal signed: %v", err)
		}
		delete(root.Roles, SnapshotRole)
		if _, err := NewStore(sign(t, root, f.key(RootRole)), f.clock.now); !errors.Is(err, ErrMalformed) {
			t.Fatalf("missing role: want ErrMalformed, got %v", err)
		}
	})

	t.Run("unsupported hash algorithm on target verification", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()

		targs := Targets{
			SignedCommon: SignedCommon{Type: TargetsRole, Version: 1, Expires: f.expire},
			Targets: map[string]FileMeta{
				testApp: {Hashes: map[string]string{"md5": "deadbeef"}},
			},
		}
		targEnv := sign(t, targs, f.key(TargetsRole))
		snapEnv := sign(t, Snapshot{
			SignedCommon: SignedCommon{Type: SnapshotRole, Version: 1, Expires: f.expire},
			Meta:         map[string]FileMeta{TargetsFile: fileMeta(1, targEnv)},
		}, f.key(SnapshotRole))
		tsEnv := sign(t, Timestamp{
			SignedCommon: SignedCommon{Type: TimestampRole, Version: 1, Expires: f.expire},
			Meta:         map[string]FileMeta{SnapshotFile: fileMeta(1, snapEnv)},
		}, f.key(TimestampRole))

		if err := store.UpdateTimestamp(tsEnv); err != nil {
			t.Fatalf("timestamp: %v", err)
		}
		if err := store.UpdateSnapshot(snapEnv); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if err := store.UpdateTargets(TargetsFile, targEnv); err != nil {
			t.Fatalf("targets: %v", err)
		}
		if err := store.VerifyTarget(testApp, []byte(testContent)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported hash: want ErrUnsupported, got %v", err)
		}
	})

	t.Run("non-positive version rejected", func(t *testing.T) {
		f := newFixture(t)
		store := f.bootstrap()
		bad := sign(t, Timestamp{
			SignedCommon: SignedCommon{Type: TimestampRole, Version: 0, Expires: f.expire},
			Meta:         map[string]FileMeta{},
		}, f.key(TimestampRole))
		if err := store.UpdateTimestamp(bad); !errors.Is(err, ErrVersion) {
			t.Fatalf("zero version: want ErrVersion, got %v", err)
		}
	})
}
