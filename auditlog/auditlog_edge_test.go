package auditlog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"example.org/netsec/uid1279/auditlog"
)

func TestEmptyPayloadAndConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)

	writer, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(100))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := writer.Append(nil); err != nil {
		t.Fatalf("empty payload: %v", err)
	}

	const goroutines = 8
	const perRoutine = 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				if _, err := writer.Append([]byte("concurrent")); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	want := 1 + goroutines*perRoutine
	if len(report.Records) != want {
		t.Fatalf("records = %d, want %d", len(report.Records), want)
	}
	for i, record := range report.Records {
		if record.Sequence != uint64(i+1) {
			t.Fatalf("sequence gap at %d: got %d", i, record.Sequence)
		}
	}
}

func TestCorruptedMagicInCompleteFrameIsTamper(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)
	buildSegments(t, dir, keys, 2, 2)

	path := filepath.Join(dir, "segment-0000000000000000.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Corrupt the magic byte of the second record; the frame stays complete.
	data[auditlog.RecordHeaderLen] = 0x77
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// The magic byte sits inside the authenticated header, so HMAC fails first.
	expectTamper(t, dir, keys, auditlog.TamperHMAC)
}

func TestTornSealInOpenPart(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)
	buildSegments(t, dir, keys, 2, 2)

	// Turn the sealed segment into an unsealed part and truncate the seal to a
	// complete-record, incomplete-seal tail.
	sealed := filepath.Join(dir, "segment-0000000000000000.log")
	data, err := os.ReadFile(sealed)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	sealStart := len(data) - auditlog.SealFrameLen
	if err := os.WriteFile(filepath.Join(dir, "segment-0000000000000000.part"),
		data[:sealStart+7], 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Remove(sealed); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(report.TornTails) != 1 {
		t.Fatalf("torn tails = %d, want 1", len(report.TornTails))
	}
	if got := report.Segments[0].Records; got != 2 {
		t.Fatalf("residue authenticated records = %d, want 2", got)
	}
}

func TestUnknownFileIsRejected(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)
	buildSegments(t, dir, keys, 1, 1)

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var tamperErr *auditlog.TamperError
	if _, err := auditlog.ReadDir(context.Background(), dir, keys); !errors.As(err, &tamperErr) {
		t.Fatalf("expected TamperError for foreign file, got %v", err)
	}
	if tamperErr.Kind != auditlog.TamperOrder {
		t.Fatalf("kind = %s", tamperErr.Kind)
	}
}

func TestWrongVerifyKeyRejectsSeal(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)
	buildSegments(t, dir, keys, 2, 2)

	other := testKeys(t)
	wrong := keys
	wrong.Verify = other.Verify
	expectTamper(t, dir, wrong, auditlog.TamperSeal)
}
