package netsec

import (
	"errors"
	"io"
	"testing"
)

func TestOmittedStripeNeverBecomesClean(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0xA5))
	const size = 3 * StripeSize
	mustCreate(t, m, "r1", size, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	fillRegion(t, mem, "r1", 3, 0xEE)

	// The device acknowledges the write but drops stripe 1.  The old tenant
	// byte 0xEE must be caught by read-back confirmation.
	mem.SetOmitStripe("r1", 1, true)
	err := m.Release("r1", "cred")
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Release error = %v, want verification failure", err)
	}
	st := mustStatus(t, m, "r1")
	if st.State != StateErasing {
		t.Fatalf("state after omitted stripe = %s, want erasing", st.State)
	}
	if st.VerifiedStripes != 1 {
		t.Fatalf("verified stripes = %d, want 1", st.VerifiedStripes)
	}

	// Retrying while the stripe is still dropped must keep failing closed.
	if err := m.ResumeErase("r1"); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("ResumeErase while omitted = %v", err)
	}
	st = mustStatus(t, m, "r1")
	if st.State != StateErasing || st.State == StateClean {
		t.Fatalf("omitted stripe produced state %s", st.State)
	}
	if _, err := m.Allocate("r1", "attacker"); !errors.Is(err, ErrNotAllocatable) {
		t.Fatalf("omitted stripe allocation = %v", err)
	}

	mem.SetOmitStripe("r1", 1, false)
	if err := m.ResumeErase("r1"); err != nil {
		t.Fatalf("ResumeErase after repair: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean {
		t.Fatalf("state after repaired erase = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), size, 0xA5)
}

func TestDroppedStripeOnBlankMemoryIsDetected(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	// A zero pattern is the hardest case: unwritten blank memory also reads as
	// zero, so a plain read-back alone would accept a dropped write.
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0x00))
	const size = 2 * StripeSize
	mustCreate(t, m, "r1", size, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	mem.SetOmitStripe("r1", 0, true)
	if err := m.Release("r1", "cred"); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Release with dropped blank stripe = %v", err)
	}
	st := mustStatus(t, m, "r1")
	if st.State != StateErasing || st.Allocatable() {
		t.Fatalf("dropped blank stripe state=%s allocatable=%v", st.State, st.Allocatable())
	}
	mem.SetOmitStripe("r1", 0, false)
	if err := m.ResumeErase("r1"); err != nil {
		t.Fatalf("ResumeErase: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean {
		t.Fatalf("state after repair = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), size, 0x00)
}

type shortWriteDevice struct {
	*MemDevice
	shortAt int64
	enabled bool
}

func (d *shortWriteDevice) WriteStripe(regionID string, index int64, p []byte) (int, error) {
	n, err := d.MemDevice.WriteStripe(regionID, index, p)
	if d.enabled && index == d.shortAt && err == nil {
		return n - 1, nil
	}
	return n, err
}

func TestShortStripeWriteNeverBecomesClean(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	dev := &shortWriteDevice{MemDevice: mem, shortAt: 1}
	m := openTestManager(t, dir, WithDevice(dev), WithPattern(0x6C))
	const size = 3 * StripeSize
	mustCreate(t, m, "r1", size, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	fillRegion(t, mem, "r1", 3, 0x11)

	dev.enabled = true
	err := m.Release("r1", "cred")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Release error = %v, want short write", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateErasing {
		t.Fatalf("state after short write = %s", st.State)
	}
	dev.enabled = false
	if err := m.ResumeErase("r1"); err != nil {
		t.Fatalf("ResumeErase: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean {
		t.Fatalf("state after fix = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), size, 0x6C)
}
