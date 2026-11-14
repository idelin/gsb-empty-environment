package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditChainDetectsTampering(t *testing.T) {
	clock := NewManualClock(time.Unix(1400, 0).UTC())
	journal := NewMemoryJournal()
	m := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x91}, 8), journal, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetireFromPeer(g.Sequence); err != nil {
		t.Fatal(err)
	}
	records, err := journal.Records()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuditChain(records); err != nil {
		t.Fatal(err)
	}
	key, ok := m.checkpointKey()
	if !ok {
		t.Fatal("no checkpoint key")
	}
	if err := VerifyAuditMACs(records, key); err != nil {
		t.Fatal(err)
	}
	tampered := copyAudit(records)
	tampered[1].Reason = "forged"
	if err := VerifyAuditChain(tampered); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("tampered chain error = %v", err)
	}
	if _, err := NewMemoryJournalFrom(tampered); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("NewMemoryJournalFrom tampered error = %v", err)
	}
	macTampered := copyAudit(records)
	if macTampered[0].MAC[0] == 'a' {
		macTampered[0].MAC = "b" + macTampered[0].MAC[1:]
	} else {
		macTampered[0].MAC = "a" + macTampered[0].MAC[1:]
	}
	if err := VerifyAuditMACs(macTampered, key); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("tampered MAC error = %v", err)
	}
	journalTampered, err := NewMemoryJournalFrom(macTampered)
	if err != nil {
		t.Fatal(err)
	}
	m2 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x95}, 8), journalTampered, "edge-a", "c1"))
	cp, err := m.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Recover(journalTampered, cp); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("recovery accepted tampered MAC: %v", err)
	}
}

func TestCheckpointMACDetectsRollbackAndTamper(t *testing.T) {
	clock := NewManualClock(time.Unix(1500, 0).UTC())
	m := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x92}, 8), NewMemoryJournal(), "edge-a", "c1"))
	if _, err := m.IssueCID(); err != nil {
		t.Fatal(err)
	}
	cp, err := m.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyCheckpoint(cp); err != nil {
		t.Fatal(err)
	}
	bad := cp
	bad.Sequence++
	if err := m.VerifyCheckpoint(bad); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("sequence tamper error = %v", err)
	}
	bad = cp
	if len(bad.Digest) > 0 {
		if bad.Digest[0] == 'a' {
			bad.Digest = "b" + bad.Digest[1:]
		} else {
			bad.Digest = "a" + bad.Digest[1:]
		}
	}
	if err := m.VerifyCheckpoint(bad); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("digest tamper error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := WriteCheckpointFile(path, cp); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpointFile(path, cp); err != nil {
		t.Fatalf("same checkpoint rewrite: %v", err)
	}
	older := cp
	if older.Sequence > 0 {
		older.Sequence--
	}
	if err := WriteCheckpointFile(path, older); !errors.Is(err, ErrRollbackDetected) {
		t.Fatalf("older checkpoint write error = %v", err)
	}
}

func TestFileJournalDropsInterruptedTailBeforeAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	clock := NewManualClock(time.Unix(1600, 0).UTC())
	fj, err := OpenFileJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	m1 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x93}, 8), fj, "edge-a", "c1"))
	if _, err := m1.IssueCID(); err != nil {
		t.Fatal(err)
	}
	cp, err := m1.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := fj.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":2,"kind":"cid.issued"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	fj2, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("open with partial tail: %v", err)
	}
	records, err := fj2.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("partial tail was treated as durable: %d records", len(records))
	}
	m2 := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0x94}, 8), fj2, "edge-a", "c1"))
	if err := m2.Recover(fj2, cp); err != nil {
		t.Fatalf("recover after partial tail: %v", err)
	}
	if _, err := m2.IssueCID(); err != nil {
		t.Fatalf("append after partial tail: %v", err)
	}
	if err := fj2.Close(); err != nil {
		t.Fatal(err)
	}
	fj3, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("reopen after append: %v", err)
	}
	defer fj3.Close()
	all, err := fj3.Records()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuditChain(all); err != nil {
		t.Fatalf("chain after partial-tail repair: %v", err)
	}
}
