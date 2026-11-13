package netsec

import (
	"errors"
	"testing"
)

func storeFromBlob(t *testing.T, blob []byte) *MemoryStore {
	t.Helper()
	store := NewMemoryStore()
	store.current = append([]byte(nil), blob...)
	return store
}

func TestRecoverSelectsHighestConfirmedAtOrAboveFuse(t *testing.T) {
	f := newFixture(t, 0, 3)
	if err := f.host.Install(requestFor(imageAt(1), 1)); err != nil {
		t.Fatal(err)
	}
	s, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Install(requestFor(imageAt(2), 2)); err != nil {
		t.Fatal(err)
	}
	s, err = f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}

	result, err := f.host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasTarget || result.RecoveryMode || result.Target != SlotA {
		t.Fatalf("recovery result = %+v", result)
	}
	if result.FuseIndex != 2 {
		t.Fatalf("recovery fuse = %d", result.FuseIndex)
	}
	ss := result.State.Slots[result.Target]
	if ss.VersionIndex != 2 || !ss.Success {
		t.Fatalf("recovery selected slot state = %+v", ss)
	}
}

func TestRecoverDeletesLowIndexCandidateAndKeepsFuse(t *testing.T) {
	imageA := imageAt(7)
	imageB := imageAt(6)
	state := NewState(SlotA)
	state.Slots[SlotA] = SlotState{
		ImageDigest:       DigestImage(imageA),
		Manifest:          requestFor(imageA, 7).Manifest,
		ManifestSignature: []byte("trusted-signature"),
		VersionIndex:      7,
		Success:           true,
	}
	state.Slots[SlotB] = SlotState{
		ImageDigest:       DigestImage(imageB),
		Manifest:          requestFor(imageB, 6).Manifest,
		ManifestSignature: []byte("trusted-signature"),
		VersionIndex:      6,
		Candidate:         true,
	}
	store := NewMemoryStoreWithBlob(t, state)
	images := NewMemoryImages()
	images.SetImage(SlotA, imageA)
	images.SetImage(SlotB, imageB)
	fuse := NewMemoryFuse(7)
	host := testHost(t, testConfig(3), fuse, store, images)

	result, err := host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoveryMode || !result.HasTarget || result.Target != SlotA {
		t.Fatalf("recovery result = %+v", result)
	}
	if result.State.Slots[SlotB].Candidate {
		t.Fatalf("low candidate survived: %+v", result.State.Slots[SlotB])
	}
	if got, _ := fuse.ReadRollbackIndex(); got != 7 {
		t.Fatalf("recovery changed fuse to %d", got)
	}
}

func TestRecoverWithoutEligibleImageEntersRecoveryMode(t *testing.T) {
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
	// A candidate at 7 may have raised the fuse before ordinary state was
	// lost. The confirmed 5 image must not be restarted.
	if err := f.fuse.RaiseRollbackIndex(7); err != nil {
		t.Fatal(err)
	}

	result, err := f.host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode || result.HasTarget {
		t.Fatalf("recovery result = %+v", result)
	}
	if got, _ := f.fuse.ReadRollbackIndex(); got != 7 {
		t.Fatalf("recovery lowered fuse to %d", got)
	}
	if _, err := f.host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
		t.Fatalf("boot in recovery mode = %v", err)
	}
}

func TestRecoverIgnoresUnauthenticatedSuccess(t *testing.T) {
	image := imageAt(8)
	state := NewState(SlotA)
	state.Slots[SlotA] = SlotState{
		ImageDigest:       DigestImage(image),
		Manifest:          requestFor(image, 8).Manifest,
		ManifestSignature: []byte("forged"),
		VersionIndex:      8,
		Success:           true,
	}
	store := NewMemoryStoreWithBlob(t, state)
	images := NewMemoryImages()
	images.SetImage(SlotA, image)
	fuse := NewMemoryFuse(8)
	host := testHost(t, testConfig(3), fuse, store, images)

	result, err := host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode || result.HasTarget {
		t.Fatalf("unauthenticated success selected: %+v", result)
	}
	if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
		t.Fatalf("boot of unauthenticated success = %v", err)
	}
}

func TestRecoverRejectsAmbiguousEqualIndexImages(t *testing.T) {
	imageA := imageAt(9)
	imageB := []byte("different-at-same-index")
	state := NewState(SlotB)
	for slot, image := range [SlotCount][]byte{imageA, imageB} {
		state.Slots[slot] = SlotState{
			ImageDigest:       DigestImage(image),
			Manifest:          requestFor(image, 9).Manifest,
			ManifestSignature: []byte("trusted-signature"),
			VersionIndex:      9,
			Success:           true,
		}
	}
	store := NewMemoryStoreWithBlob(t, state)
	images := NewMemoryImages()
	images.SetImage(SlotA, imageA)
	images.SetImage(SlotB, imageB)
	fuse := NewMemoryFuse(9)
	host := testHost(t, testConfig(3), fuse, store, images)

	result, err := host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode || result.HasTarget {
		t.Fatalf("ambiguous recovery result = %+v", result)
	}
}

func TestRecoverStateWriteFaultIsIdempotent(t *testing.T) {
	imageA := imageAt(7)
	imageB := imageAt(6)
	state := NewState(SlotA)
	state.Slots[SlotA] = SlotState{
		ImageDigest:       DigestImage(imageA),
		Manifest:          requestFor(imageA, 7).Manifest,
		ManifestSignature: []byte("trusted-signature"),
		VersionIndex:      7,
		Success:           true,
	}
	state.Slots[SlotB] = SlotState{
		ImageDigest:       DigestImage(imageB),
		Manifest:          requestFor(imageB, 6).Manifest,
		ManifestSignature: []byte("trusted-signature"),
		VersionIndex:      6,
		Candidate:         true,
	}
	blob, err := EncodeState(state)
	if err != nil {
		t.Fatal(err)
	}
	base := storeFromBlob(t, blob)
	store := &faultStore{base: base, failSaveAt: 1, failArmed: true, failSaveAfter: true}
	images := NewMemoryImages()
	images.SetImage(SlotA, imageA)
	images.SetImage(SlotB, imageB)
	fuse := NewMemoryFuse(7)
	host := testHost(t, testConfig(3), fuse, store, images)

	if _, err := host.Recover(); !errors.Is(err, ErrStateWrite) {
		t.Fatalf("recovery fault error = %v", err)
	}
	result, err := host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasTarget || result.Target != SlotA || result.State.Slots[SlotB].Candidate {
		t.Fatalf("recovery after retry = %+v", result)
	}
}
