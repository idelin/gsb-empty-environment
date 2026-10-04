package paxos

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"sync"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Entry{
		{Term: 0, Node: 0, Index: 0, Cmd: nil},
		{Term: 1, Node: 2, Index: 3, Cmd: []byte{}},
		{Term: 1<<63 + 7, Node: 1<<31 + 5, Index: 1<<62 + 9, Cmd: []byte("set x = 1")},
		{Term: 42, Node: 4, Index: 1000, Cmd: bytes.Repeat([]byte{0xAB}, 1<<20)},
		{Term: 9, Node: 9, Index: 9, Cmd: NoopCmd},
	}
	var buf []byte
	for _, e := range cases {
		buf = AppendRecord(buf, e)
	}
	off := 0
	for i, want := range cases {
		got, n, err := DecodeRecord(buf[off:])
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if n != RecordSize(want) {
			t.Fatalf("case %d: consumed %d, want %d", i, n, RecordSize(want))
		}
		if got.Term != want.Term || got.Node != want.Node || got.Index != want.Index {
			t.Fatalf("case %d: header mismatch: %+v vs %+v", i, got, want)
		}
		if !bytes.Equal(got.Cmd, want.Cmd) {
			t.Fatalf("case %d: cmd mismatch (len %d vs %d)", i, len(got.Cmd), len(want.Cmd))
		}
		off += n
	}
	if off != len(buf) {
		t.Fatalf("leftover bytes: consumed %d of %d", off, len(buf))
	}
}

func TestDecodeZeroCopyAliasesBuffer(t *testing.T) {
	buf := AppendRecord(nil, Entry{Term: 1, Node: 1, Index: 1, Cmd: []byte("hello")})
	e, _, err := DecodeRecord(buf)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the underlying buffer must be visible through the decoded
	// entry, proving Cmd aliases buf instead of copying.
	buf[RecordHdrSize+payloadMin] = 'J'
	if string(e.Cmd) != "Jello" {
		t.Fatalf("expected zero-copy alias, got %q", e.Cmd)
	}
}

func TestDecodeTruncatedAtEveryOffset(t *testing.T) {
	full := AppendRecord(nil, Entry{Term: 7, Node: 3, Index: 11, Cmd: []byte("payload-bytes")})
	for i := 0; i < len(full); i++ {
		if _, _, err := DecodeRecord(full[:i]); err != ErrTooShort {
			t.Fatalf("prefix %d: got %v, want ErrTooShort", i, err)
		}
	}
	if _, _, err := DecodeRecord(full); err != nil {
		t.Fatalf("full buffer: %v", err)
	}
}

func TestDecodeCorruptCRC(t *testing.T) {
	buf := AppendRecord(nil, Entry{Term: 1, Node: 1, Index: 1, Cmd: []byte("data")})
	buf[len(buf)-1] ^= 0xFF // flip a payload bit
	if _, _, err := DecodeRecord(buf); err != ErrCorrupt {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestDecodeLengthOverflow(t *testing.T) {
	// Length field claims more than MaxRecordSize.
	buf := make([]byte, RecordHdrSize)
	binary.LittleEndian.PutUint32(buf[4:8], MaxRecordSize+1)
	if _, _, err := DecodeRecord(buf); err != ErrRecordSizeExceeds {
		t.Fatalf("got %v, want ErrRecordSizeExceeds", err)
	}
	// Length field valid but larger than the remaining buffer (torn write).
	binary.LittleEndian.PutUint32(buf[4:8], 1024)
	if _, _, err := DecodeRecord(buf); err != ErrTooShort {
		t.Fatalf("got %v, want ErrTooShort", err)
	}
	// Length field at max uint32 must not overflow or panic.
	binary.LittleEndian.PutUint32(buf[4:8], 0xFFFFFFFF)
	if _, _, err := DecodeRecord(buf); err != ErrRecordSizeExceeds {
		t.Fatalf("got %v, want ErrRecordSizeExceeds", err)
	}
}

func TestDecodeCmdLenOverflow(t *testing.T) {
	// Craft a record whose cmdLen exceeds the payload: CRC is valid, so the
	// bounds check inside the payload is what protects us.
	payload := make([]byte, payloadMin)
	binary.LittleEndian.PutUint32(payload[20:24], 1<<30)
	buf := make([]byte, RecordHdrSize+len(payload))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[RecordHdrSize:], payload)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(payload))
	if _, _, err := DecodeRecord(buf); err != ErrCorrupt {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestDecodeForwardCompatTrailingBytes(t *testing.T) {
	// Simulate a future writer that appends extra fields to the payload.
	cmd := []byte("cmd")
	payload := make([]byte, payloadMin+len(cmd)+8)
	binary.LittleEndian.PutUint64(payload[0:8], 5)
	binary.LittleEndian.PutUint32(payload[8:12], 2)
	binary.LittleEndian.PutUint64(payload[12:20], 9)
	binary.LittleEndian.PutUint32(payload[20:24], uint32(len(cmd)))
	copy(payload[24:], cmd)
	copy(payload[24+len(cmd):], []byte("future!!")) // unknown tail
	buf := make([]byte, RecordHdrSize+len(payload))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[RecordHdrSize:], payload)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(payload))

	e, n, err := DecodeRecord(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) {
		t.Fatalf("consumed %d, want %d", n, len(buf))
	}
	if e.Term != 5 || e.Node != 2 || e.Index != 9 || string(e.Cmd) != "cmd" {
		t.Fatalf("bad decode: %+v", e)
	}
}

func TestHeaderCompatibility(t *testing.T) {
	if err := CheckHeader(EncodeHeader(nil)); err != nil {
		t.Fatal(err)
	}
	// Older version (0) is accepted: backward compatible read.
	old := EncodeHeader(nil)
	binary.LittleEndian.PutUint32(old[8:12], 0)
	if err := CheckHeader(old); err != nil {
		t.Fatalf("version 0 rejected: %v", err)
	}
	// Newer version is rejected explicitly.
	future := EncodeHeader(nil)
	binary.LittleEndian.PutUint32(future[8:12], FormatVersion+1)
	if err := CheckHeader(future); err == nil {
		t.Fatal("expected unsupported version error")
	}
	// Bad magic.
	bad := EncodeHeader(nil)
	bad[0] = 'X'
	if err := CheckHeader(bad); err != ErrBadMagic {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
	// Short buffer.
	if err := CheckHeader([]byte("MP")); err != ErrTooShort {
		t.Fatalf("got %v, want ErrTooShort", err)
	}
}

func TestScanStopsAtTornTail(t *testing.T) {
	var buf []byte
	for i := uint64(1); i <= 3; i++ {
		buf = AppendRecord(buf, Entry{Term: 1, Node: 0, Index: i, Cmd: []byte{byte(i)}})
	}
	validLen := len(buf)
	// Append half of a fourth record (torn write).
	torn := AppendRecord(nil, Entry{Term: 1, Node: 0, Index: 4, Cmd: []byte("torn")})
	buf = append(buf, torn[:len(torn)/2]...)

	var got []uint64
	valid, err := Scan(buf, func(_ int64, e Entry) error {
		got = append(got, e.Index)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid != validLen {
		t.Fatalf("valid prefix %d, want %d", valid, validLen)
	}
	if len(got) != 3 {
		t.Fatalf("scanned %d entries, want 3", len(got))
	}
}

func TestConcurrentEncodeDecode(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				e := Entry{Term: uint64(i), Node: uint32(g), Index: uint64(i), Cmd: []byte{byte(g), byte(i)}}
				buf := AppendRecord(nil, e)
				d, n, err := DecodeRecord(buf)
				if err != nil || n != len(buf) || d.Index != e.Index || d.Node != e.Node {
					t.Errorf("roundtrip failed: %+v err=%v", d, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
