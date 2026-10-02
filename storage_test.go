package roaring

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStorage(t *testing.T) (*Storage, *FaultInjector) {
	t.Helper()
	dir := t.TempDir()
	fi := NewFaultInjector()
	return NewStorage(filepath.Join(dir, "bitmap.dat"), fi), fi
}

func sampleBitmap(seed uint64) *Bitmap {
	b := New()
	for i := uint64(0); i < 5000; i++ {
		b.Add(seed*1000000 + i*3)
	}
	b.AddRange(seed*1000000+500000, seed*1000000+700000)
	return b
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st, _ := newTestStorage(t)
	want := sampleBitmap(1)
	if err := st.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Equal(want) {
		t.Fatal("round trip mismatch")
	}
}

func TestLoadFreshStorageReturnsEmpty(t *testing.T) {
	st, _ := newTestStorage(t)
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cardinality() != 0 {
		t.Fatal("fresh storage should yield an empty bitmap")
	}
}

// Disk full mid-save: the write fails, the temp file is rolled back and
// the previously committed image stays intact.
func TestDiskFullRollback(t *testing.T) {
	st, fi := newTestStorage(t)
	v1 := sampleBitmap(1)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	fi.DiskFullAfter = 128 // only 128 bytes fit on the "disk"
	if err := st.Save(sampleBitmap(2)); err != ErrDiskFull {
		t.Fatalf("expected ErrDiskFull, got %v", err)
	}
	fi.Reset()
	if _, err := os.Stat(st.tmpPath()); !os.IsNotExist(err) {
		t.Fatal("temp file was not rolled back after disk-full")
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load after disk full: %v", err)
	}
	if !got.Equal(v1) {
		t.Fatal("state was not rolled back to the last committed image")
	}
}

// Sudden power loss mid-commit: a partial temp file is left behind but
// never committed; recovery ignores it and returns the last good image.
func TestPowerLossRecovery(t *testing.T) {
	st, fi := newTestStorage(t)
	v1 := sampleBitmap(1)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	fi.CrashAfterBytes = 512 // "power cut" after 512 bytes
	if err := st.Save(sampleBitmap(2)); err != ErrPowerLoss {
		t.Fatalf("expected ErrPowerLoss, got %v", err)
	}
	fi.Reset()
	// The crash leaves a partial temp file on disk.
	info, err := os.Stat(st.tmpPath())
	if err != nil {
		t.Fatalf("expected leftover temp file after power loss: %v", err)
	}
	if info.Size() != 512 {
		t.Fatalf("partial temp size = %d, want 512", info.Size())
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load after power loss: %v", err)
	}
	if !got.Equal(v1) {
		t.Fatal("recovery did not return the last committed image")
	}
	// Recovery cleans up the orphaned temp file.
	if _, err := os.Stat(st.tmpPath()); !os.IsNotExist(err) {
		t.Fatal("orphaned temp file was not cleaned up during recovery")
	}
}

// Power loss before the very first commit: storage comes back empty but
// consistent, and the next save succeeds.
func TestPowerLossBeforeFirstCommit(t *testing.T) {
	st, fi := newTestStorage(t)
	fi.CrashAfterBytes = 64
	if err := st.Save(sampleBitmap(1)); err != ErrPowerLoss {
		t.Fatalf("expected ErrPowerLoss, got %v", err)
	}
	fi.Reset()
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cardinality() != 0 {
		t.Fatal("uncommitted data must not be visible after a crash")
	}
	v2 := sampleBitmap(2)
	if err := st.Save(v2); err != nil {
		t.Fatalf("Save after crash: %v", err)
	}
	got, err = st.Load()
	if err != nil || !got.Equal(v2) {
		t.Fatal("storage did not heal after pre-commit crash")
	}
}

// Torn write / bit-rot on the main file: Load detects the CRC mismatch
// and self-heals from the backup image.
func TestCorruptMainSelfHeal(t *testing.T) {
	st, fi := newTestStorage(t)
	v1 := sampleBitmap(1)
	v2 := sampleBitmap(2)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if err := st.Save(v2); err != nil { // v1 becomes the backup
		t.Fatalf("Save v2: %v", err)
	}
	fi.CorruptMainOnCommit = true
	v3 := sampleBitmap(3)
	if err := st.Save(v3); err != nil { // commits v3, then corrupts main
		t.Fatalf("Save v3: %v", err)
	}
	fi.Reset()
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load should self-heal, got %v", err)
	}
	if !got.Equal(v2) {
		t.Fatal("self-heal did not restore the last intact image")
	}
	// The main file itself must have been repaired on disk.
	data, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := Unmarshal(data); err != nil {
		t.Fatal("main file was not repaired on disk")
	}
}

// Both copies destroyed: recovery reports ErrCorrupt instead of
// panicking or returning garbage.
func TestDoubleCorruptionFailsCleanly(t *testing.T) {
	st, _ := newTestStorage(t)
	if err := st.Save(sampleBitmap(1)); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if err := st.Save(sampleBitmap(2)); err != nil {
		t.Fatalf("Save v2: %v", err)
	}
	corruptFile(st.Path)
	corruptFile(st.bakPath())
	if _, err := st.Load(); err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}

// Truncated main file (partial sector write) must also trigger self-heal.
func TestTruncatedMainSelfHeal(t *testing.T) {
	st, _ := newTestStorage(t)
	v1 := sampleBitmap(1)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if err := st.Save(sampleBitmap(2)); err != nil {
		t.Fatalf("Save v2: %v", err)
	}
	data, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(st.Path, data[:len(data)/3], 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load should self-heal from truncation, got %v", err)
	}
	if !got.Equal(v1) {
		t.Fatal("self-heal after truncation restored the wrong image")
	}
}

// Repeated crashes must never lose the last committed state.
func TestRepeatedCrashStorm(t *testing.T) {
	st, fi := newTestStorage(t)
	v1 := sampleBitmap(1)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	for i := 0; i < 20; i++ {
		fi.CrashAfterBytes = i * 37 // crash at every possible offset
		if err := st.Save(sampleBitmap(uint64(i + 2))); err != ErrPowerLoss {
			t.Fatalf("round %d: expected ErrPowerLoss, got %v", i, err)
		}
		fi.Reset()
		got, err := st.Load()
		if err != nil {
			t.Fatalf("round %d: Load: %v", i, err)
		}
		if !got.Equal(v1) {
			t.Fatalf("round %d: committed state lost after crash storm", i)
		}
	}
}
