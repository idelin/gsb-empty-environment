package sframe

import (
	"bytes"
	"errors"
	"testing"
)

func testPayload() []byte {
	return MarshalObject([]*Field{
		{Name: "user", Type: TypeObject, Kids: []*Field{
			{Name: "name", Type: TypeString, Raw: []byte("alice")},
			{Name: "credentials", Type: TypeObject, Kids: []*Field{
				{Name: "password", Type: TypeString, Raw: []byte("s3cret-PA55word")},
				{Name: "token", Type: TypeString, Raw: []byte("tok-live-9f8e7d6c")},
			}},
		}},
		{Name: "x-ext-9f", Type: TypeBytes, Raw: []byte("ext-payload-bytes")},
		{Name: "attempt", Type: TypeInt64, Int: 3},
		{Name: "remember", Type: TypeBool, Bool: true},
	})
}

func testFrame(seq uint64) []byte {
	return EncodeFrame(Header{Version: Version1, Seq: seq, Timestamp: 1_758_000_000_000_000_000 + int64(seq)}, testPayload())
}

func TestEncodeSplitRoundTrip(t *testing.T) {
	frame := testFrame(42)
	hdr, payload, err := SplitFrame(frame)
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	if hdr.Seq != 42 || hdr.Timestamp != 1_758_000_000_000_000_042 {
		t.Fatalf("header not preserved: %+v", hdr)
	}
	if !bytes.Equal(payload, testPayload()) {
		t.Fatal("payload mismatch")
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	stream := append(testFrame(1), testFrame(2)...)
	for _, size := range []int{1, 3, 7, 64, len(stream)} {
		p := NewParser(1 << 20)
		var got [][]byte
		for off := 0; off < len(stream); off += size {
			end := off + size
			if end > len(stream) {
				end = len(stream)
			}
			frames, err := p.Feed(stream[off:end])
			if err != nil {
				t.Fatalf("chunk=%d: %v", size, err)
			}
			got = append(got, frames...)
		}
		if len(got) != 2 {
			t.Fatalf("chunk=%d: got %d frames, want 2", size, len(got))
		}
		if !bytes.Equal(got[0], testFrame(1)) || !bytes.Equal(got[1], testFrame(2)) {
			t.Fatalf("chunk=%d: frame bytes differ", size)
		}
	}
}

func TestNeedMoreVsCorrupt(t *testing.T) {
	frame := testFrame(1)

	// Truncated header and truncated payload: need more data, not corruption.
	for _, n := range []int{0, 5, HeaderSize - 1, HeaderSize + 3, len(frame) - 1} {
		if _, _, err := DecodeFrame(frame[:n], 1<<20); !NeedMore(err) {
			t.Fatalf("len=%d: want need-more, got %v", n, err)
		}
	}

	// Bad magic, bad version, bad CRC: corruption.
	bad := bytes.Clone(frame)
	bad[0] = 'X'
	if _, _, err := DecodeFrame(bad, 1<<20); NeedMore(err) {
		t.Fatal("bad magic reported as need-more")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("bad magic: want CorruptError, got %T", err)
		}
	}

	bad = bytes.Clone(frame)
	bad[2] = 99
	if _, _, err := DecodeFrame(bad, 1<<20); NeedMore(err) {
		t.Fatal("bad version reported as need-more")
	}

	bad = bytes.Clone(frame)
	bad[len(bad)-1] ^= 0xff
	if _, _, err := DecodeFrame(bad, 1<<20); NeedMore(err) {
		t.Fatal("bad crc reported as need-more")
	}
}

func TestLengthQuotaCheckedBeforeWaiting(t *testing.T) {
	frame := testFrame(1)
	// Announce a payload larger than the quota but deliver only the header:
	// this must be corruption immediately, not "need more data".
	bad := bytes.Clone(frame)
	bad[20], bad[21], bad[22], bad[23] = 0xff, 0xff, 0xff, 0xff
	_, _, err := DecodeFrame(bad[:HeaderSize], 1<<20)
	if NeedMore(err) {
		t.Fatal("over-quota length reported as need-more")
	}
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want CorruptError, got %T: %v", err, err)
	}
}

func TestParserCorruptMidStream(t *testing.T) {
	stream := append(testFrame(1), testFrame(2)...)
	stream[len(stream)-2] ^= 0xff // break second frame's CRC
	p := NewParser(1 << 20)
	var got [][]byte
	var gotErr error
	for off := 0; off < len(stream); off += 5 {
		end := off + 5
		if end > len(stream) {
			end = len(stream)
		}
		frames, err := p.Feed(stream[off:end])
		if err != nil {
			gotErr = err
			break
		}
		got = append(got, frames...)
	}
	if gotErr == nil {
		t.Fatal("expected corruption error")
	}
	var ce *CorruptError
	if !errors.As(gotErr, &ce) {
		t.Fatalf("want CorruptError, got %T", gotErr)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 good frame before corruption, got %d", len(got))
	}
}
