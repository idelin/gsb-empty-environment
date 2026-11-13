package netsec

import (
	"errors"
	"testing"
)

type fixture struct {
	host   *Host
	fuse   *MemoryFuse
	store  *MemoryStore
	images *MemoryImages
}

func newFixture(t *testing.T, initialFuse uint32, maxAttempts uint32) *fixture {
	t.Helper()
	images := NewMemoryImages()
	fuse := NewMemoryFuse(initialFuse)
	store := NewMemoryStore()
	cfg := Config{
		DeviceModel:        "board-1",
		CurrentBootVersion: 3,
		MaxAttempts:        maxAttempts,
		InitialActive:      SlotA,
		VerifyManifest: func(manifest Manifest, signature, image []byte) error {
			if string(signature) != "trusted-signature" {
				return errors.New("bad signature")
			}
			return nil
		},
	}
	host, err := New(cfg, fuse, store, images)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{host: host, fuse: fuse, store: store, images: images}
}

func (f *fixture) newHost(cfg Config) *Host {
	cfg.DeviceModel = "board-1"
	if cfg.CurrentBootVersion == 0 {
		cfg.CurrentBootVersion = 3
	}
	if cfg.InitialActive == 0 {
		cfg.InitialActive = SlotA
	}
	cfg.VerifyManifest = func(manifest Manifest, signature, image []byte) error {
		if string(signature) != "trusted-signature" {
			return errors.New("bad signature")
		}
		return nil
	}
	host, err := New(cfg, f.fuse, f.store, f.images)
	if err != nil {
		panic(err)
	}
	return host
}

func imageAt(index uint32) []byte {
	return []byte{byte(index), 0xa5, 0x5a, byte(index >> 8)}
}

func requestFor(image []byte, index uint32) InstallRequest {
	return InstallRequest{
		Image: image,
		Manifest: Manifest{
			DeviceModel:    "board-1",
			ImageDigest:    DigestImage(image),
			RollbackIndex:  index,
			MinBootVersion: 1,
		},
		Signature: []byte("trusted-signature"),
	}
}

func confirmFor(session *BootSession) BootConfirmation {
	return BootConfirmation{
		Nonce:       session.Nonce,
		Slot:        session.Slot,
		ImageDigest: session.ImageDigest,
	}
}

func stateSlot(t *testing.T, state State, slot SlotID) SlotState {
	t.Helper()
	return state.Slots[slot]
}

func TestLifecycleCandidateFuseAndAtomicSuccess(t *testing.T) {
	f := newFixture(t, 0, 3)
	if err := f.host.Install(requestFor(imageAt(2), 2)); err != nil {
		t.Fatal(err)
	}
	state, err := f.host.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	ss := stateSlot(t, state, SlotB)
	if !ss.Candidate || ss.Success || ss.Attempts != 0 || ss.VersionIndex != 2 {
		t.Fatalf("unexpected installed state: %+v", ss)
	}
	if state.Active != SlotA {
		t.Fatalf("install changed active slot to %s", state.Active)
	}
	if got, _ := f.fuse.ReadRollbackIndex(); got != 0 {
		t.Fatalf("install raised fuse to %d", got)
	}

	session, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if session.Slot != SlotB || !session.Candidate || session.Manifest.RollbackIndex != 2 {
		t.Fatalf("unexpected boot session: %+v", session)
	}
	if got, _ := f.fuse.ReadRollbackIndex(); got != 2 {
		t.Fatalf("fuse after candidate boot = %d, want 2", got)
	}
	state, _ = f.host.Inspect()
	ss = state.Slots[SlotB]
	if ss.Attempts != 1 || ss.Success || state.Active != SlotA {
		t.Fatalf("attempt/active state changed before confirmation: %+v", state)
	}

	if err := f.host.ConfirmBoot(BootConfirmation{
		Nonce: session.Nonce + "x", Slot: session.Slot, ImageDigest: session.ImageDigest,
	}); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("wrong nonce error = %v", err)
	}
	if err := f.host.ConfirmBoot(BootConfirmation{
		Nonce: session.Nonce, Slot: SlotA, ImageDigest: session.ImageDigest,
	}); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("wrong slot error = %v", err)
	}
	if err := f.host.ConfirmBoot(BootConfirmation{
		Nonce: session.Nonce, Slot: session.Slot, ImageDigest: DigestImage([]byte("other")),
	}); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("wrong digest error = %v", err)
	}
	if err := f.host.ConfirmBoot(confirmFor(session)); err != nil {
		t.Fatal(err)
	}
	state, _ = f.host.Inspect()
	ss = state.Slots[SlotB]
	if !ss.Success || ss.Candidate || ss.Attempts != 0 {
		t.Fatalf("confirmation did not commit success: %+v", ss)
	}
	if state.Active != SlotB {
		t.Fatalf("confirmation active = %s, want B", state.Active)
	}

	if err := f.host.ConfirmBoot(confirmFor(session)); !errors.Is(err, ErrNoBootSession) {
		t.Fatalf("nonce replay error = %v", err)
	}
	session2, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if session2.Slot != SlotB || session2.Candidate || session2.Nonce == session.Nonce {
		t.Fatalf("unexpected confirmed boot session: %+v", session2)
	}
	state, _ = f.host.Inspect()
	if state.Slots[SlotB].Attempts != 0 {
		t.Fatalf("confirmed image consumed an attempt: %+v", state.Slots[SlotB])
	}

	// Installing after the switch must write the newly inactive slot.
	if err := f.host.Install(requestFor(imageAt(3), 3)); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Install(requestFor(imageAt(1), 1)); !errors.Is(err, ErrRollback) {
		t.Fatalf("below-fuse install error = %v", err)
	}
}

func TestBeginBootInvalidatesPreviousNonce(t *testing.T) {
	f := newFixture(t, 0, 3)
	if err := f.host.Install(requestFor(imageAt(2), 2)); err != nil {
		t.Fatal(err)
	}
	s1, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if s1.Nonce == s2.Nonce {
		t.Fatal("boot nonce was reused")
	}
	if err := f.host.ConfirmBoot(confirmFor(s1)); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("old boot session was accepted: %v", err)
	}
	if err := f.host.ConfirmBoot(confirmFor(s2)); err != nil {
		t.Fatal(err)
	}
}

func TestManifestPolicies(t *testing.T) {
	f := newFixture(t, 0, 3)

	bad := requestFor(imageAt(2), 2)
	bad.Manifest.DeviceModel = "board-2"
	if err := f.host.Install(bad); !errors.Is(err, ErrDeviceMismatch) {
		t.Fatalf("device mismatch error = %v", err)
	}

	bad = requestFor(imageAt(2), 2)
	bad.Manifest.ImageDigest = DigestImage(imageAt(9))
	if err := f.host.Install(bad); !errors.Is(err, ErrImageDigestMismatch) {
		t.Fatalf("digest mismatch error = %v", err)
	}

	bad = requestFor(imageAt(2), 2)
	bad.Signature = []byte("forged")
	if err := f.host.Install(bad); !errors.Is(err, ErrManifestRejected) {
		t.Fatalf("invalid signature error = %v", err)
	}

	bad = requestFor(imageAt(2), 2)
	bad.Manifest.MinBootVersion = 4
	if err := f.host.Install(bad); !errors.Is(err, ErrMinBootVersion) {
		t.Fatalf("minimum boot version error = %v", err)
	}

	bad = requestFor(imageAt(2), 2)
	bad.Manifest.RollbackIndex = 1
	if err := f.fuse.RaiseRollbackIndex(2); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Install(bad); !errors.Is(err, ErrRollback) {
		t.Fatalf("below-fuse manifest error = %v", err)
	}
}

func TestEqualIndexDifferentDigestPolicy(t *testing.T) {
	f := newFixture(t, 0, 3)
	if err := f.host.Install(requestFor(imageAt(5), 5)); err != nil {
		t.Fatal(err)
	}
	s, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}

	// Slot A is inactive. A different image at the same index is rejected by
	// default before slot A is written.
	if err := f.host.Install(requestFor([]byte("different-image"), 5)); !errors.Is(err, ErrSameIndexDigest) {
		t.Fatalf("same-index conflict error = %v", err)
	}
	if _, err := f.images.ReadImage(SlotA); err == nil {
		t.Fatal("rejected install wrote slot A")
	}

	// The same authenticated image is allowed in the other slot.
	if err := f.host.Install(requestFor(imageAt(5), 5)); err != nil {
		t.Fatalf("same digest install failed: %v", err)
	}

	// With an explicit allow policy a conflicting candidate can be installed,
	// but boot still fails closed on the same-index conflict.
	allow := newFixture(t, 0, 3)
	allowHost := allow.newHost(Config{
		MaxAttempts:                    3,
		AllowEqualIndexDifferentDigest: true,
	})
	allow.host = allowHost
	if err := allow.host.Install(requestFor(imageAt(5), 5)); err != nil {
		t.Fatal(err)
	}
	s, err = allow.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := allow.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}
	if err := allow.host.Install(requestFor([]byte("different-image"), 5)); err != nil {
		t.Fatalf("allowed same-index install failed: %v", err)
	}
	if _, err := allow.host.BeginBoot(); err != nil {
		t.Fatalf("explicitly allowed conflicting boot failed: %v", err)
	}

	// The same persisted conflict must fail closed if policy is fixed to
	// reject equal-index/different-digest images.
	strict := allow.newHost(Config{MaxAttempts: 3})
	if _, err := strict.BeginBoot(); !errors.Is(err, ErrSameIndexDigest) {
		t.Fatalf("conflicting boot error = %v", err)
	}
}

func TestAttemptExhaustionFallsBackOnlyAtOrAboveFuse(t *testing.T) {
	f := newFixture(t, 0, 1)
	if err := f.host.Install(requestFor(imageAt(5), 5)); err != nil {
		t.Fatal(err)
	}
	s, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}

	// A same-index candidate in the other slot exhausts after one boot.
	if err := f.host.Install(requestFor(imageAt(5), 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.BeginBoot(); err != nil {
		t.Fatal(err)
	}
	fallback, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Slot != SlotB || fallback.Manifest.RollbackIndex < 5 {
		t.Fatalf("fallback selected unsafe session: %+v", fallback)
	}

	// A lower confirmed slot may not be used after the fuse has advanced.
	f2 := newFixture(t, 0, 1)
	f2.host.Install(requestFor(imageAt(5), 5))
	s, _ = f2.host.BeginBoot()
	if err := f2.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}
	if err := f2.host.Install(requestFor(imageAt(6), 6)); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.host.BeginBoot(); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
		t.Fatalf("below-fuse fallback error = %v", err)
	}
	if got, _ := f2.fuse.ReadRollbackIndex(); got != 6 {
		t.Fatalf("fuse changed during fallback: %d", got)
	}
}
