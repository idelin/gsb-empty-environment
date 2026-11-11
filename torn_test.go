package batchlease

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func binaryU32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// TestTornTailRecovery appends partial frames, bad magic and zero padding
// behind valid records; reopen must truncate each torn tail and keep the
// durable prefix.
func TestTornTailRecovery(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	l := mustAcquire(t, s, "batch-A", 1, 3, "A")
	mustConfirm(t, s, l.Token, 1)
	logPath := filepath.Join(dir, logName)
	goodSize := s.LogSize()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Half a frame: torn power loss during a write.
	partial := buildFrame(recConfirm, encodeConfirm(2))
	base := mustReadFile(t, logPath)
	torn := append(append([]byte(nil), base...), partial[:len(partial)/2]...)
	if err := os.WriteFile(logPath, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, dir)
	if s.Confirmed() != 1 {
		t.Fatalf("torn half-frame not truncated: confirmed=%d size=%d want %d",
			s.Confirmed(), s.LogSize(), goodSize)
	}
	// Reopen appends one fresh epoch frame after truncating the torn tail.
	epochFrame := int64(len(buildFrame(recEpoch, encodeEpoch(s.Epoch()))))
	if s.LogSize() != goodSize+epochFrame {
		t.Fatalf("torn tail not reclaimed: size=%d want %d", s.LogSize(), goodSize+epochFrame)
	}
	mustConfirm(t, s, s.Leases()[0].Token, 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Garbage tail with bad magic (e.g. an overwritten sector).
	good2 := mustReadFile(t, logPath)
	garbage := append(append([]byte(nil), good2...), 0xDE, 0xAD, 0xBE, 0xEF)
	if err := os.WriteFile(logPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, dir)
	if s.Confirmed() != 2 {
		t.Fatalf("garbage tail not truncated: confirmed=%d size=%d want %d",
			s.Confirmed(), s.LogSize(), len(good2))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Zero-filled tail (zeroed allocation units after a valid prefix).
	good3 := mustReadFile(t, logPath)
	zeroTail := make([]byte, frameOverhead+7)
	zeroPadded := append(append([]byte(nil), good3...), zeroTail...)
	if err := os.WriteFile(logPath, zeroPadded, 0o600); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, dir)
	defer s.Close()
	if s.Confirmed() != 2 {
		t.Fatalf("zero tail not truncated: confirmed=%d size=%d want %d",
			s.Confirmed(), s.LogSize(), len(good3))
	}
	if id, ok := s.Identify(1); !ok || id != "batch-A" {
		t.Fatalf("identity after zero-tail recovery: %q %v", id, ok)
	}
}

// TestCorruptFrameRejected ensures corruption in the middle of the log is an
// error rather than silently dropping committed records.
func TestCorruptFrameRejected(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	l := mustAcquire(t, s, "batch-A", 1, 2, "A")
	mustConfirm(t, s, l.Token, 1)
	logPath := filepath.Join(dir, logName)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data := mustReadFile(t, logPath)

	// Corrupt a middle frame and append a fresh valid frame after it, so the
	// damaged frame is not a truncatable tail: replay must fail hard rather
	// than silently discard committed history.
	corrupt := append([]byte(nil), data...)
	epochFrame := buildFrame(recEpoch, encodeEpoch(99))
	// First frame is the epoch record; skip its full bytes to reach the
	// second (lease) frame, then flip a byte well inside its payload.
	firstFrameLen := frameOverhead + int(binaryU32(data[3:7]))
	target := firstFrameLen + headerSize + 4
	corrupt[target] ^= 0xFF
	corrupt = append(corrupt, epochFrame...)
	if err := os.WriteFile(logPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected Open to reject a corrupted committed frame")
	}
}

// TestShortWriteInjection makes the write path persist half a frame and then
// fail; reopen discards the torn half and keeps the durable prefix.
func TestShortWriteInjection(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	l := mustAcquire(t, s, "batch-A", 1, 3, "A")
	mustConfirm(t, s, l.Token, 1)
	goodSize := s.LogSize()

	fired := false
	s.hook = func(op string) error {
		if op == OpWrite && !fired {
			fired = true
			return ErrShortWrite
		}
		return nil
	}
	err := s.Confirm(l.Token, 2)
	if err == nil {
		t.Fatal("expected injected short write to fail Confirm")
	}
	if !s.failed || !errors.Is(err, ErrFailed) {
		t.Fatalf("store not poisoned after short write: failed=%v err=%v", s.failed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := reopenStore(t, dir)
	defer s2.Close()
	if s2.Confirmed() != 1 {
		t.Fatalf("durable prefix lost after short write: confirmed=%d", s2.Confirmed())
	}
	epochFrame := int64(len(buildFrame(recEpoch, encodeEpoch(s2.Epoch()))))
	if s2.LogSize() != goodSize+epochFrame {
		t.Fatalf("torn tail not reclaimed: size=%d want %d", s2.LogSize(), goodSize+epochFrame)
	}
	if id, ok := s2.Identify(3); !ok || id != "batch-A" {
		t.Fatalf("identity after short-write recovery: %q %v", id, ok)
	}
}
