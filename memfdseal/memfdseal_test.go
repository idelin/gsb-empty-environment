package memfdseal

import (
	"errors"
	"testing"
)

func mustWrite(t *testing.T, f *File, p []byte) {
	t.Helper()
	if _, err := f.Write(p); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestResidualSharedMapping: a leftover writable shared mapping blocks
// F_SEAL_WRITE with EBUSY, and even with every other seal installed the
// file is not deliverable while the mapping exists.
func TestResidualSharedMapping(t *testing.T) {
	f := New(true)
	mustWrite(t, f, []byte("config-v1"))

	m, err := f.Mmap(true, true)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}

	if err := f.AddSeals(SealWrite); !errors.Is(err, ErrBusy) {
		t.Fatalf("AddSeals(WRITE) with writable shared mapping = %v, want EBUSY", err)
	}

	// FUTURE_WRITE can be added, but the existing mapping stays writable.
	if err := f.AddSeals(SealFutureWrite | SealShrink | SealGrow | SealSeal); err != nil {
		t.Fatalf("AddSeals(FUTURE_WRITE|SHRINK|GROW|SEAL): %v", err)
	}
	if _, err := m.Write(0, []byte("pwned-v1!")); err != nil {
		t.Fatalf("residual mapping must stay writable after FUTURE_WRITE: %v", err)
	}
	if got, _ := m.Read(); string(got) != "pwned-v1!" {
		t.Fatalf("content = %q, want %q", got, "pwned-v1!")
	}

	if f.Deliverable() {
		t.Fatal("Deliverable with residual writable mapping")
	}
	if _, err := f.Dup(); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Dup = %v, want ErrNotDeliverable", err)
	}
}

// TestFutureWriteOnly: FUTURE_WRITE blocks new writes and new writable
// shared mappings, but pre-existing mappings keep writing, so the write-
// after-seal guarantee is not met.
func TestFutureWriteOnly(t *testing.T) {
	f := New(true)
	mustWrite(t, f, []byte("image-bytes"))

	m, err := f.Mmap(true, true)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	if err := f.AddSeals(SealFutureWrite); err != nil {
		t.Fatalf("AddSeals(FUTURE_WRITE): %v", err)
	}

	if _, err := f.Write([]byte("x")); !errors.Is(err, ErrPerm) {
		t.Fatalf("fd write after FUTURE_WRITE = %v, want EPERM", err)
	}
	if _, err := f.Mmap(true, true); !errors.Is(err, ErrPerm) {
		t.Fatalf("new writable shared mmap after FUTURE_WRITE = %v, want EPERM", err)
	}
	if _, err := m.Write(0, []byte("tampered-by")); err != nil {
		t.Fatalf("existing mapping must remain writable: %v", err)
	}
	if got, _ := m.Read(); string(got) != "tampered-by" {
		t.Fatalf("content = %q, want %q", got, "tampered-by")
	}

	if _, err := f.Dup(); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Dup = %v, want ErrNotDeliverable", err)
	}
}

// TestSealSealFirst: once F_SEAL_SEAL is installed no further seals can be
// added, so sealing SEAL first locks the file out of the delivery
// condition forever.
func TestSealSealFirst(t *testing.T) {
	f := New(true)
	mustWrite(t, f, []byte("payload"))

	if err := f.AddSeals(SealSeal); err != nil {
		t.Fatalf("AddSeals(SEAL): %v", err)
	}
	for _, s := range []Seal{SealWrite, SealShrink, SealGrow, SealFutureWrite} {
		if err := f.AddSeals(s); !errors.Is(err, ErrPerm) {
			t.Fatalf("AddSeals(%d) after SEAL = %v, want EPERM", s, err)
		}
	}

	// Content is still mutable: nothing was actually sealed.
	if _, err := f.Write([]byte("more")); err != nil {
		t.Fatalf("write after SEAL-only must succeed: %v", err)
	}
	if _, err := f.Dup(); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Dup = %v, want ErrNotDeliverable", err)
	}
}

// TestNoAllowSealing: a memfd created without MFD_ALLOW_SEALING rejects
// every F_ADD_SEALS attempt.
func TestNoAllowSealing(t *testing.T) {
	f := New(false)
	mustWrite(t, f, []byte("data"))
	if err := f.AddSeals(SealWrite | SealShrink | SealGrow | SealSeal); !errors.Is(err, ErrPerm) {
		t.Fatalf("AddSeals without MFD_ALLOW_SEALING = %v, want EPERM", err)
	}
	if _, err := f.Dup(); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Dup = %v, want ErrNotDeliverable", err)
	}
}

// TestCompliantSequence: build, verify through a mapping, unmap, seal
// everything, then hand off. After handoff the content is immutable.
func TestCompliantSequence(t *testing.T) {
	f := New(true)

	if err := f.Truncate(64); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	mustWrite(t, f, []byte("sandbox-config"))

	// Private writable mapping does not block F_SEAL_WRITE (copy-on-write).
	priv, err := f.Mmap(false, true)
	if err != nil {
		t.Fatalf("private mmap: %v", err)
	}

	shared, err := f.Mmap(true, true)
	if err != nil {
		t.Fatalf("shared mmap: %v", err)
	}
	if _, err := shared.Write(0, []byte("SANDBOX-CONFIG")); err != nil {
		t.Fatalf("write via shared mapping: %v", err)
	}
	if got, _ := shared.Read(); string(got[:14]) != "SANDBOX-CONFIG" {
		t.Fatalf("content = %q, want %q", got[:14], "SANDBOX-CONFIG")
	}
	if err := shared.Unmap(); err != nil {
		t.Fatalf("munmap: %v", err)
	}

	if err := f.AddSeals(SealWrite | SealShrink | SealGrow | SealSeal); err != nil {
		t.Fatalf("AddSeals(all): %v", err)
	}
	if !f.Deliverable() {
		t.Fatal("not deliverable after compliant sequence")
	}
	if _, err := f.Dup(); err != nil {
		t.Fatalf("Dup: %v", err)
	}

	// Everything is frozen now.
	if _, err := f.Write([]byte("x")); !errors.Is(err, ErrPerm) {
		t.Fatalf("write after full seal = %v, want EPERM", err)
	}
	if err := f.Truncate(4); !errors.Is(err, ErrPerm) {
		t.Fatalf("shrink after SHRINK seal = %v, want EPERM", err)
	}
	if err := f.Truncate(128); !errors.Is(err, ErrPerm) {
		t.Fatalf("grow after GROW seal = %v, want EPERM", err)
	}
	if _, err := f.Mmap(true, true); !errors.Is(err, ErrPerm) {
		t.Fatalf("writable shared mmap after WRITE seal = %v, want EPERM", err)
	}
	if err := f.AddSeals(SealFutureWrite); !errors.Is(err, ErrPerm) {
		t.Fatalf("AddSeals after SEAL = %v, want EPERM", err)
	}
	if _, err := priv.Write(0, []byte("cow")); err != nil {
		t.Fatalf("private mapping write (copy-on-write) must succeed: %v", err)
	}
	if got, _ := priv.Read(); string(got[:14]) != "SANDBOX-CONFIG" {
		t.Fatalf("private write leaked into file: %q", got[:14])
	}
}

// TestShrinkGrowSeals exercises the truncate seals independently.
func TestShrinkGrowSeals(t *testing.T) {
	f := New(true)
	mustWrite(t, f, []byte("0123456789"))

	if err := f.AddSeals(SealShrink); err != nil {
		t.Fatalf("AddSeals(SHRINK): %v", err)
	}
	if err := f.Truncate(4); !errors.Is(err, ErrPerm) {
		t.Fatalf("shrink with SHRINK seal = %v, want EPERM", err)
	}
	if err := f.Truncate(20); err != nil {
		t.Fatalf("grow without GROW seal: %v", err)
	}

	if err := f.AddSeals(SealGrow); err != nil {
		t.Fatalf("AddSeals(GROW): %v", err)
	}
	if err := f.Truncate(30); !errors.Is(err, ErrPerm) {
		t.Fatalf("grow with GROW seal = %v, want EPERM", err)
	}
	if err := f.Truncate(20); err != nil {
		t.Fatalf("same-size truncate must succeed: %v", err)
	}
}
