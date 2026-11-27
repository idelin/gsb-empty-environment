package zipguard

import (
	"encoding/binary"
	"errors"
	"testing"
)

// TestLFHOffsetPointsIntoCentralDirectory rejects a CDH whose local header
// offset points at central-directory bytes rather than a real LFH.
func TestLFHOffsetPointsIntoCentralDirectory(t *testing.T) {
	specs := []specEntry{
		{name: "a", content: []byte("first"), method: methodStored, hostSystem: 3},
		{name: "b", content: []byte("second"), method: methodStored, hostSystem: 3},
	}
	arc := buildArchive(specs, nil)
	eocdOff := indexSignature(arc, sigEOCD, 0)
	cdOff := int(binary.LittleEndian.Uint32(arc[eocdOff+16:]))
	secondCDH := indexSignature(arc, sigCDH, cdOff+1)
	patch32(arc, secondCDH+42, uint32(cdOff))
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrOverlap) && !errors.Is(err, ErrGap) {
		t.Fatalf("err = %v, want structural rejection", err)
	}
}

// TestForgedEOCDAtTailBoundary plants a fake EOCD signature right at the
// beginning of the 65535+22 byte scanning window.
func TestForgedEOCDAtTailBoundary(t *testing.T) {
	specs := []specEntry{{
		name:       "pad",
		content:    make([]byte, 64),
		method:     methodStored,
		hostSystem: 3,
	}}
	arc := buildArchive(specs, nil)
	fakeAt := len(arc) - eocdFixedSize - 100
	put32(arc[fakeAt:], sigEOCD)
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrMultipleEOCD) {
		t.Fatalf("err = %v, want ErrMultipleEOCD", err)
	}
}

// TestDuplicateReaderAmbivalence models the MasterKey-style "verifier reads
// first, runtime reads last" duplicate: both entries are structurally valid
// but the normalized names collide.
func TestDuplicateReaderAmbivalence(t *testing.T) {
	specs := []specEntry{
		{name: "classes.dex", content: []byte("VERIFIER-VIEW"), method: methodStored, hostSystem: 3},
		{name: "classes.dex", content: []byte("RUNTIME-VIEW!!"), method: methodStored, hostSystem: 3},
	}
	_, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
}

// TestEmptyNameRejected makes sure a zero-length name cannot slip through
// normalization as a hidden collision key.
func TestEmptyNameRejected(t *testing.T) {
	specs := []specEntry{{name: "x", content: []byte("v"), method: methodStored, hostSystem: 3}}
	arc := buildArchive(specs, nil)
	cdhOff := indexSignature(arc, sigCDH, 0)
	patch16(arc, cdhOff+28, 0)
	// The CDH name byte becomes an unreferenced byte; either way validation
	// must fail rather than accept an empty name.
	_, err := VerifyBytes(arc, testPolicy())
	if err == nil {
		t.Fatal("empty entry name accepted")
	}
}

// TestSplitArchiveRejected flips the EOCD disk numbers to emulate a
// multi-disk archive.
func TestSplitArchiveRejected(t *testing.T) {
	arc := buildArchive([]specEntry{
		{name: "a", content: []byte("x"), method: methodStored, hostSystem: 3},
	}, nil)
	eocdOff := indexSignature(arc, sigEOCD, 0)
	patch16(arc, eocdOff+4, 1)  // disk number
	patch16(arc, eocdOff+6, 0)  // CD start disk
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrSplitArchive) {
		t.Fatalf("err = %v, want ErrSplitArchive", err)
	}
}
