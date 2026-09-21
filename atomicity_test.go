package bitlayout_test

import (
	"bytes"
	"testing"

	"bitlayout"
)

// TestEncodeFailureLeavesBufferUntouched: a record whose last element is
// out of range must not modify the target buffer at all.
func TestEncodeFailureLeavesBufferUntouched(t *testing.T) {
	plan := mustCompile(t, sensorSchema)
	rec := map[string]any{
		"tag": uint64(1), "level": int64(7),
		"body": map[string]any{"count": uint64(3),
			// 4096 does not fit 12 bits: the LAST element is out of range
			"samples": []any{uint64(1), uint64(2), uint64(4096)}},
	}
	buf := bytes.Repeat([]byte{0xAA}, 16)
	before := append([]byte(nil), buf...)
	if _, err := plan.Encode(rec, buf); err == nil {
		t.Fatal("expected encode to fail")
	}
	if !bytes.Equal(buf, before) {
		t.Fatal("buffer modified by failed encode")
	}
}

// TestEncodeCapacityCheckedBeforeWrite: too-small buffers are rejected
// before any bit is written.
func TestEncodeCapacityCheckedBeforeWrite(t *testing.T) {
	plan, _, nbits, rec := encodeSensor(t)
	buf := bytes.Repeat([]byte{0x55}, int((nbits-1)/8)) // one byte too small
	before := append([]byte(nil), buf...)
	_, err := plan.Encode(rec, buf)
	if e, ok := err.(*bitlayout.Error); !ok || e.Kind != bitlayout.ErrCapacity {
		t.Fatalf("want capacity error, got %v", err)
	}
	if !bytes.Equal(buf, before) {
		t.Fatal("buffer modified by failed encode")
	}
}

// TestChecksumFailureDeliversNoRecord: a record failing the trailing
// checksum must not change the output object — Decode returns nil.
func TestChecksumFailureDeliversNoRecord(t *testing.T) {
	plan, buf, nbits, _ := encodeSensor(t)
	bad := append([]byte(nil), buf...)
	bad[7] ^= 0x80 // corrupt the checksum field itself
	rec, n, err := plan.Decode(bad, nbits, nil)
	if err == nil {
		t.Fatal("expected checksum failure")
	}
	if rec != nil || n != 0 {
		t.Fatalf("partial record delivered: %#v (%d bits)", rec, n)
	}

	// Same for the incremental session.
	s := plan.NewSession(nil)
	s.Feed(bad, nbits)
	rec, done, err := s.Try()
	if err == nil || done || rec != nil {
		t.Fatalf("session delivered partial record: done=%v rec=%#v err=%v", done, rec, err)
	}
}

// TestEncodeDoesNotTouchBitsBeyondRecord: bits past the record end stay
// as the caller had them.
func TestEncodeDoesNotTouchBitsBeyondRecord(t *testing.T) {
	plan, _, nbits, rec := encodeSensor(t)
	buf := bytes.Repeat([]byte{0xFF}, 16)
	n, err := plan.Encode(rec, buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != nbits {
		t.Fatalf("wrote %d bits, want %d", n, nbits)
	}
	for i := nbits; i < int64(len(buf))*8; i++ {
		if buf[i/8]&(0x80>>uint(i%8)) == 0 {
			t.Fatalf("bit %d beyond record was modified", i)
		}
	}
}
