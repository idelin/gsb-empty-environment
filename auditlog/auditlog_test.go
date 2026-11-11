package auditlog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/uid1279/auditlog"
	"example.org/netsec/uid1279/auditlog/faultfile"
)

func testKeys(t *testing.T) auditlog.Keys {
	t.Helper()
	keys, err := auditlog.GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys: %v", err)
	}
	return keys
}

func appendN(t *testing.T, writer *auditlog.Writer, start, count int) {
	t.Helper()
	for i := start; i < start+count; i++ {
		payload := []byte(fmt.Sprintf("event-%04d", i))
		seq, err := writer.Append(payload)
		if err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if seq != uint64(i+1) {
			t.Fatalf("Append returned seq %d, want %d", seq, i+1)
		}
	}
}

func assertReport(t *testing.T, report *auditlog.Report, records int, sealedSegments, tornTails int) {
	t.Helper()
	if len(report.Records) != records {
		t.Fatalf("records = %d, want %d", len(report.Records), records)
	}
	sealed := 0
	for _, status := range report.Segments {
		if status.Sealed {
			sealed++
		}
	}
	if sealed != sealedSegments {
		t.Fatalf("sealed segments = %d, want %d", sealed, sealedSegments)
	}
	if len(report.TornTails) != tornTails {
		t.Fatalf("torn tails = %d, want %d", len(report.TornTails), tornTails)
	}
	for i, record := range report.Records {
		want := []byte(fmt.Sprintf("event-%04d", record.Sequence-1))
		if string(record.Payload) != string(want) {
			t.Fatalf("record %d = (seq=%d,payload=%q), want payload %q",
				i, record.Sequence, record.Payload, want)
		}
	}
}

func TestSegmentRotationAndRead(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)

	writer, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithMaxSegmentRecords(3))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, writer, 0, 7)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("segment files = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".log" {
			t.Fatalf("unexpected unsealed file after Close: %s", entry.Name())
		}
	}

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	assertReport(t, report, 7, 3, 0)
}

func TestTornTailIsRecoveredAndPreserved(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)
	controller, opener := faultfile.New()

	writer, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithOpener(opener), auditlog.WithMaxSegmentRecords(100))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, writer, 0, 4)

	// A power loss mid-write: only part of the fifth record reaches disk.
	partial := controller.Last()
	before := partial.DurableSize()
	controller.ArmSyncLimit(before + 20)
	if _, err := writer.Append([]byte("event-0004-torn")); err == nil {
		t.Fatal("expected torn sync error")
	}
	if err := partial.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	// The failed writer is frozen: no further appends without a fresh Open.
	if _, err := writer.Append([]byte("must-be-rejected")); err == nil {
		t.Fatal("failed writer must reject further appends")
	}

	tornFiles, err := filepath.Glob(filepath.Join(dir, "*.part"))
	if err != nil || len(tornFiles) != 1 {
		t.Fatalf("torn part file missing: %v %v", tornFiles, err)
	}
	tornInfoBefore, err := os.Stat(tornFiles[0])
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	report, err := auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir after power loss: %v", err)
	}
	assertReport(t, report, 0, 0, 1)
	if got := report.Segments[0].Records; got != 4 {
		t.Fatalf("residue segment records = %d, want 4", got)
	}
	if report.TornTails[0].Bytes != 20 {
		t.Fatalf("torn bytes = %d, want 20", report.TornTails[0].Bytes)
	}

	// Reopen: the damaged evidence must remain byte-for-byte untouched while
	// new appends land in a fresh segment.
	controller.ClearFaults()
	reopened, err := auditlog.Open(context.Background(), dir, keys,
		auditlog.WithOpener(opener))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if tails := reopened.TornTails(); len(tails) != 1 || tails[0].Bytes != 20 {
		t.Fatalf("TornTails = %+v", tails)
	}
	appendN(t, reopened, 4, 2)
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tornInfoAfter, err := os.Stat(tornFiles[0])
	if err != nil {
		t.Fatalf("Stat after recovery: %v", err)
	}
	if tornInfoAfter.Size() != tornInfoBefore.Size() {
		t.Fatalf("torn evidence overwritten: before=%d after=%d",
			tornInfoBefore.Size(), tornInfoAfter.Size())
	}

	report, err = auditlog.ReadDir(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("ReadDir after recovery: %v", err)
	}
	// Four authenticated records live in the preserved residue segment and two
	// more in the sealed recovery segment. Residue records are reported in
	// SegmentStatus but excluded from the global record set.
	assertReport(t, report, 2, 1, 1)
	if got := report.Segments[0].Records; got != 4 {
		t.Fatalf("residue segment records = %d, want 4", got)
	}
	if report.LastSequence != 6 {
		t.Fatalf("last sequence = %d, want 6", report.LastSequence)
	}
}

func TestCompleteRecordAuthFailureIsTamper(t *testing.T) {
	dir := t.TempDir()
	keys := testKeys(t)

	writer, err := auditlog.Open(context.Background(), dir, keys)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendN(t, writer, 0, 3)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, "segment-0000000000000000.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Flip a byte inside the payload of the second complete record.
	flip := auditlog.RecordHeaderLen + 3
	data[flip] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = auditlog.ReadDir(context.Background(), dir, keys)
	var tamperErr *auditlog.TamperError
	if !errors.As(err, &tamperErr) {
		t.Fatalf("expected TamperError, got %v", err)
	}
	if tamperErr.Kind != auditlog.TamperHMAC {
		t.Fatalf("kind = %s, want %s", tamperErr.Kind, auditlog.TamperHMAC)
	}

	// A writer must refuse to append onto a tampered log.
	if _, err := auditlog.Open(context.Background(), dir, keys); !errors.As(err, &tamperErr) {
		t.Fatalf("Open should reject tampered log, got %v", err)
	}
}
