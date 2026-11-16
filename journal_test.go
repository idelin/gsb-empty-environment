package netsec

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileJournalRecoversTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torn.wal")
	base := testBaseTime()
	deadline := base.Add(time.Minute)
	nonce := testNonce(0x66)
	commitment := mustCommitment(t, "torn", "alice", nonce)

	journal, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("OpenFileJournal: %v", err)
	}
	host, err := NewHost(journal)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	startRound(t, host, "torn", []string{"alice"}, deadline, base)
	if err := host.Commit("torn", "alice", commitment[:], base.Add(time.Second)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open for torn append: %v", err)
	}
	if _, err := f.Write(recordMagic[:2]); err != nil {
		t.Fatalf("write torn header: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close torn append: %v", err)
	}

	journal, err = OpenFileJournal(path)
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	defer journal.Close()
	host, err = NewHost(journal)
	if err != nil {
		t.Fatalf("replay with torn tail: %v", err)
	}
	if state := mustState(t, host, "torn"); state.Commitments != 1 || state.Reveals != 0 {
		t.Fatalf("replayed state = %+v", state)
	}
	if err := host.Reveal("torn", "alice", nonce, base.Add(2*time.Second)); err != nil {
		t.Fatalf("Reveal after recovery: %v", err)
	}
	if _, err := host.Result("torn"); err != nil {
		t.Fatalf("Result after recovery: %v", err)
	}
}

func TestFileJournalRejectsMiddleCorruption(t *testing.T) {
	first := frameRecord([]byte("first"))
	second := frameRecord([]byte("second"))
	corrupt := append(append([]byte(nil), first...), second...)
	corrupt[len(first)+recordHeaderSize] ^= 0xff
	corrupt = append(corrupt, frameRecord([]byte("third"))...)

	records, valid, err := splitRecords(corrupt)
	if !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("splitRecords error = %v, want ErrJournalCorrupt", err)
	}
	if len(records) != 1 || valid != len(first) {
		t.Fatalf("records = %d, valid = %d", len(records), valid)
	}
}

func TestFileJournalDropsCorruptFinalRecord(t *testing.T) {
	data := frameRecord([]byte("first"))
	bad := frameRecord([]byte("second"))
	bad[recordHeaderSize] ^= 0xff
	data = append(data, bad...)

	records, valid, err := splitRecords(data)
	if err != nil {
		t.Fatalf("splitRecords: %v", err)
	}
	if len(records) != 1 || valid != len(frameRecord([]byte("first"))) {
		t.Fatalf("records = %d, valid = %d", len(records), valid)
	}
}

func TestMemoryJournalRoundTrip(t *testing.T) {
	journal := NewMemoryJournal()
	if err := journal.Append([]byte("one")); err != nil {
		t.Fatalf("Append(one): %v", err)
	}
	if err := journal.Append([]byte("two")); err != nil {
		t.Fatalf("Append(two): %v", err)
	}
	var got []string
	if err := journal.Replay(func(record []byte) error {
		got = append(got, string(record))
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("records = %v", got)
	}
}

func frameRecord(payload []byte) []byte {
	out := make([]byte, recordHeaderSize+len(payload))
	copy(out[0:4], recordMagic[:])
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(out[8:12], crc32.Checksum(payload, crcTable))
	copy(out[recordHeaderSize:], payload)
	return out
}
