package netsec

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func testOptions(volumeID, generation uint64) Options {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return Options{
		VolumeID:      volumeID,
		KeyGeneration: generation,
		MasterKey:     key,
	}
}

func pattern(seed byte) []byte {
	p := make([]byte, LogicalSectorSize)
	for i := range p {
		p[i] = seed ^ byte(i)
	}
	return p
}

func mustWrite(t *testing.T, v *Volume, logical uint64, p []byte) {
	t.Helper()
	if err := v.Write(logical, p); err != nil {
		t.Fatalf("write logical %d: %v", logical, err)
	}
}

func mustRead(t *testing.T, v *Volume, logical uint64) []byte {
	t.Helper()
	p, err := v.Read(logical)
	if err != nil {
		t.Fatalf("read logical %d: %v", logical, err)
	}
	return p
}

func TestRoundTripAndEpoch(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(7, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	first := pattern(0x11)
	mustWrite(t, v, 9, first)
	if got := mustRead(t, v, 9); !bytes.Equal(got, first) {
		t.Fatal("first read mismatch")
	}
	state, err := v.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.WriteEpoch != 1 || state.CommitEpoch != 1 || state.RootRevision != 1 {
		t.Fatalf("unexpected state: %+v", state)
	}
	before, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].PhysicalGeneration != 1 {
		t.Fatalf("unexpected mapping: %+v", before)
	}

	second := pattern(0x77)
	mustWrite(t, v, 9, second)
	if got := mustRead(t, v, 9); !bytes.Equal(got, second) {
		t.Fatal("second read mismatch")
	}
	state, err = v.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.WriteEpoch != 2 || state.CommitEpoch != 2 || state.RootRevision != 2 {
		t.Fatalf("unexpected state after overwrite: %+v", state)
	}
	after, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].PhysicalGeneration < 2 {
		t.Fatalf("mapping was not rewritten with a new generation: %+v", after)
	}
}

func TestSectorSizeContract(t *testing.T) {
	if LogicalSectorSize != 4096 {
		t.Fatalf("logical sector size = %d", LogicalSectorSize)
	}
	v, err := NewVolume(NewMemoryDevice(32), testOptions(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Write(0, make([]byte, 4095)); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("short write error = %v", err)
	}
	if err := v.Write(0, make([]byte, 4097)); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("long write error = %v", err)
	}
}

func TestCopiedAndSwappedCiphertextRejected(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(12, 3))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	first := pattern(0x21)
	second := pattern(0x31)
	mustWrite(t, v, 1, first)
	mustWrite(t, v, 2, second)
	mappings, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 2 {
		t.Fatalf("mappings = %+v", mappings)
	}

	snapshot := dev.Snapshot()
	if err := dev.CopySector(mappings[1].Physical, mappings[0].Physical); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read(2); !errors.Is(err, ErrAuthentication) && !errors.Is(err, ErrPhysicalGeneration) {
		t.Fatalf("copied ciphertext read error = %v", err)
	}

	if err := dev.RestoreSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := dev.SwapSectors(mappings[0].Physical, mappings[1].Physical); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read(1); !errors.Is(err, ErrPhysicalGeneration) && !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped ciphertext read error = %v", err)
	}
	if _, err := v.Read(2); !errors.Is(err, ErrPhysicalGeneration) && !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped ciphertext read error = %v", err)
	}
}

func TestOldPhysicalIsZeroedAndReuseHasNewGeneration(t *testing.T) {
	dev := NewMemoryDevice(48)
	v, err := NewVolume(dev, testOptions(4, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	oldPlain := pattern(0x41)
	mustWrite(t, v, 5, oldPlain)
	oldMappings, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	oldRecord, err := dev.Sector(oldMappings[0].Physical)
	if err != nil {
		t.Fatal(err)
	}
	oldPhysical := oldMappings[0].Physical

	mustWrite(t, v, 5, pattern(0x42))
	zero, err := dev.SectorIsZero(oldPhysical)
	if err != nil {
		t.Fatal(err)
	}
	if !zero {
		t.Fatalf("unreferenced physical sector %d was not zeroed", oldPhysical)
	}

	mustWrite(t, v, 6, pattern(0x43))
	mappings, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	var reused *Mapping
	for i := range mappings {
		if mappings[i].Logical == 6 && mappings[i].Physical == oldPhysical {
			reused = &mappings[i]
		}
	}
	if reused == nil {
		t.Skip("allocator did not reuse the physical ID in this small run")
	}
	if reused.PhysicalGeneration <= oldMappings[0].PhysicalGeneration {
		t.Fatalf("reused generation did not advance: %+v", *reused)
	}
	if err := dev.WriteSector(oldPhysical, oldRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read(6); !errors.Is(err, ErrPhysicalGeneration) && !errors.Is(err, ErrAuthentication) {
		t.Fatalf("stale ciphertext in reused sector error = %v", err)
	}
}

func TestRelocationReencryptsAndInterruptionKeepsOldRoot(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(9, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	plain := pattern(0x51)
	mustWrite(t, v, 3, plain)
	before, _ := v.Mappings()
	beforeEpoch := v.WriteEpoch()
	oldCiphertext, err := dev.Sector(before[0].Physical)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Relocate(3); err != nil {
		t.Fatal(err)
	}
	after, _ := v.Mappings()
	if after[0].Physical == before[0].Physical && after[0].PhysicalGeneration == before[0].PhysicalGeneration {
		t.Fatal("relocation reused the same physical generation")
	}
	if v.WriteEpoch() <= beforeEpoch {
		t.Fatal("relocation did not advance write epoch")
	}
	if got := mustRead(t, v, 3); !bytes.Equal(got, plain) {
		t.Fatal("relocated plaintext mismatch")
	}
	newCiphertext, err := dev.Sector(after[0].Physical)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldCiphertext, newCiphertext) {
		t.Fatal("relocation copied old ciphertext")
	}

	failNextDataWrite := true
	dev.SetFaultInjector(func(ctx *FaultContext) error {
		if failNextDataWrite && ctx.Operation == DeviceWrite && ctx.Sector >= DataStartSector {
			failNextDataWrite = false
			ctx.TornBytes = PhysicalSectorSize / 2
			return ErrInjected
		}
		return nil
	})
	if err := v.Relocate(3); err == nil {
		t.Fatal("interrupted relocation unexpectedly succeeded")
	}
	dev.SetFaultInjector(nil)
	if got := mustRead(t, v, 3); !bytes.Equal(got, plain) {
		t.Fatal("old root was not readable after interrupted relocation")
	}
}

func TestTornEpochReservationDoesNotAdvanceOrWriteData(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(19, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	old := pattern(0x61)
	mustWrite(t, v, 1, old)
	epoch := v.WriteEpoch()
	before := dev.Snapshot()
	snapshot := dev.Snapshot()
	dev.SetFaultInjector(func(ctx *FaultContext) error {
		if ctx.Operation == DeviceWrite && ctx.Sector < DataStartSector {
			ctx.TornBytes = 64
			return ErrInjected
		}
		return nil
	})
	if err := v.Write(1, pattern(0x62)); err == nil {
		t.Fatal("write with torn epoch reservation succeeded")
	}
	dev.SetFaultInjector(nil)
	if v.WriteEpoch() != epoch {
		t.Fatalf("epoch advanced after an unpublished reservation: %d -> %d", epoch, v.WriteEpoch())
	}
	if got := mustRead(t, v, 1); !bytes.Equal(got, old) {
		t.Fatal("old plaintext was not preserved")
	}

	// A later successful write must recover and advance normally.
	mustWrite(t, v, 1, pattern(0x63))
	if v.WriteEpoch() != epoch+1 {
		t.Fatalf("recovered epoch = %d, want %d", v.WriteEpoch(), epoch+1)
	}
	_ = before
	_ = snapshot
}

func TestWriteEpochExhaustion(t *testing.T) {
	options := testOptions(3, 1)
	options.InitialWriteEpoch = math.MaxUint64
	v, err := NewVolume(NewMemoryDevice(48), options)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if err := v.Write(0, pattern(1)); !errors.Is(err, ErrWriteEpochExhausted) {
		t.Fatalf("write at max epoch error = %v", err)
	}
	if epoch := v.WriteEpoch(); epoch != math.MaxUint64 {
		t.Fatalf("epoch wrapped to %d", epoch)
	}
}

func TestRootRollbackDetectedAgainstObservedState(t *testing.T) {
	dev := NewMemoryDevice(64)
	options := testOptions(23, 1)
	v, err := NewVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}
	first := pattern(0x71)
	mustWrite(t, v, 1, first)
	snapshot := dev.Snapshot()
	second := pattern(0x72)
	mustWrite(t, v, 1, second)

	if err := dev.RestoreSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := v.Reload(); !errors.Is(err, ErrWriteEpochRollback) && !errors.Is(err, ErrRootRollback) {
		t.Fatalf("reload after root rollback error = %v", err)
	}
	if got, err := v.Read(1); err == nil && !bytes.Equal(got, second) {
		t.Fatal("read after rollback returned a stale plaintext")
	}
	_ = v.Close()

	options.MinimumWriteEpoch = 2
	if _, err := OpenVolume(dev, options); !errors.Is(err, ErrWriteEpochRollback) {
		t.Fatalf("open with external epoch anchor error = %v", err)
	}
}

func TestNewerOrphanEpochDetectsStateOnlyRollback(t *testing.T) {
	dev := NewMemoryDevice(64)
	options := testOptions(29, 1)
	v, err := NewVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, v, 1, pattern(0x73))

	stateSnapshot := make([]byte, StateSectorCount*PhysicalSectorSize)
	for slot := 0; slot < StateSectorCount; slot++ {
		record, err := dev.Sector(uint64(slot))
		if err != nil {
			t.Fatal(err)
		}
		copy(stateSnapshot[slot*PhysicalSectorSize:], record)
	}
	mustWrite(t, v, 1, pattern(0x74))
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	for slot := 0; slot < StateSectorCount; slot++ {
		record := stateSnapshot[slot*PhysicalSectorSize : (slot+1)*PhysicalSectorSize]
		if err := dev.WriteSector(uint64(slot), record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenVolume(dev, options); !errors.Is(err, ErrWriteEpochRollback) {
		t.Fatalf("state-only root rollback error = %v", err)
	}
}

func TestKeyRotationReencryptsAndInterruptionKeepsOldKey(t *testing.T) {
	dev := NewMemoryDevice(96)
	v, err := NewVolume(dev, testOptions(31, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	one := pattern(0x81)
	two := pattern(0x82)
	mustWrite(t, v, 1, one)
	mustWrite(t, v, 2, two)
	before, _ := v.Mappings()
	oldCiphertext, err := dev.Sector(before[0].Physical)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RotateKey(2); err != nil {
		t.Fatal(err)
	}
	state, err := v.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.KeyGeneration != 2 {
		t.Fatalf("key generation = %d", state.KeyGeneration)
	}
	if got := mustRead(t, v, 1); !bytes.Equal(got, one) {
		t.Fatal("sector 1 mismatch after rotation")
	}
	if got := mustRead(t, v, 2); !bytes.Equal(got, two) {
		t.Fatal("sector 2 mismatch after rotation")
	}
	after, _ := v.Mappings()
	newCiphertext, err := dev.Sector(after[0].Physical)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldCiphertext, newCiphertext) {
		t.Fatal("key rotation copied old ciphertext")
	}

	failNextDataWrite := true
	dev.SetFaultInjector(func(ctx *FaultContext) error {
		if failNextDataWrite && ctx.Operation == DeviceWrite && ctx.Sector >= DataStartSector {
			failNextDataWrite = false
			ctx.TornBytes = 32
			return ErrInjected
		}
		return nil
	})
	if err := v.RotateKey(3); err == nil {
		t.Fatal("interrupted key rotation unexpectedly succeeded")
	}
	dev.SetFaultInjector(nil)
	state, err = v.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.KeyGeneration != 2 {
		t.Fatalf("interrupted rotation changed key generation to %d", state.KeyGeneration)
	}
	if got := mustRead(t, v, 1); !bytes.Equal(got, one) {
		t.Fatal("old key generation was not readable after interruption")
	}
}

func TestReopenRestoresCommittedState(t *testing.T) {
	dev := NewMemoryDevice(96)
	options := testOptions(41, 1)
	v, err := NewVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}
	one := pattern(0x91)
	two := pattern(0x92)
	mustWrite(t, v, 1, one)
	mustWrite(t, v, 2, two)
	if err := v.RotateKey(4); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := v.State()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stateAfter, err := reopened.State()
	if err != nil {
		t.Fatal(err)
	}
	if stateAfter != stateBefore {
		t.Fatalf("state changed across reopen:\nbefore %+v\nafter  %+v", stateBefore, stateAfter)
	}
	if got := mustRead(t, reopened, 1); !bytes.Equal(got, one) {
		t.Fatal("sector 1 mismatch after reopen")
	}
	if got := mustRead(t, reopened, 2); !bytes.Equal(got, two) {
		t.Fatal("sector 2 mismatch after reopen")
	}
}

func TestManyMappingsTreeSplitAndReopen(t *testing.T) {
	dev := NewMemoryDevice(400)
	options := testOptions(43, 1)
	v, err := NewVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}

	const count = 320
	plain := make([][]byte, count)
	for i := 0; i < count; i++ {
		plain[i] = pattern(byte(i))
		logical := uint64(1000 + i*3)
		mustWrite(t, v, logical, plain[i])
	}
	mappings, err := v.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != count {
		t.Fatalf("mapping count = %d, want %d", len(mappings), count)
	}
	for i, mapping := range mappings {
		wantLogical := uint64(1000 + i*3)
		if mapping.Logical != wantLogical {
			t.Fatalf("mapping %d logical = %d, want %d", i, mapping.Logical, wantLogical)
		}
		if got := mustRead(t, v, wantLogical); !bytes.Equal(got, plain[i]) {
			t.Fatalf("read mismatch for logical %d", wantLogical)
		}
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenVolume(dev, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i := 0; i < count; i++ {
		logical := uint64(1000 + i*3)
		if got := mustRead(t, reopened, logical); !bytes.Equal(got, plain[i]) {
			t.Fatalf("read mismatch after reopen for logical %d", logical)
		}
	}
}

func TestTornRootCommitFallsBackToReservedRoot(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(47, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	old := pattern(0xa1)
	mustWrite(t, v, 1, old)
	epoch := v.WriteEpoch()
	stateWrites := 0
	dev.SetFaultInjector(func(ctx *FaultContext) error {
		if ctx.Operation == DeviceWrite && ctx.Sector < DataStartSector {
			stateWrites++
			if stateWrites == 3 {
				ctx.TornBytes = 72
				return ErrInjected
			}
		}
		return nil
	})
	if err := v.Write(1, pattern(0xa2)); err == nil {
		t.Fatalf("write with torn root commit succeeded after %d state writes", stateWrites)
	}
	dev.SetFaultInjector(nil)
	if v.WriteEpoch() != epoch+1 {
		t.Fatalf("reservation epoch = %d, want %d", v.WriteEpoch(), epoch+1)
	}
	if got := mustRead(t, v, 1); !bytes.Equal(got, old) {
		t.Fatal("root did not fall back to the reserved old root")
	}
	mustWrite(t, v, 1, pattern(0xa3))
	if v.WriteEpoch() != epoch+2 {
		t.Fatalf("post-recovery epoch = %d, want %d", v.WriteEpoch(), epoch+2)
	}
}

func TestUncertainStateSyncRequiresReload(t *testing.T) {
	dev := NewMemoryDevice(64)
	v, err := NewVolume(dev, testOptions(53, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	old := pattern(0xb1)
	mustWrite(t, v, 1, old)
	epoch := v.WriteEpoch()
	dev.SetFaultInjector(func(ctx *FaultContext) error {
		if ctx.Operation == DeviceSync {
			return ErrInjected
		}
		return nil
	})
	if err := v.Write(1, pattern(0xb2)); !errors.Is(err, ErrStateUncertain) {
		t.Fatalf("uncertain write error = %v", err)
	}
	dev.SetFaultInjector(nil)
	if _, err := v.Read(1); !errors.Is(err, ErrStateUncertain) {
		t.Fatalf("read before reload error = %v", err)
	}
	if err := v.Reload(); err != nil {
		t.Fatal(err)
	}
	if v.WriteEpoch() != epoch+1 {
		t.Fatalf("reloaded epoch = %d, want %d", v.WriteEpoch(), epoch+1)
	}
	if got := mustRead(t, v, 1); !bytes.Equal(got, old) {
		t.Fatal("old root was not readable after reload")
	}
}
