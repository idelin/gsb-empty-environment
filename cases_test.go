package netsec

import (
	"errors"
	"testing"
)

func TestDirectReadWriteAndCommit(t *testing.T) {
	dev := NewSimDevice(0x30000)
	v := newTestValidator(t, dev)

	readWord := word(
		fieldAt(0, 1, opDirectRead),
		fieldAt(8, 8, 0x10000),
	)
	writeWord := word(
		fieldAt(0, 1, opDirectWrite),
		fieldAt(4, 4, 0x20000),
		fieldAt(8, 4, 0xdeadbeef),
	)
	plan, err := v.Prepare([][]byte{readWord, writeWord})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.WriteCount() != 1 {
		t.Fatalf("WriteCount = %d, want 1", plan.WriteCount())
	}
	wantPages := []uint64{0x10000, 0x20000}
	if got := plan.Pages(); !equalUint64(got, wantPages) {
		t.Fatalf("Pages = %#x, want %#x", got, wantPages)
	}
	if err := plan.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	var got [4]byte
	if err := dev.Read(0x20000, got[:]); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if binaryUint32(got[:]) != 0xdeadbeef {
		t.Fatalf("committed value = %#x", binaryUint32(got[:]))
	}
	if pages := dev.FrozenPages(); len(pages) != 0 {
		t.Fatalf("frozen pages after commit = %#x, want none", pages)
	}
	if err := plan.Commit(); !errors.Is(err, ErrPlanFinalized) {
		t.Fatalf("re-commit err = %v, want ErrPlanFinalized", err)
	}
}

func TestRejectUnauthorizedWrite(t *testing.T) {
	dev := NewSimDevice(0x30000)
	v := newTestValidator(t, dev)
	bad := word(
		fieldAt(0, 1, opDirectWrite),
		fieldAt(4, 4, 0x12345678),
		fieldAt(8, 4, 1),
	)
	_, err := v.Prepare([][]byte{bad})
	var ve *ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("err = %v, want ValidationError wrapping ErrWriteDenied", err)
	}
	if ve.Index != 0 {
		t.Fatalf("ValidationError.Index = %d, want 0", ve.Index)
	}
}

func TestRejectUnauthorizedRead(t *testing.T) {
	dev := NewSimDevice(0x30000)
	v := newTestValidator(t, dev)
	bad := word(
		fieldAt(0, 1, opDirectRead),
		fieldAt(8, 8, 0x50000),
	)
	if _, err := v.Prepare([][]byte{bad}); !errors.Is(err, ErrReadDenied) {
		t.Fatalf("err = %v, want ErrReadDenied", err)
	}
}

func TestUnknownOpcode(t *testing.T) {
	dev := NewSimDevice(0x30000)
	v := newTestValidator(t, dev)
	bad := word(fieldAt(0, 1, 0x99))
	if _, err := v.Prepare([][]byte{bad}); !errors.Is(err, ErrUnknownOpcode) {
		t.Fatalf("err = %v, want ErrUnknownOpcode", err)
	}
}

func equalUint64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func binaryUint32(buf []byte) uint32 {
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
}
