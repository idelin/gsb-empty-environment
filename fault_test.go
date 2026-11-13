package netsec

import (
	"errors"
	"fmt"
	"testing"
)

var errInjected = errors.New("injected fault")

type faultStore struct {
	base          *MemoryStore
	failSaveAt    int
	failSaveAfter bool
	failLoad      bool
	saveCount     int
	failArmed     bool
}

func (s *faultStore) Load() ([]byte, error) {
	if s.failLoad {
		return nil, errInjected
	}
	return s.base.Load()
}

func (s *faultStore) Save(blob []byte) error {
	s.saveCount++
	if s.failArmed && s.saveCount == s.failSaveAt {
		s.failArmed = false
		if !s.failSaveAfter {
			return errInjected
		}
		if err := s.base.Save(blob); err != nil {
			return err
		}
		return errInjected
	}
	if s.failArmed && s.failSaveAt == 0 {
		s.failArmed = false
		if !s.failSaveAfter {
			return errInjected
		}
		if err := s.base.Save(blob); err != nil {
			return err
		}
		return errInjected
	}
	return s.base.Save(blob)
}

type faultFuse struct {
	base            *MemoryFuse
	failReadAt      int
	failRaiseBefore bool
	failRaiseAfter  bool
	failRaiseOnce   bool
	readBackStale   bool
	readCount       int
	raiseCount      int
	raisedFrom      uint32
	lastRaised      bool
}

func (f *faultFuse) ReadRollbackIndex() (uint32, error) {
	f.readCount++
	if f.failReadAt != 0 && f.readCount == f.failReadAt {
		return 0, errInjected
	}
	index, err := f.base.ReadRollbackIndex()
	if err == nil && f.readBackStale && f.lastRaised {
		f.lastRaised = false
		return f.raisedFrom, nil
	}
	return index, err
}

func (f *faultFuse) RaiseRollbackIndex(to uint32) error {
	f.raiseCount++
	if f.failRaiseOnce {
		f.failRaiseOnce = false
		if !f.failRaiseAfter {
			return errInjected
		}
	}
	before, _ := f.base.ReadRollbackIndex()
	if err := f.base.RaiseRollbackIndex(to); err != nil {
		return err
	}
	f.raisedFrom = before
	f.lastRaised = true
	if f.failRaiseAfter {
		f.failRaiseAfter = false
		return errInjected
	}
	return nil
}

type faultImages struct {
	base           *MemoryImages
	failWrite      bool
	failWriteAfter bool
	failReadAt     int
	readCount      int
	corruptReadAt  int
}

func (m *faultImages) WriteImage(slot SlotID, image []byte) error {
	if m.failWrite {
		m.failWrite = false
		return errInjected
	}
	if err := m.base.WriteImage(slot, image); err != nil {
		return err
	}
	if m.failWriteAfter {
		m.failWriteAfter = false
		return errInjected
	}
	return nil
}

func (m *faultImages) ReadImage(slot SlotID) ([]byte, error) {
	m.readCount++
	if m.failReadAt != 0 && m.readCount == m.failReadAt {
		return nil, errInjected
	}
	image, err := m.base.ReadImage(slot)
	if err != nil {
		return nil, err
	}
	if m.corruptReadAt != 0 && m.readCount == m.corruptReadAt {
		image[0] ^= 0xff
	}
	return image, nil
}

func testConfig(maxAttempts uint32) Config {
	return Config{
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
}

func testHost(t *testing.T, cfg Config, fuse RollbackFuse, store StateStore, images SlotImages) *Host {
	t.Helper()
	host, err := New(cfg, fuse, store, images)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestInstallFaultPoints(t *testing.T) {
	t.Run("image write rejected", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := NewMemoryStore()
		images := &faultImages{base: NewMemoryImages(), failWrite: true}
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrImageWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatal("failed image write produced a candidate")
		}
		if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
			t.Fatalf("boot after failed install = %v", err)
		}
	})

	t.Run("image write survives but returns error", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := NewMemoryStore()
		images := &faultImages{base: NewMemoryImages(), failWriteAfter: true}
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrImageWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatal("uncommitted image write produced a candidate")
		}
		if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
			t.Fatalf("boot after partial install = %v", err)
		}
	})

	t.Run("readback failure", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := NewMemoryStore()
		images := &faultImages{base: NewMemoryImages(), failReadAt: 1}
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrImageReadback) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatal("readback failure produced a candidate")
		}
		if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
			t.Fatalf("boot after readback failure = %v", err)
		}
	})

	t.Run("readback digest mismatch", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := NewMemoryStore()
		images := &faultImages{base: NewMemoryImages(), corruptReadAt: 1}
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrImageDigestMismatch) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatal("digest mismatch produced a candidate")
		}
	})

	t.Run("candidate state write rejected", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := &faultStore{base: NewMemoryStore(), failSaveAt: 1, failArmed: true}
		images := NewMemoryImages()
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrStateWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatal("failed candidate commit produced a candidate")
		}
		if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
			t.Fatalf("boot after failed candidate commit = %v", err)
		}
	})

	t.Run("candidate commit survives but returns error", func(t *testing.T) {
		baseFuse := NewMemoryFuse(0)
		baseStore := &faultStore{base: NewMemoryStore(), failSaveAt: 1, failArmed: true, failSaveAfter: true}
		images := NewMemoryImages()
		host := testHost(t, testConfig(3), baseFuse, baseStore, images)
		if err := host.Install(requestFor(imageAt(2), 2)); !errors.Is(err, ErrStateWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if !state.Slots[SlotB].Candidate {
			t.Fatal("committed candidate was lost")
		}
		next := testHost(t, testConfig(3), baseFuse, baseStore, images)
		session, err := next.BeginBoot()
		if err != nil {
			t.Fatal(err)
		}
		if session.Manifest.RollbackIndex != 2 {
			t.Fatalf("booted index = %d", session.Manifest.RollbackIndex)
		}
		fuse, _ := baseFuse.ReadRollbackIndex()
		if session.Manifest.RollbackIndex < fuse {
			t.Fatalf("booted index %d below fuse %d", session.Manifest.RollbackIndex, fuse)
		}
	})
}

func TestFuseFaultPoints(t *testing.T) {
	installCandidate := func(t *testing.T, cfg Config, fuse *faultFuse, store StateStore, images SlotImages) *Host {
		t.Helper()
		host := testHost(t, cfg, fuse, store, images)
		if err := host.Install(requestFor(imageAt(2), 2)); err != nil {
			t.Fatal(err)
		}
		return host
	}

	t.Run("raise fails before commit", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0)}
		store := NewMemoryStore()
		images := NewMemoryImages()
		stale := installCandidate(t, testConfig(3), fuse, store, images)
		fuse.failRaiseOnce = true
		fuse.failRaiseBefore = true
		if _, err := stale.BeginBoot(); !errors.Is(err, ErrFuseRaise) {
			t.Fatalf("error = %v", err)
		}
		if got, _ := fuse.base.ReadRollbackIndex(); got != 0 {
			t.Fatalf("fuse = %d after failed raise", got)
		}
		fuse.failRaiseBefore = false
		host := testHost(t, testConfig(3), fuse, store, images)
		if session, err := host.BeginBoot(); err != nil || session.Manifest.RollbackIndex != 2 {
			t.Fatalf("retry session = %+v, err = %v", session, err)
		}
	})

	t.Run("raise commits but returns error", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0)}
		store := NewMemoryStore()
		images := NewMemoryImages()
		stale := installCandidate(t, testConfig(3), fuse, store, images)
		fuse.failRaiseOnce = true
		fuse.failRaiseAfter = true
		if _, err := stale.BeginBoot(); !errors.Is(err, ErrFuseRaise) {
			t.Fatalf("error = %v", err)
		}
		if got, _ := fuse.base.ReadRollbackIndex(); got != 2 {
			t.Fatalf("fuse = %d after committed-but-error raise", got)
		}
		host := testHost(t, testConfig(3), fuse, store, images)
		session, err := host.BeginBoot()
		if err != nil {
			t.Fatal(err)
		}
		if session.Manifest.RollbackIndex < 2 {
			t.Fatalf("booted below raised fuse: %+v", session)
		}
	})

	t.Run("fuse readback fails", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0), failReadAt: 3}
		store := NewMemoryStore()
		images := NewMemoryImages()
		// Install read is #1. BeginBoot's first read is #2; the read before
		// raise is #3.
		fuse.readCount = 0
		host := installCandidate(t, testConfig(3), fuse, store, images)
		fuse.failReadAt = 3
		if _, err := host.BeginBoot(); !errors.Is(err, ErrFuseRead) {
			t.Fatalf("error = %v", err)
		}
		if got, _ := fuse.base.ReadRollbackIndex(); got != 0 {
			t.Fatalf("fuse raised despite read failure: %d", got)
		}
	})

	t.Run("fuse readback after raise fails", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0)}
		store := NewMemoryStore()
		images := NewMemoryImages()
		host := installCandidate(t, testConfig(3), fuse, store, images)
		fuse.failReadAt = fuse.readCount + 3 // initial read + pre-raise read + readback
		if _, err := host.BeginBoot(); !errors.Is(err, ErrFuseRead) {
			t.Fatalf("error = %v", err)
		}
		if got, _ := fuse.base.ReadRollbackIndex(); got != 2 {
			t.Fatalf("fuse = %d after readback crash", got)
		}
		next := testHost(t, testConfig(3), fuse, store, images)
		if session, err := next.BeginBoot(); err != nil || session.Manifest.RollbackIndex != 2 {
			t.Fatalf("retry session = %+v, err = %v", session, err)
		}
	})

	t.Run("stale readback rejects boot", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0), readBackStale: true}
		store := NewMemoryStore()
		images := NewMemoryImages()
		host := installCandidate(t, testConfig(3), fuse, store, images)
		if _, err := host.BeginBoot(); !errors.Is(err, ErrFuseVerify) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("fuse already ahead rejects low candidate", func(t *testing.T) {
		fuse := &faultFuse{base: NewMemoryFuse(0)}
		store := NewMemoryStore()
		images := NewMemoryImages()
		host := installCandidate(t, testConfig(3), fuse, store, images)
		if err := fuse.base.RaiseRollbackIndex(3); err != nil {
			t.Fatal(err)
		}
		if _, err := host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
			t.Fatalf("error = %v", err)
		}
		if got, _ := fuse.base.ReadRollbackIndex(); got != 3 {
			t.Fatalf("fuse lowered to %d", got)
		}
		state, _ := host.Inspect()
		if state.Slots[SlotB].Candidate {
			t.Fatalf("low candidate was not deleted: %+v", state.Slots[SlotB])
		}
	})
}

func TestConfirmationAndActiveSwitchFaultPoints(t *testing.T) {
	setup := func(t *testing.T, failAfter bool) (*Host, *faultStore, *MemoryFuse, *MemoryImages, *BootSession) {
		t.Helper()
		fuse := NewMemoryFuse(0)
		baseStore := NewMemoryStore()
		store := &faultStore{base: baseStore}
		images := NewMemoryImages()
		host := testHost(t, testConfig(3), fuse, store, images)
		if err := host.Install(requestFor(imageAt(1), 1)); err != nil {
			t.Fatal(err)
		}
		s, err := host.BeginBoot()
		if err != nil {
			t.Fatal(err)
		}
		if err := host.ConfirmBoot(confirmFor(s)); err != nil {
			t.Fatal(err)
		}
		if err := host.Install(requestFor(imageAt(2), 2)); err != nil {
			t.Fatal(err)
		}
		session, err := host.BeginBoot()
		if err != nil {
			t.Fatal(err)
		}
		store.failSaveAt = store.saveCount + 1
		store.failSaveAfter = failAfter
		store.failArmed = true
		return host, store, fuse, images, session
	}

	t.Run("confirmation state write rejected", func(t *testing.T) {
		host, _, fuse, images, session := setup(t, false)
		if err := host.ConfirmBoot(confirmFor(session)); !errors.Is(err, ErrStateWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Active != SlotB || state.Slots[SlotA].Success || !state.Slots[SlotA].Candidate {
			t.Fatalf("failed confirmation changed state: %+v", state)
		}
		if err := host.ConfirmBoot(confirmFor(session)); !errors.Is(err, ErrNoBootSession) {
			t.Fatalf("nonce replay after failed write = %v", err)
		}
		next := testHost(t, testConfig(3), fuse, NewMemoryStoreWithBlob(t, state), images)
		if boot, err := next.BeginBoot(); err != nil || boot.Manifest.RollbackIndex < 2 {
			t.Fatalf("retry boot = %+v, err = %v", boot, err)
		}
	})

	t.Run("confirmation commits but returns error", func(t *testing.T) {
		host, store, fuse, images, session := setup(t, true)
		if err := host.ConfirmBoot(confirmFor(session)); !errors.Is(err, ErrStateWrite) {
			t.Fatalf("error = %v", err)
		}
		state, _ := host.Inspect()
		if state.Active != SlotA || !state.Slots[SlotA].Success || state.Slots[SlotA].Candidate {
			t.Fatalf("committed confirmation state = %+v", state)
		}
		if err := host.ConfirmBoot(confirmFor(session)); !errors.Is(err, ErrNoBootSession) {
			t.Fatalf("nonce replay after committed write = %v", err)
		}
		next := testHost(t, testConfig(3), fuse, store, images)
		boot, err := next.BeginBoot()
		if err != nil {
			t.Fatal(err)
		}
		if boot.Slot != SlotA || boot.Candidate || boot.Manifest.RollbackIndex < 2 {
			t.Fatalf("post-crash boot = %+v", boot)
		}
		currentFuse, _ := fuse.ReadRollbackIndex()
		if boot.Manifest.RollbackIndex < currentFuse {
			t.Fatalf("boot index below fuse: %d < %d", boot.Manifest.RollbackIndex, currentFuse)
		}
	})
}

func TestConfirmAfterRolledBackStateStillRepairs(t *testing.T) {
	fuse := NewMemoryFuse(0)
	store := NewMemoryStore()
	images := NewMemoryImages()
	host := testHost(t, testConfig(3), fuse, store, images)
	if err := host.Install(requestFor(imageAt(2), 2)); err != nil {
		t.Fatal(err)
	}
	session, err := host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}

	// Attacker restores no ordinary state after the genuine candidate boot.
	store.ResetToNoState()
	if err := host.ConfirmBoot(confirmFor(session)); err != nil {
		t.Fatal(err)
	}
	state, _ := host.Inspect()
	if state.Active != SlotB || !state.Slots[SlotB].Success {
		t.Fatalf("confirmation did not repair state: %+v", state)
	}
}

func runStateWriteScenario(t *testing.T, store StateStore, fuse *MemoryFuse, images *MemoryImages) error {
	t.Helper()
	host := testHost(t, testConfig(3), fuse, store, images)
	if err := host.Install(requestFor(imageAt(1), 1)); err != nil {
		return err
	}
	session, err := host.BeginBoot()
	if err != nil {
		return err
	}
	if err := host.ConfirmBoot(confirmFor(session)); err != nil {
		return err
	}
	if err := host.Install(requestFor(imageAt(2), 2)); err != nil {
		return err
	}
	session, err = host.BeginBoot()
	if err != nil {
		return err
	}
	if err := host.ConfirmBoot(confirmFor(session)); err != nil {
		return err
	}
	if err := host.Install(requestFor(imageAt(3), 3)); err != nil {
		return err
	}
	if _, err := host.BeginBoot(); err != nil {
		return err
	}
	_, err = host.BeginBoot()
	return err
}

// TestEveryStateWriteFaultIsRollbackSafe fails every ordinary-state commit
// before and after durability along a realistic install/boot/confirm path.
// A fresh host is then allowed to recover from the surviving (old or new)
// snapshot. Any boot it permits must still be at or above the fuse.
func TestEveryStateWriteFaultIsRollbackSafe(t *testing.T) {
	countStore := &faultStore{base: NewMemoryStore()}
	if err := runStateWriteScenario(t, countStore, NewMemoryFuse(0), NewMemoryImages()); err != nil {
		t.Fatal(err)
	}
	if countStore.saveCount == 0 {
		t.Fatal("scenario made no state writes")
	}

	for failAt := 1; failAt <= countStore.saveCount; failAt++ {
		for _, after := range []bool{false, true} {
			name := "before"
			if after {
				name = "after"
			}
			t.Run(fmt.Sprintf("%s/save-%d", name, failAt), func(t *testing.T) {
				fuse := NewMemoryFuse(0)
				base := NewMemoryStore()
				store := &faultStore{base: base, failSaveAt: failAt, failArmed: true, failSaveAfter: after}
				images := NewMemoryImages()
				// An injected error stops the scenario exactly at the crash
				// boundary; the important check is what a reboot may start.
				_ = runStateWriteScenario(t, store, fuse, images)

				next := testHost(t, testConfig(3), fuse, store, images)
				if _, err := next.Recover(); err != nil {
					t.Fatalf("recovery after save failure: %v", err)
				}
				session, err := next.BeginBoot()
				if errors.Is(err, ErrRecoveryMode) || errors.Is(err, ErrSameIndexDigest) {
					return
				}
				if err != nil {
					t.Fatalf("boot after save failure: %v", err)
				}
				current, _ := fuse.ReadRollbackIndex()
				if session.Manifest.RollbackIndex < current {
					t.Fatalf("booted index %d below fuse %d", session.Manifest.RollbackIndex, current)
				}
				if session.Manifest.ImageDigest != DigestImage(session.Image) {
					t.Fatal("booted image does not match its manifest")
				}
			})
		}
	}
}

// MemoryStoreWithBlob is a small test helper that seeds the store with a
// state blob. It deliberately has no historical snapshots.
func NewMemoryStoreWithBlob(t *testing.T, state State) *MemoryStore {
	t.Helper()
	blob, err := EncodeState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := NewMemoryStore()
	s.current = append([]byte(nil), blob...)
	return s
}
