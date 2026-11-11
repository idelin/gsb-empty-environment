package auditlog_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/uid1279/auditlog"
)

func buildSegments(t *testing.T, dir string, keys auditlog.Keys, total, perSegment int) {
	t.Helper()
	writer, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(perSegment))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, writer, 0, total)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func expectTamper(t *testing.T, dir string, keys auditlog.Keys, want auditlog.TamperKind) *auditlog.TamperError {
	t.Helper()
	_, err := auditlog.ReadDir(context.Background(), dir, keys)
	var tamperErr *auditlog.TamperError
	if !errors.As(err, &tamperErr) {
		t.Fatalf("expected TamperError(%s), got %v", want, err)
	}
	if tamperErr.Kind != want {
		t.Fatalf("tamper kind = %s, want %s (detail: %s)",
			tamperErr.Kind, want, tamperErr.Detail)
	}
	return tamperErr
}

func TestCrossSegmentSplicing(t *testing.T) {
	t.Run("mutated record breaks cross-segment chain", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 6, 3)

		path := filepath.Join(dir, "segment-0000000000000001.log")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		// First record header of segment 1 starts at offset zero; flip a byte
		// in its sequence number so the record no longer fits the chain.
		data[10] ^= 0x01
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		expectTamper(t, dir, keys, auditlog.TamperHMAC)
	})

	t.Run("deleted segment leaves an index gap", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 9, 3)

		if err := os.Remove(filepath.Join(dir, "segment-0000000000000001.log")); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		// The directory index itself no longer has contiguous segment numbers.
		expectTamper(t, dir, keys, auditlog.TamperOrder)
	})

	t.Run("duplicate segment index", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 3, 3)

		src := filepath.Join(dir, "segment-0000000000000000.log")
		duplicate := filepath.Join(dir, "segment-0000000000000000.part")
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if err := os.WriteFile(duplicate, data, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		expectTamper(t, dir, keys, auditlog.TamperOrder)
	})

	t.Run("foreign segment spliced into directory", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 3, 3)

		other := t.TempDir()
		otherKeys := testKeys(t)
		buildSegments(t, other, otherKeys, 3, 3)
		foreign, err := os.ReadFile(filepath.Join(other, "segment-0000000000000000.log"))
		if err != nil {
			t.Fatalf("ReadFile foreign: %v", err)
		}
		// Re-sign the foreign seal with the foreign key after renumbering it
		// as segment 1. The seal is cryptographically valid, but its chained
		// digests do not belong to this directory's log.
		sealStart := len(foreign) - auditlog.SealFrameLen
		foreign[sealStart+10] = 1
		sig := ed25519.Sign(otherKeys.Sign, foreign[sealStart:sealStart+auditlog.SealHeaderLen])
		copy(foreign[sealStart+auditlog.SealHeaderLen:], sig)
		if err := os.WriteFile(filepath.Join(dir, "segment-0000000000000001.log"),
			foreign, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		// The foreign records carry the foreign HMAC key, so authentication
		// fails before the chain check is even reached.
		expectTamper(t, dir, keys, auditlog.TamperHMAC)
	})
}

func TestSealTamper(t *testing.T) {
	t.Run("seal signature byte flipped", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 2, 2)

		path := filepath.Join(dir, "segment-0000000000000000.log")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		data[len(data)-1] ^= 0xFF
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		expectTamper(t, dir, keys, auditlog.TamperSeal)
	})

	t.Run("data appended after seal", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 2, 2)

		path := filepath.Join(dir, "segment-0000000000000000.log")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("OpenFile: %v", err)
		}
		if _, err := file.Write([]byte{0xAA, 0xBB, 0xCC}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		file.Close()

		expectTamper(t, dir, keys, auditlog.TamperSeal)
	})

	t.Run("sealed segment truncated before seal", func(t *testing.T) {
		dir := t.TempDir()
		keys := testKeys(t)
		buildSegments(t, dir, keys, 2, 2)

		path := filepath.Join(dir, "segment-0000000000000000.log")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if err := os.WriteFile(path, data[:auditlog.RecordHeaderLen+3], 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		expectTamper(t, dir, keys, auditlog.TamperFrame)
	})
}

func TestRestartAppendChainsAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)

	first, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(2))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, first, 0, 3)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(2))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if seq := second.LastSequence(); seq != 3 {
		t.Fatalf("LastSequence = %d, want 3", seq)
	}
	appendN(t, second, 3, 3)
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	assertReport(t, report, 6, 4, 0)

	// Reopening with mismatched HMAC key must fail HMAC verification.
	wrong := keys
	wrong.HMAC = append([]byte(nil), keys.HMAC...)
	wrong.HMAC[0] ^= 0xFF
	expectTamper(t, dir, wrong, auditlog.TamperHMAC)
}

func TestUnsealedCleanPartIsResumed(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)

	writer, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(10))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, writer, 0, 2)

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(report.Segments) != 1 || report.Segments[0].Sealed {
		t.Fatalf("expected one unsealed segment, got %+v", report.Segments)
	}

	resumed, err := auditlog.Open(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("resume Open: %v", err)
	}
	appendN(t, resumed, 2, 1)
	if err := resumed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	report, err = auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir after resume: %v", err)
	}
	assertReport(t, report, 3, 1, 0)
}
