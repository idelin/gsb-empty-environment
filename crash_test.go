package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fillRegion(t *testing.T, mem *MemDevice, id string, stripes int64, value byte) {
	t.Helper()
	buf := make([]byte, StripeSize)
	for i := range buf {
		buf[i] = value
	}
	for i := int64(0); i < stripes; i++ {
		if _, err := mem.WriteStripe(id, i, buf); err != nil {
			t.Fatalf("fill stripe %d: %v", i, err)
		}
	}
}

func TestPowerCutAtEveryErasePhase(t *testing.T) {
	const size = 3 * StripeSize
	cases := []struct {
		name  string
		hooks func(target int64) Faults
	}{
		{
			name: "before_stripe_1",
			hooks: func(target int64) Faults {
				return Faults{BeforeStripe: func(_ string, s int64) error {
					if s == target {
						return ErrPowerLoss
					}
					return nil
				}}
			},
		},
		{
			name: "after_write_stripe_1",
			hooks: func(target int64) Faults {
				return Faults{AfterWrite: func(_ string, s int64) error {
					if s == target {
						return ErrPowerLoss
					}
					return nil
				}}
			},
		},
		{
			name: "after_verify_stripe_1",
			hooks: func(target int64) Faults {
				return Faults{AfterVerify: func(_ string, s int64) error {
					if s == target {
						return ErrPowerLoss
					}
					return nil
				}}
			},
		},
		{
			name: "before_mark_clean",
			hooks: func(_ int64) Faults {
				return Faults{BeforeMarkClean: func(_ string) error { return ErrPowerLoss }}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mem := NewMemDevice()
			m := openTestManager(t, dir, WithDevice(mem), WithPattern(0xA5))

			mustCreate(t, m, "r1", size, "cred")
			if _, err := m.Allocate("r1", "tenant"); err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			fillRegion(t, mem, "r1", 3, 0xEE)

			m.SetFaults(tc.hooks(1))
			err := m.Release("r1", "cred")
			if !errors.Is(err, ErrPowerLoss) {
				t.Fatalf("Release error = %v, want power loss", err)
			}
			if st := mustStatus(t, m, "r1"); st.State != StateErasing {
				t.Fatalf("state immediately after cut = %s, want erasing", st.State)
			}
			if err := m.Close(); err != nil {
				t.Fatalf("Close after cut: %v", err)
			}

			// Reopen: the durable log must still say erasing, never clean.
			restarted := openTestManager(t, dir, WithDevice(mem), WithPattern(0xA5))
			st := mustStatus(t, restarted, "r1")
			if st.State != StateErasing {
				t.Fatalf("reopened state = %s, want erasing", st.State)
			}
			if st.Allocatable() {
				t.Fatalf("erasing region became allocatable after restart")
			}
			if _, err := restarted.Allocate("r1", "attacker"); !errors.Is(err, ErrNotAllocatable) {
				t.Fatalf("Allocate after restart = %v", err)
			}
			if err := restarted.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			st = mustStatus(t, restarted, "r1")
			if st.State != StateClean {
				t.Fatalf("state after Recover = %s, want clean", st.State)
			}
			checkPattern(t, mem.Data("r1"), size, 0xA5)
			for i := int64(0); i < 3; i++ {
				if n := mem.WriteCount("r1", i); n < 2 {
					t.Fatalf("stripe %d write count = %d, want at least provisioning+erase/recovery", i, n)
				}
			}
		})
	}
}

func TestReleaseRetryResumesInterruptedErase(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0x3C))
	mustCreate(t, m, "r1", 2*StripeSize, "cred")
	lease, err := m.Allocate("r1", "tenant")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	fillRegion(t, mem, "r1", 2, 0xD7)
	m.SetFaults(Faults{BeforeStripe: func(_ string, stripe int64) error {
		if stripe == 1 {
			return ErrPowerLoss
		}
		return nil
	}})
	if err := m.ReleaseLease(lease, "cred"); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("first ReleaseLease = %v, want power loss", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateErasing {
		t.Fatalf("state after cut = %s", st.State)
	}
	m.ClearFaults()
	if err := m.ReleaseLease(lease, "cred"); err != nil {
		t.Fatalf("retried ReleaseLease: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean {
		t.Fatalf("state after retry = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), 2*StripeSize, 0x3C)
}

func TestDefaultFileDevicePowerCutRecovery(t *testing.T) {
	dir := t.TempDir()
	m := openTestManager(t, dir, WithPattern(0x9C))
	const size = 2 * StripeSize
	mustCreate(t, m, "r1", size, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	path := regionFilePath(filepath.Join(dir, "regions"), "r1")
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open region file: %v", err)
	}
	old := make([]byte, size)
	for i := range old {
		old[i] = 0xEE
	}
	if _, err := f.WriteAt(old, 0); err != nil {
		t.Fatalf("write tenant data: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync tenant data: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close region file: %v", err)
	}

	m.SetFaults(Faults{BeforeStripe: func(_ string, stripe int64) error {
		if stripe == 1 {
			return ErrPowerLoss
		}
		return nil
	}})
	if err := m.Release("r1", "cred"); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("Release = %v, want power loss", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateErasing {
		t.Fatalf("state after cut = %s", st.State)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted := openTestManager(t, dir, WithPattern(0x9C))
	if st := mustStatus(t, restarted, "r1"); st.State != StateErasing {
		t.Fatalf("reopened state = %s", st.State)
	}
	if err := restarted.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if st := mustStatus(t, restarted, "r1"); st.State != StateClean {
		t.Fatalf("state after Recover = %s", st.State)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read region file: %v", err)
	}
	checkPattern(t, data, size, 0x9C)
}

func TestAutoRecoverResumesOnOpen(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0xB4))
	mustCreate(t, m, "r1", 2*StripeSize, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	fillRegion(t, mem, "r1", 2, 0x44)
	m.SetFaults(Faults{BeforeStripe: func(_ string, stripe int64) error {
		if stripe == 1 {
			return ErrPowerLoss
		}
		return nil
	}})
	if err := m.Release("r1", "cred"); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("Release = %v, want power loss", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted := openTestManager(t, dir, WithDevice(mem), WithPattern(0xB4), WithAutoRecover())
	if st := mustStatus(t, restarted, "r1"); st.State != StateClean {
		t.Fatalf("auto-recovered state = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), 2*StripeSize, 0xB4)
}

func TestTornFinalJournalRecordIsRecovered(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0x77))
	mustCreate(t, m, "r1", 2*StripeSize, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := m.BeginErase("r1", "cred"); err != nil {
		t.Fatalf("BeginErase: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	journalPath := filepath.Join(dir, "erase-journal.jsonl")
	f, err := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	// A power cut during an append leaves a partial, newline-less record.
	if _, err := f.WriteString(`{"v":1,"seq":`); err != nil {
		t.Fatalf("write torn record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close torn journal: %v", err)
	}

	restarted := openTestManager(t, dir, WithDevice(mem), WithPattern(0x77))
	if st := mustStatus(t, restarted, "r1"); st.State != StateErasing {
		t.Fatalf("state with torn log = %s, want erasing", st.State)
	}
	if err := restarted.Recover(); err != nil {
		t.Fatalf("Recover after torn log: %v", err)
	}
	if st := mustStatus(t, restarted, "r1"); st.State != StateClean {
		t.Fatalf("state after recovery = %s", st.State)
	}
	checkPattern(t, mem.Data("r1"), 2*StripeSize, 0x77)
}

func TestCorruptInteriorJournalRejected(t *testing.T) {
	dir := t.TempDir()
	first := openTestManager(t, dir)
	mustCreate(t, first, "r1", StripeSize, "cred")
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, "erase-journal.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	idx := bytes.Index(data, []byte{'\n'})
	if idx < 0 {
		t.Fatalf("journal has no line break: %q", data)
	}
	corrupt := append(append(append([]byte{}, data[:idx+1]...), []byte("this is not a record\n")...), data[idx+1:]...)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("write corrupt journal: %v", err)
	}
	if _, err := NewManager(dir); !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("NewManager on corrupt journal = %v", err)
	}
}

func TestNonJSONFinalJournalGarbageRejected(t *testing.T) {
	dir := t.TempDir()
	first := openTestManager(t, dir)
	mustCreate(t, first, "r1", StripeSize, "cred")
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	path := filepath.Join(dir, "erase-journal.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	if _, err := f.WriteString("not json"); err != nil {
		t.Fatalf("append garbage: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := NewManager(dir); !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("NewManager with non-JSON tail = %v", err)
	}
}

// TestCleanRecordNeedsEveryStripe proves the replay path independently
// re-checks the final gate: a forged clean record without checkpoints is
// rejected instead of producing an allocatable region.
func TestCleanRecordNeedsEveryStripe(t *testing.T) {
	dir := t.TempDir()
	m := openTestManager(t, dir)
	mustCreate(t, m, "r1", 2*StripeSize, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := m.BeginErase("r1", "cred"); err != nil {
		t.Fatalf("BeginErase: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, "erase-journal.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	seq := uint64(strings.Count(string(data), "\n") + 1)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	forged, err := json.Marshal(journalEntry{Version: journalVersion, Seq: seq, Kind: kindClean, Region: "r1"})
	if err != nil {
		t.Fatalf("marshal forged record: %v", err)
	}
	if _, err := fmt.Fprintf(f, "%s\n", forged); err != nil {
		t.Fatalf("append forged clean: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close forged journal: %v", err)
	}

	if _, err := NewManager(dir); !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("NewManager with forged clean = %v", err)
	}
}
