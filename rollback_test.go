package netsec

import (
	"errors"
	"testing"
)

func TestOrdinaryStateRollbackEnumeration(t *testing.T) {
	f := newFixture(t, 0, 3)
	var imageSnapshots [][SlotCount][]byte

	recordNewState := func() {
		for len(imageSnapshots) < f.store.SnapshotCount() {
			imageSnapshots = append(imageSnapshots, f.images.Snapshot())
		}
	}
	if err := f.host.Install(requestFor(imageAt(1), 1)); err != nil {
		t.Fatal(err)
	}
	recordNewState()
	s, err := f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	recordNewState()
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}
	recordNewState()
	if err := f.host.Install(requestFor(imageAt(2), 2)); err != nil {
		t.Fatal(err)
	}
	recordNewState()
	s, err = f.host.BeginBoot()
	if err != nil {
		t.Fatal(err)
	}
	recordNewState()
	if err := f.host.ConfirmBoot(confirmFor(s)); err != nil {
		t.Fatal(err)
	}
	recordNewState()
	if err := f.host.Install(requestFor(imageAt(3), 3)); err != nil {
		t.Fatal(err)
	}
	recordNewState()
	for i := 0; i < 2; i++ {
		if _, err := f.host.BeginBoot(); err != nil {
			t.Fatal(err)
		}
		recordNewState()
	}
	finalFuse, err := f.fuse.ReadRollbackIndex()
	if err != nil {
		t.Fatal(err)
	}
	if finalFuse != 3 {
		t.Fatalf("final fuse = %d, want 3", finalFuse)
	}

	boots := 0
	recoveries := 0
	for i := 0; i < f.store.SnapshotCount(); i++ {
		blob, err := f.store.Snapshot(i)
		if err != nil {
			t.Fatal(err)
		}
		f.images.Restore(imageSnapshots[i])
		store := storeFromBlob(t, blob)
		host := testHost(t, testConfig(3), f.fuse, store, f.images)

		before, _ := f.fuse.ReadRollbackIndex()
		recovery, err := host.Recover()
		if err != nil {
			t.Fatalf("snapshot %d recovery: %v", i, err)
		}
		after, _ := f.fuse.ReadRollbackIndex()
		if after < before {
			t.Fatalf("snapshot %d lowered fuse %d -> %d", i, before, after)
		}
		if recovery.HasTarget {
			ss := recovery.State.Slots[recovery.Target]
			if ss.VersionIndex < after {
				t.Fatalf("snapshot %d recovery target index %d below fuse %d",
					i, ss.VersionIndex, after)
			}
		} else {
			recoveries++
		}

		session, err := host.BeginBoot()
		if err != nil {
			if errors.Is(err, ErrRecoveryMode) || errors.Is(err, ErrSameIndexDigest) {
				continue
			}
			t.Fatalf("snapshot %d boot: %v", i, err)
		}
		fuseNow, _ := f.fuse.ReadRollbackIndex()
		if session.Manifest.RollbackIndex < fuseNow {
			t.Fatalf("snapshot %d booted index %d below fuse %d",
				i, session.Manifest.RollbackIndex, fuseNow)
		}
		if session.Manifest.ImageDigest != DigestImage(session.Image) {
			t.Fatalf("snapshot %d boot image does not match manifest", i)
		}
		actual, err := f.images.ReadImage(session.Slot)
		if err != nil {
			t.Fatal(err)
		}
		if DigestImage(actual) != session.ImageDigest {
			t.Fatalf("snapshot %d boot image is not the slot image", i)
		}
		boots++
	}
	if boots == 0 {
		t.Fatal("rollback enumeration never exercised a safe boot")
	}
	if recoveries == 0 {
		t.Fatal("rollback enumeration never exercised recovery mode")
	}

	// Complete loss of ordinary state is also only a recovery event.
	f.store.ResetToNoState()
	result, err := f.host.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode {
		t.Fatalf("no-state recovery = %+v", result)
	}
	if got, _ := f.fuse.ReadRollbackIndex(); got != 3 {
		t.Fatalf("no-state recovery lowered fuse to %d", got)
	}
	if _, err := f.host.BeginBoot(); !errors.Is(err, ErrRecoveryMode) {
		t.Fatalf("no-state boot = %v", err)
	}
}
