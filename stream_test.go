package varint

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// TestStreamRoundTrip writes a deterministic stream of values covering
// every encoding length, then reads them back checking absolute frame
// offsets at every step.
func TestStreamRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var uints []uint64
	uints = append(uints, 0, 1, 127, 128, 1<<63-1, 1<<63, maxUint64)
	for i := 0; i < 300; i++ {
		uints = append(uints, rng.Uint64()%uint64(1<<uint(1+rng.Intn(63))))
	}
	var ints []int64
	ints = append(ints, 0, -1, 1, minInt64, maxInt64)
	for i := 0; i < 300; i++ {
		ints = append(ints, int64(rng.Uint64()))
	}

	var buf bytes.Buffer
	e := NewEncoder(&buf)
	var wantOff int64
	for _, v := range uints {
		if e.Offset() != wantOff {
			t.Fatalf("encoder offset = %d, want %d", e.Offset(), wantOff)
		}
		if err := e.WriteUint64(v); err != nil {
			t.Fatalf("WriteUint64(%d): %v", v, err)
		}
		wantOff += int64(EncodedLen(v))
	}
	for _, x := range ints {
		if err := e.WriteInt64(x); err != nil {
			t.Fatalf("WriteInt64(%d): %v", x, err)
		}
		wantOff += int64(EncodedLen(ZigZagEncode(x)))
	}
	if e.Offset() != wantOff || int64(buf.Len()) != wantOff {
		t.Fatalf("encoder offset = %d, buffer = %d, want %d", e.Offset(), buf.Len(), wantOff)
	}

	d := NewDecoder(&buf)
	wantOff = 0
	for i, want := range uints {
		if d.Offset() != wantOff {
			t.Fatalf("uint %d: decoder offset = %d, want %d", i, d.Offset(), wantOff)
		}
		got, err := d.ReadUint64()
		if err != nil || got != want {
			t.Fatalf("uint %d: ReadUint64 = (%d, %v), want (%d, nil)", i, got, err, want)
		}
		wantOff += int64(EncodedLen(want))
	}
	for i, want := range ints {
		got, err := d.ReadInt64()
		if err != nil || got != want {
			t.Fatalf("int %d: ReadInt64 = (%d, %v), want (%d, nil)", i, got, err, want)
		}
		wantOff += int64(EncodedLen(ZigZagEncode(want)))
	}
	if _, err := d.ReadUint64(); err != io.EOF {
		t.Fatalf("final read error = %v, want io.EOF", err)
	}
	if d.Offset() != wantOff {
		t.Fatalf("final decoder offset = %d, want %d", d.Offset(), wantOff)
	}
}

// TestStreamResumeAfterError corrupts a stream with each failure kind
// and verifies the decoder reports the absolute consumed range and
// resumes the next value at the explicit frame boundary.
func TestStreamResumeAfterError(t *testing.T) {
	cases := []struct {
		name     string
		bad      []byte
		kind     Kind
		consumed int
	}{
		{"non-minimal", []byte{0x80, 0x00}, ErrNonCanonical, 2},
		{"overlong", bytes.Repeat([]byte{0x80}, 10), ErrOverlong, 10},
		{"overflow", append(bytes.Repeat([]byte{0xff}, 9), 0x02), ErrOverflow, 10},
		{"truncated", []byte{0xff, 0xff}, ErrTruncated, 2},
	}
	for _, c := range cases {
		var buf []byte
		buf = AppendUint64(buf, 42)
		badOff := int64(len(buf))
		buf = append(buf, c.bad...)
		if c.kind != ErrTruncated {
			buf = AppendInt64(buf, -7)
		}

		d := NewDecoder(bytes.NewReader(buf))
		v, err := d.ReadUint64()
		if err != nil || v != 42 {
			t.Fatalf("%s: first value = (%d, %v), want (42, nil)", c.name, v, err)
		}
		_, err = d.ReadUint64()
		mustError(t, err, c.kind, badOff, c.consumed)
		if d.Offset() != badOff+int64(c.consumed) {
			t.Fatalf("%s: offset after error = %d, want %d", c.name, d.Offset(), badOff+int64(c.consumed))
		}
		if c.kind == ErrTruncated {
			continue
		}
		x, err := d.ReadInt64()
		if err != nil || x != -7 {
			t.Fatalf("%s: resumed value = (%d, %v), want (-7, nil)", c.name, x, err)
		}
		if _, err := d.ReadUint64(); err != io.EOF {
			t.Fatalf("%s: final read error = %v, want io.EOF", c.name, err)
		}
	}
}

// TestStreamTruncatedAtBoundary checks that a stream cut exactly at a
// frame boundary yields io.EOF, while one cut mid-value yields
// ErrTruncated with the partial byte count.
func TestStreamTruncatedAtBoundary(t *testing.T) {
	full := AppendUint64(AppendUint64(nil, 1), 0x1f2f3f4f)
	d := NewDecoder(bytes.NewReader(full))
	if _, err := d.ReadUint64(); err != nil {
		t.Fatalf("first value: %v", err)
	}
	if _, err := d.ReadUint64(); err != nil {
		t.Fatalf("second value: %v", err)
	}
	if _, err := d.ReadUint64(); err != io.EOF {
		t.Fatalf("read past end = %v, want io.EOF", err)
	}

	cut := full[:len(full)-1]
	d = NewDecoder(bytes.NewReader(cut))
	if _, err := d.ReadUint64(); err != nil {
		t.Fatalf("first value: %v", err)
	}
	_, err := d.ReadUint64()
	mustError(t, err, ErrTruncated, 1, len(cut)-1)
}
