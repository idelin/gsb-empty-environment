package transcript

import (
	"errors"
	"testing"
)

func TestFieldLengthLimits(t *testing.T) {
	big := make([]byte, MaxFieldLen+1)
	if err := New().WriteBytes(1, big); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("oversize WriteBytes: got %v", err)
	}
	if _, err := New().BeginField(1, MaxFieldLen+1); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("oversize BeginField: got %v", err)
	}
	if _, err := New().BeginField(1, -1); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("negative BeginField: got %v", err)
	}

	// A field exactly at the limit is accepted.
	b := New()
	fw, err := b.BeginField(1, MaxFieldLen)
	if err != nil {
		t.Fatal(err)
	}
	if fw.Tag() != 1 {
		t.Fatalf("Tag() = %d, want 1", fw.Tag())
	}
	if err := fw.Close(); err == nil {
		t.Fatal("empty close of declared field must fail")
	}

	// A zero-length field closes immediately; a second close is an error.
	zb := New()
	zero, err := zb.BeginField(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zero.Close(); err == nil {
		t.Fatal("double field close must fail")
	}
}

func TestVarintRejections(t *testing.T) {
	bad := [][]byte{
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, // never terminates within 10
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}, // 10th byte overflows
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}, // overflow bit set
		{0x80, 0}, // non-minimal zero
	}
	for i, p := range bad {
		if _, _, err := readUvarint(p); err == nil {
			t.Fatalf("bad varint %d accepted", i)
		}
	}
	// The maximal valid 10-byte encoding is accepted.
	maxEnc := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, n, err := readUvarint(maxEnc)
	if err != nil || v != ^uint64(0) || n != 10 {
		t.Fatalf("max varint: v=%d n=%d err=%v", v, n, err)
	}
}

func TestOpenFieldBlocksMutations(t *testing.T) {
	b := New()
	fw, err := b.BeginField(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	ops := []func() error{
		func() error { return b.WriteBytes(2, []byte("x")) },
		func() error { return b.WriteUint(2, 1) },
		func() error { return b.WriteInt(2, 1) },
		func() error { return b.BeginPhase(1) },
		func() error { return b.WriteRawFrame(appendFrame(nil, 0, 2, typeBytes, nil)) },
	}
	for i, op := range ops {
		if err := op(); !errors.Is(err, ErrOpenField) {
			t.Fatalf("op %d while field open: got %v", i, err)
		}
	}
	if _, err := fw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	// A writer detached from its builder must reject further writes.
	if _, err := fw.Write([]byte("y")); err == nil {
		t.Fatal("write to closed field must fail")
	}
}

func TestStateGatesOnAllMutators(t *testing.T) {
	// Cancelled builders reject every mutator.
	cb := New()
	cb.Cancel()
	cancelOps := []func() error{
		func() error { return cb.WriteBytes(1, nil) },
		func() error { return cb.WriteUint(1, 1) },
		func() error { return cb.WriteInt(1, 1) },
		func() error { return cb.BeginPhase(1) },
		func() error { return cb.WriteRawFrame(appendFrame(nil, 0, 1, typeBytes, nil)) },
		func() error { _, e := cb.BeginField(1, 0); return e },
	}
	for i, op := range cancelOps {
		if err := op(); !errors.Is(err, ErrCancelled) {
			t.Fatalf("cancelled op %d: got %v", i, err)
		}
	}

	// Closed builders reject every mutator and expose no open field.
	db := New()
	if _, err := db.Close(); err != nil {
		t.Fatal(err)
	}
	closedOps := []func() error{
		func() error { return db.WriteBytes(1, nil) },
		func() error { return db.WriteUint(1, 1) },
		func() error { return db.WriteInt(1, 1) },
		func() error { return db.BeginPhase(1) },
		func() error { return db.WriteRawFrame(appendFrame(nil, 0, 1, typeBytes, nil)) },
		func() error { _, e := db.BeginField(1, 0); return e },
	}
	for i, op := range closedOps {
		if err := op(); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed op %d: got %v", i, err)
		}
	}
	if _, err := db.Digest(); err != nil {
		t.Fatalf("digest on closed builder: %v", err)
	}

	// Open field also gates BeginField and the chunked writer close path.
	ob := New()
	ofw, err := ob.BeginField(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ob.BeginField(2, 1); !errors.Is(err, ErrOpenField) {
		t.Fatalf("nested BeginField: got %v", err)
	}
	if _, err := ob.Close(); !errors.Is(err, ErrOpenField) {
		t.Fatalf("Close with open field: got %v", err)
	}
	ob.Cancel()
	if err := ofw.Close(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("field close after cancel: got %v", err)
	}
}

func TestParseFrameTruncatedPayload(t *testing.T) {
	// Header promises 5 payload bytes but provides only 2.
	truncated := appendFrame(nil, 0, 1, typeBytes, []byte("abcde"))[:6]
	if _, err := parseFrame(truncated); err == nil {
		t.Fatal("truncated payload accepted")
	}
	// Length over the configured field limit.
	huge := appendFrame(nil, 0, 1, typeBytes, nil)
	huge[3] = 0xff // overwrite length varint with a large value
	if _, err := parseFrame(huge); err == nil {
		t.Fatal("oversize length accepted")
	}
}
