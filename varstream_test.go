package varstream

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

// hasErr reports whether err carries the sentinel, directly or via the
// field-context wrapper (Go 1.11 has no errors.Is).
func hasErr(err, sentinel error) bool {
	return err == sentinel || (err != nil && strings.Contains(err.Error(), sentinel.Error()))
}

func boundarySchema() *Schema {
	return &Schema{Version: 1, Fields: []Field{
		{Name: "u1", Bits: 1},
		{Name: "s7", Bits: 7, Signed: true},
		{Name: "u8", Bits: 8},
		{Name: "s9", Bits: 9, Signed: true},
		{Name: "u63", Bits: 63},
		{Name: "s64", Bits: 64, Signed: true},
	}}
}

func boundaryValues() [][]Value {
	maxU1 := uint64(1)
	maxS7 := int64(63)
	maxU8 := uint64(255)
	maxS9 := int64(255)
	maxU63 := uint64(1)<<63 - 1
	return [][]Value{
		{Uint(0), Int(0), Uint(0), Int(0), Uint(0), Int(0)},
		{Uint(1), Int(1), Uint(1), Int(1), Uint(1), Int(1)},
		{Uint(maxU1), Int(maxS7), Uint(maxU8), Int(maxS9), Uint(maxU63), Int(1<<63 - 1)},
		{Uint(maxU1), Int(-maxS7 - 1), Uint(maxU8), Int(-maxS9 - 1), Uint(maxU63), Int(-1 << 63)},
		{Uint(0), Int(-1), Uint(127), Int(-1), Uint(1<<62 - 1), Int(-1)},
		{Uint(1), Int(maxS7 - 1), Uint(254), Int(maxS9 - 1), Uint(maxU63 - 1), Int(1<<63 - 2)},
	}
}

func TestBoundaryRoundTrip(t *testing.T) {
	s := boundarySchema()
	for _, vals := range boundaryValues() {
		buf, err := s.Encode(vals...)
		if err != nil {
			t.Fatalf("encode %v: %v", vals, err)
		}
		rec, n, err := s.DecodeRecord(buf)
		if err != nil {
			t.Fatalf("decode %v: %v", vals, err)
		}
		if n != len(buf) {
			t.Fatalf("consumed %d of %d bytes", n, len(buf))
		}
		for i, f := range s.Fields {
			if f.Signed && rec[i].I != vals[i].I {
				t.Fatalf("field %s: got %d want %d", f.Name, rec[i].I, vals[i].I)
			}
			if !f.Signed && rec[i].U != vals[i].U {
				t.Fatalf("field %s: got %d want %d", f.Name, rec[i].U, vals[i].U)
			}
		}
		// Re-encoding must reproduce the unique canonical byte string.
		buf2, err := s.Encode(rec...)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if !bytes.Equal(buf, buf2) {
			t.Fatalf("canonical mismatch: %x vs %x", buf, buf2)
		}
	}
}

func TestOverflowRejected(t *testing.T) {
	// Encoder side.
	s := &Schema{Version: 1, Fields: []Field{{Name: "u8", Bits: 8}}}
	if _, err := s.Encode(Uint(256)); !hasErr(err, ErrOverflow) {
		t.Fatalf("encode 256 into u8: %v", err)
	}
	ss := &Schema{Version: 1, Fields: []Field{{Name: "s8", Bits: 8, Signed: true}}}
	for _, v := range []int64{128, -129, 1 << 40} {
		if _, err := ss.Encode(Int(v)); !hasErr(err, ErrOverflow) {
			t.Fatalf("encode %d into s8: %v", v, err)
		}
	}
	// Decoder side: crafted in-range-varint but out-of-range-value bytes.
	if _, _, err := s.DecodeRecord([]byte{0x80, 0x02}); !hasErr(err, ErrOverflow) { // 256
		t.Fatalf("decode 256 into u8: %v", err)
	}
	if _, _, err := ss.DecodeRecord([]byte{0x80, 0x02}); !hasErr(err, ErrOverflow) { // zigzag 256 -> 128
		t.Fatalf("decode 128 into s8: %v", err)
	}
	// Exact-boundary values must still pass.
	if _, err := s.Encode(Uint(255)); err != nil {
		t.Fatalf("encode 255 into u8: %v", err)
	}
	if _, err := ss.Encode(Int(127)); err != nil {
		t.Fatalf("encode 127 into s8: %v", err)
	}
	if _, err := ss.Encode(Int(-128)); err != nil {
		t.Fatalf("encode -128 into s8: %v", err)
	}
}

func TestNonMinimalRejected(t *testing.T) {
	s := &Schema{Version: 1, Fields: []Field{{Name: "u64", Bits: 64}}}
	cases := [][]byte{
		{0x80, 0x00},       // 0 with a redundant group
		{0x81, 0x00},       // 1 with a redundant group
		{0xff, 0x80, 0x00}, // 127 padded to three bytes
	}
	for _, c := range cases {
		if _, _, err := s.DecodeRecord(c); !hasErr(err, ErrNonMinimal) {
			t.Fatalf("decode %x: %v", c, err)
		}
	}
	// The minimal forms of the same values must decode fine.
	for _, c := range [][]byte{{0x00}, {0x01}, {0x7f}} {
		if _, _, err := s.DecodeRecord(c); err != nil {
			t.Fatalf("decode minimal %x: %v", c, err)
		}
	}
}

func TestVarintOverflowRejected(t *testing.T) {
	s := &Schema{Version: 1, Fields: []Field{{Name: "u64", Bits: 64}}}
	ten := bytes.Repeat([]byte{0xff}, 9)
	if _, _, err := s.DecodeRecord(append(ten, 0x02)); !hasErr(err, ErrVarintOverflow) {
		t.Fatalf("10th byte payload 2: %v", err)
	}
	eleven := append(bytes.Repeat([]byte{0xff}, 10), 0x01)
	if _, _, err := s.DecodeRecord(eleven); !hasErr(err, ErrVarintOverflow) {
		t.Fatalf("11-byte varint: %v", err)
	}
	// Max uint64 is exactly 10 bytes and must pass.
	max := append(bytes.Repeat([]byte{0xff}, 9), 0x01)
	rec, _, err := s.DecodeRecord(max)
	if err != nil || rec[0].U != ^uint64(0) {
		t.Fatalf("decode max uint64: %v %v", rec, err)
	}
}

func TestTruncationNeedMore(t *testing.T) {
	s := boundarySchema()
	vals := boundaryValues()[2] // all-max record, multi-byte varints
	buf, err := s.Encode(vals...)
	if err != nil {
		t.Fatal(err)
	}
	// Every strict prefix must yield ErrNeedMore, never a zero record.
	for i := 0; i < len(buf); i++ {
		rec, n, err := s.DecodeRecord(buf[:i])
		if !hasErr(err, ErrNeedMore) {
			t.Fatalf("prefix %d: got rec=%v n=%d err=%v", i, rec, n, err)
		}
		if n != 0 || rec != nil {
			t.Fatalf("prefix %d committed n=%d rec=%v", i, n, rec)
		}
	}
	// Byte-by-byte streaming: nothing completes before the final byte.
	d, err := NewDecoder(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(buf)-1; i++ {
		if _, err := d.Feed(buf[i : i+1]); err != nil {
			t.Fatalf("feed byte %d: %v", i, err)
		}
		if got := d.Records(); len(got) != 0 {
			t.Fatalf("record committed early at byte %d", i)
		}
		if !d.Pending() {
			t.Fatalf("decoder not pending after byte %d", i)
		}
	}
	if _, err := d.Feed(buf[len(buf)-1:]); err != nil {
		t.Fatal(err)
	}
	got := d.Records()
	if len(got) != 1 || got[0][5].I != vals[5].I {
		t.Fatalf("streamed record: %v", got)
	}
	if d.Pending() {
		t.Fatal("decoder still pending after full record")
	}
}

func TestPauseResume(t *testing.T) {
	s := boundarySchema()
	var stream []byte
	for _, vals := range boundaryValues() {
		buf, err := s.Encode(vals...)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, buf...)
	}
	// Pause at every byte boundary; resuming must reproduce the stream.
	for cut := 0; cut <= len(stream); cut++ {
		d, _ := NewDecoder(s)
		if _, err := d.Feed(stream[:cut]); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		st := d.Pause()
		r, err := s.Resume(st)
		if err != nil {
			t.Fatalf("resume cut %d: %v", cut, err)
		}
		if _, err := r.Feed(stream[cut:]); err != nil {
			t.Fatalf("feed rest cut %d: %v", cut, err)
		}
		if got := len(d.Records()) + len(r.Records()); got != len(boundaryValues()) {
			t.Fatalf("cut %d: got %d records", cut, got)
		}
	}
	// A state is bound to its schema version.
	d, _ := NewDecoder(s)
	if _, err := d.Feed(stream[:3]); err != nil {
		t.Fatal(err)
	}
	st := d.Pause()
	newer := *s
	newer.Version = s.Version + 1
	if _, err := newer.Resume(st); err != ErrSchemaVersion {
		t.Fatalf("resume with bumped version: %v", err)
	}
}

func TestBatchAtomicity(t *testing.T) {
	s := &Schema{Version: 1, Fields: []Field{
		{Name: "u8", Bits: 8},
		{Name: "s8", Bits: 8, Signed: true},
	}}
	good1, _ := s.Encode(Uint(1), Int(-1))
	good2, _ := s.Encode(Uint(2), Int(-2))
	bad := []byte{0x80, 0x02, 0x00} // u8 = 256 overflows; trailing byte unused

	// Corrupt record in the middle: only the records before it commit.
	stream := append(append(append([]byte{}, good1...), bad...), good2...)
	recs, n, err := s.DecodeAll(stream)
	if !hasErr(err, ErrOverflow) {
		t.Fatalf("batch with bad record: %v", err)
	}
	if len(recs) != 1 || recs[0][0].U != 1 {
		t.Fatalf("committed %v", recs)
	}
	if n != len(good1) {
		t.Fatalf("consumed %d, want %d", n, len(good1))
	}

	// Truncated tail: complete records commit, the half record does not.
	stream = append(append([]byte{}, good1...), good2[:len(good2)-1]...)
	recs, n, err = s.DecodeAll(stream)
	if !hasErr(err, ErrNeedMore) {
		t.Fatalf("batch with truncated tail: %v", err)
	}
	if len(recs) != 1 || n != len(good1) {
		t.Fatalf("committed %v n=%d", recs, n)
	}

	// Streaming: a failing record is discarded, never emitted.
	d, _ := NewDecoder(s)
	if _, err := d.Feed(append(append([]byte{}, good1...), bad...)); !hasErr(err, ErrOverflow) {
		t.Fatalf("feed bad record: %v", err)
	}
	if got := d.Records(); len(got) != 1 || got[0][0].U != 1 {
		t.Fatalf("stream committed %v", got)
	}
}

func TestCanonicalReencodeRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	widths := []uint{1, 2, 3, 7, 8, 13, 16, 31, 32, 33, 63, 64}
	for iter := 0; iter < 2000; iter++ {
		ub := widths[rng.Intn(len(widths))]
		sb := widths[rng.Intn(len(widths))]
		s := &Schema{Version: 7, Fields: []Field{
			{Name: "u", Bits: ub},
			{Name: "s", Bits: sb, Signed: true},
		}}
		var u uint64
		if ub == 64 {
			u = rng.Uint64()
		} else {
			u = rng.Uint64() & ((uint64(1) << ub) - 1)
		}
		var i int64
		if sb == 64 {
			i = int64(rng.Uint64())
		} else {
			lim := int64(1) << (sb - 1)
			i = int64(rng.Uint64()%(uint64(lim)*2)) - lim
		}
		buf, err := s.Encode(Uint(u), Int(i))
		if err != nil {
			t.Fatalf("iter %d encode: %v", iter, err)
		}
		rec, n, err := s.DecodeRecord(buf)
		if err != nil {
			t.Fatalf("iter %d decode: %v", iter, err)
		}
		if n != len(buf) || rec[0].U != u || rec[1].I != i {
			t.Fatalf("iter %d: got %v n=%d want u=%d i=%d", iter, rec, n, u, i)
		}
		// Decoding and re-encoding must yield the identical byte string.
		buf2, err := s.Encode(rec...)
		if err != nil {
			t.Fatalf("iter %d re-encode: %v", iter, err)
		}
		if !bytes.Equal(buf, buf2) {
			t.Fatalf("iter %d: canonical mismatch %x vs %x", iter, buf, buf2)
		}
	}
}
