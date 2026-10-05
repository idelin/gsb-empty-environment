package varint

import (
	"bytes"
	"math/rand"
	"testing"
)

var testWidths = []uint{1, 2, 7, 8, 9, 16, 31, 32, 33, 63, 64}

func umax(w uint) uint64 {
	if w == 64 {
		return ^uint64(0)
	}
	return (uint64(1) << w) - 1
}

func smin(w uint) int64 { return -smax(w) - 1 }
func smax(w uint) int64 {
	if w == 64 {
		return int64(^uint64(0) >> 1)
	}
	return (int64(1) << (w - 1)) - 1
}

func TestUvarintBoundaries(t *testing.T) {
	for _, w := range testWidths {
		vals := []uint64{0, 1, umax(w)}
		if w > 1 {
			vals = append(vals, uint64(1)<<(w-1), umax(w)-1)
		}
		for _, v := range vals {
			buf, err := AppendUvarint(nil, v, w)
			if err != nil {
				t.Fatalf("w=%d v=%d encode: %v", w, v, err)
			}
			got, n, err := decodeUvarint(buf, w)
			if err != nil {
				t.Fatalf("w=%d v=%d decode: %v", w, v, err)
			}
			if got != v || n != len(buf) {
				t.Fatalf("w=%d v=%d: got %d consumed %d/%d", w, v, got, n, len(buf))
			}
			// Re-encoding must reproduce the unique canonical bytes.
			re, _ := AppendUvarint(nil, got, w)
			if !bytes.Equal(re, buf) {
				t.Fatalf("w=%d v=%d: re-encode %v != %v", w, v, re, buf)
			}
		}
	}
}

func TestSvarintBoundaries(t *testing.T) {
	for _, w := range testWidths {
		vals := []int64{smin(w), smax(w), -1, 0, 1, smin(w) + 1, smax(w) - 1}
		for _, v := range vals {
			if v < smin(w) || v > smax(w) {
				continue
			}
			buf, err := AppendSvarint(nil, v, w)
			if err != nil {
				t.Fatalf("w=%d v=%d encode: %v", w, v, err)
			}
			got, n, err := decodeSvarint(buf, w)
			if err != nil {
				t.Fatalf("w=%d v=%d decode: %v", w, v, err)
			}
			if got != v || n != len(buf) {
				t.Fatalf("w=%d v=%d: got %d consumed %d/%d", w, v, got, n, len(buf))
			}
			re, _ := AppendSvarint(nil, got, w)
			if !bytes.Equal(re, buf) {
				t.Fatalf("w=%d v=%d: re-encode %v != %v", w, v, re, buf)
			}
		}
	}
}

func TestEncodeOverflow(t *testing.T) {
	if _, err := AppendUvarint(nil, 256, 8); err != ErrOverflow {
		t.Fatalf("u8 256: %v", err)
	}
	if _, err := AppendUvarint(nil, 2, 1); err != ErrOverflow {
		t.Fatalf("u1 2: %v", err)
	}
	if _, err := AppendSvarint(nil, 128, 8); err != ErrOverflow {
		t.Fatalf("s8 128: %v", err)
	}
	if _, err := AppendSvarint(nil, -129, 8); err != ErrOverflow {
		t.Fatalf("s8 -129: %v", err)
	}
	// Width 64 accepts everything.
	if _, err := AppendUvarint(nil, ^uint64(0), 64); err != nil {
		t.Fatalf("u64 max: %v", err)
	}
	if _, err := AppendSvarint(nil, smin(64), 64); err != nil {
		t.Fatalf("s64 min: %v", err)
	}
}

func TestDecodeOverflow(t *testing.T) {
	cases := []struct {
		buf   []byte
		width uint
	}{
		{[]byte{0x10}, 4},             // bit above width 4
		{[]byte{0x80, 0x02}, 8},       // 256 in 8 bits
		{[]byte{0x80, 0x80, 0x01}, 8}, // continuation past last byte
		{[]byte{0xff, 0x04}, 9},       // bit above width 9
		{[]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}, 64}, // bit 64
		{[]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, 64}, // 11th group
	}
	for _, c := range cases {
		if _, _, err := decodeUvarint(c.buf, c.width); err != ErrOverflow {
			t.Fatalf("buf=%v w=%d: got %v, want ErrOverflow", c.buf, c.width, err)
		}
	}
}

func TestNonCanonical(t *testing.T) {
	cases := []struct {
		buf   []byte
		width uint
	}{
		{[]byte{0x80, 0x00}, 8},        // 1 encoded in 2 groups
		{[]byte{0x80, 0x80, 0x00}, 16}, // trailing zero group
		{[]byte{0xff, 0x00}, 8},        // 127 with redundant group
		{[]byte{0x80, 0x80, 0x80, 0x00}, 32},
	}
	for _, c := range cases {
		if _, _, err := decodeUvarint(c.buf, c.width); err != ErrNonCanonical {
			t.Fatalf("buf=%v w=%d: got %v, want ErrNonCanonical", c.buf, c.width, err)
		}
	}
	// Single zero byte is the canonical encoding of 0.
	v, n, err := decodeUvarint([]byte{0x00}, 8)
	if err != nil || v != 0 || n != 1 {
		t.Fatalf("canonical zero: v=%d n=%d err=%v", v, n, err)
	}
	// 0x80 0x01 is the canonical encoding of 128 in 8+ bits.
	v, _, err = decodeUvarint([]byte{0x80, 0x01}, 8)
	if err != nil || v != 128 {
		t.Fatalf("canonical 128: v=%d err=%v", v, err)
	}
}

func TestCanonicalVectors(t *testing.T) {
	u := func(v uint64, w uint, want ...byte) {
		t.Helper()
		got, err := AppendUvarint(nil, v, w)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("u%d %d: got %v err=%v want %v", w, v, got, err, want)
		}
	}
	s := func(v int64, w uint, want ...byte) {
		t.Helper()
		got, err := AppendSvarint(nil, v, w)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("s%d %d: got %v err=%v want %v", w, v, got, err, want)
		}
	}
	u(0, 8, 0x00)
	u(1, 8, 0x01)
	u(127, 8, 0x7f)
	u(128, 8, 0x80, 0x01)
	u(255, 8, 0xff, 0x01)
	s(-1, 8, 0x01)
	s(1, 8, 0x02)
	s(-2, 8, 0x03)
	s(63, 8, 0x7e)
	s(-64, 8, 0x7f)
	s(64, 8, 0x80, 0x01)
	s(-128, 8, 0xff, 0x01)
}

func TestTruncationByteByByte(t *testing.T) {
	s := Schema{Version: 1, Fields: []Field{
		{Name: "a", Width: 3},
		{Name: "b", Width: 12},
		{Name: "c", Width: 64, Signed: true},
		{Name: "d", Width: 7},
		{Name: "e", Width: 16, Signed: true},
	}}
	vals := []Value{
		{U: 5},
		{U: 3000},
		{S: smin(64)},
		{U: 127},
		{S: -12345},
	}
	stream, err := AppendRecord(nil, s, vals...)
	if err != nil {
		t.Fatal(err)
	}
	// Every proper prefix must yield ErrNeedMore, never a zero record.
	for i := 0; i < len(stream); i++ {
		if _, _, err := decodeRecord(stream[:i], s); err != ErrNeedMore {
			t.Fatalf("prefix %d/%d: got %v, want ErrNeedMore", i, len(stream), err)
		}
	}
	// Feed byte by byte; only the final byte completes the record.
	d, err := NewDecoder(s)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range stream {
		d.Feed([]byte{b})
		rec, err := d.DecodeOne()
		if i < len(stream)-1 {
			if err != ErrNeedMore {
				t.Fatalf("after byte %d: got %v, want ErrNeedMore", i, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("final byte: %v", err)
		}
		for j := range vals {
			if rec[j] != vals[j] {
				t.Fatalf("field %d: got %+v want %+v", j, rec[j], vals[j])
			}
		}
	}
	if d.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", d.Pending())
	}
}

func TestReencodeEqualsCanonical(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 2000; iter++ {
		nf := 1 + rng.Intn(5)
		s := Schema{Version: 1}
		var vals []Value
		for i := 0; i < nf; i++ {
			w := uint(1 + rng.Intn(64))
			f := Field{Name: "f", Width: w, Signed: rng.Intn(2) == 0}
			s.Fields = append(s.Fields, f)
			var v Value
			if f.Signed {
				if w == 64 {
					v.S = int64(rng.Uint64())
				} else {
					r := uint64(smax(w)) - uint64(smin(w))
					v.S = smin(w) + int64(rng.Uint64()%(r+1))
				}
			} else {
				if w == 64 {
					v.U = rng.Uint64()
				} else {
					v.U = rng.Uint64() & umax(w)
				}
			}
			vals = append(vals, v)
		}
		buf, err := AppendRecord(nil, s, vals...)
		if err != nil {
			t.Fatal(err)
		}
		rec, n, err := decodeRecord(buf, s)
		if err != nil || n != len(buf) {
			t.Fatalf("iter %d: n=%d/%d err=%v", iter, n, len(buf), err)
		}
		re, err := AppendRecord(nil, s, rec...)
		if err != nil {
			t.Fatalf("iter %d re-encode: %v", iter, err)
		}
		if !bytes.Equal(re, buf) {
			t.Fatalf("iter %d: re-encode %x != canonical %x", iter, re, buf)
		}
	}
}

func TestBatchRollback(t *testing.T) {
	s := Schema{Version: 1, Fields: []Field{{Name: "x", Width: 8}}}
	d, _ := NewDecoder(s)
	// Records 1, 2, then a non-canonical encoding, then 3.
	d.Feed([]byte{0x01, 0x02, 0x80, 0x00, 0x03})
	recs, err := d.DecodeBatch(10)
	if err != ErrNonCanonical {
		t.Fatalf("err=%v, want ErrNonCanonical", err)
	}
	if len(recs) != 2 || recs[0][0].U != 1 || recs[1][0].U != 2 {
		t.Fatalf("recs=%v", recs)
	}
	// The failed record must not be committed: its bytes (and the
	// rest of the stream) remain buffered.
	if d.Pending() != 3 {
		t.Fatalf("pending=%d, want 3", d.Pending())
	}
	// A failed DecodeOne consumes nothing either.
	if _, err := d.DecodeOne(); err != ErrNonCanonical {
		t.Fatalf("DecodeOne err=%v", err)
	}
	if d.Pending() != 3 {
		t.Fatalf("pending after failed DecodeOne=%d, want 3", d.Pending())
	}
}

func TestBatchStopsAtNeedMore(t *testing.T) {
	s := Schema{Version: 1, Fields: []Field{{Name: "x", Width: 8}}}
	d, _ := NewDecoder(s)
	d.Feed([]byte{0x05, 0x80}) // record 5, then half a varint
	recs, err := d.DecodeBatch(10)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(recs) != 1 || recs[0][0].U != 5 {
		t.Fatalf("recs=%v", recs)
	}
	if d.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", d.Pending())
	}
	// Empty stream: ErrNeedMore, not a zero record.
	d2, _ := NewDecoder(s)
	if _, err := d2.DecodeOne(); err != ErrNeedMore {
		t.Fatalf("empty DecodeOne=%v, want ErrNeedMore", err)
	}
}

func TestPauseResumeSchemaVersion(t *testing.T) {
	v1 := Schema{Version: 1, Fields: []Field{
		{Name: "a", Width: 8},
		{Name: "b", Width: 16, Signed: true},
	}}
	v2 := v1
	v2.Version = 2

	r1, _ := AppendRecord(nil, v1, Value{U: 7}, Value{S: -300})
	r2, _ := AppendRecord(nil, v1, Value{U: 9}, Value{S: 400})
	stream := append(r1, r2...)

	d, _ := NewDecoder(v1)
	// Feed record 1 and half of record 2.
	d.Feed(stream[:len(r1)+1])
	if _, err := d.DecodeOne(); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()

	// Resume with the same version: continues exactly where paused.
	d2, err := Resume(v1, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d2.DecodeOne(); err != ErrNeedMore {
		t.Fatalf("half record: %v, want ErrNeedMore", err)
	}
	d2.Feed(stream[len(r1)+1:])
	rec, err := d2.DecodeOne()
	if err != nil {
		t.Fatal(err)
	}
	if rec[0].U != 9 || rec[1].S != 400 {
		t.Fatalf("rec=%v", rec)
	}

	// Resume with a different schema version is rejected.
	if _, err := Resume(v2, st); err != ErrSchemaVersion {
		t.Fatalf("version mismatch: %v, want ErrSchemaVersion", err)
	}
}
