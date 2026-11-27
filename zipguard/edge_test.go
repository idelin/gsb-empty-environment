package zipguard

import (
	"errors"
	"testing"
)

func TestZeroLengthEntries(t *testing.T) {
	specs := []specEntry{
		{name: "empty-stored", content: nil, method: methodStored, hostSystem: 3},
		{name: "empty-deflate", content: nil, method: methodDeflate, hostSystem: 3},
		{name: "empty-dd/", content: nil, method: methodDeflate, dataDescriptor: true, hostSystem: 3},
	}
	rep, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
	if err != nil {
		t.Fatalf("zero-length entries rejected: %v", err)
	}
	if len(rep.Entries) != 3 {
		t.Fatalf("entries = %d", len(rep.Entries))
	}
}

func TestTinyEntryExemptFromRatio(t *testing.T) {
	// Tiny highly-compressible payloads sit below RatioFloorBytes and must
	// not trip the ratio bomb check.
	specs := []specEntry{{
		name: "tiny", content: []byte{0, 0, 0, 0},
		method: methodDeflate, hostSystem: 3,
	}}
	p := testPolicy()
	p.MaxCompressionRatio = 2
	if _, err := VerifyBytes(buildArchive(specs, nil), p); err != nil {
		t.Fatalf("tiny entry not ratio-exempt: %v", err)
	}
}

func TestArchiveSizeLimit(t *testing.T) {
	arc := buildArchive([]specEntry{regularSpec("a", make([]byte, 2048))}, nil)
	p := testPolicy()
	p.MaxArchiveSize = 16
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want size-limit rejection", err)
	}
}

func TestNonUnixHostZeroModeAccepted(t *testing.T) {
	// DOS host (0) with zero high word: no Unix mode to inspect.
	specs := []specEntry{{
		name: "dos.txt", content: []byte("plain"),
		method: methodStored, hostSystem: 0, externalAttrs: 0,
	}}
	rep, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
	if err != nil {
		t.Fatalf("DOS-host entry rejected: %v", err)
	}
	if rep.Entries[0].UnixMode != 0 {
		t.Fatalf("expected zero unix mode, got %#o", rep.Entries[0].UnixMode)
	}
}

func TestNonUnixHostWithModeBitsRejected(t *testing.T) {
	specs := []specEntry{{
		name: "x", content: []byte("x"),
		method: methodStored, hostSystem: 0, externalAttrs: 0o755 << 16,
	}}
	_, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
	if !errors.Is(err, ErrUnsafeMode) {
		t.Fatalf("err = %v, want ErrUnsafeMode", err)
	}
}

func TestDeflateBombAbortsEarly(t *testing.T) {
	// The per-entry quota must abort mid-stream instead of buffering the full
	// expansion. 16 MiB of zeroes deflates to a few KiB.
	specs := []specEntry{{
		name: "bomb", content: make([]byte, 16<<20),
		method: methodDeflate, hostSystem: 3,
	}}
	arc := buildArchive(specs, nil)
	p := testPolicy()
	p.MaxEntryUncompressed = 1 << 20
	p.MaxCompressionRatio = 1 << 30
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("err = %v, want ErrEntryTooLarge", err)
	}
}
