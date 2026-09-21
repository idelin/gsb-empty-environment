package chunkfetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]byte{nil, {}, []byte("a"), bytes.Repeat([]byte("xy"), 500)}
	for idx, payload := range cases {
		frame := EncodeFrame(payload)
		got, consumed, err := DecodeFrame(frame)
		if err != nil {
			t.Fatalf("case %d: unexpected err %v", idx, err)
		}
		if consumed != len(frame) {
			t.Fatalf("case %d: consumed %d want %d", idx, consumed, len(frame))
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("case %d: payload mismatch", idx)
		}
	}
}

// Feeding the bytes of a frame one at a time must keep reporting
// ErrNeedMore until the final byte turns it into a full frame: arbitrary
// input chunking must not be confused with corruption.
func TestDecodeFrameByteByByte(t *testing.T) {
	frame := EncodeFrame([]byte("hello world"))
	for n := 0; n < len(frame)-1; n++ {
		_, _, err := DecodeFrame(frame[:n])
		if !errors.Is(err, ErrNeedMore) {
			t.Fatalf("n=%d: want ErrNeedMore, got %v", n, err)
		}
	}
	payload, consumed, err := DecodeFrame(frame)
	if err != nil || string(payload) != "hello world" || consumed != len(frame) {
		t.Fatalf("full frame: payload=%q consumed=%d err=%v", payload, consumed, err)
	}
}

func TestDecodeFrameMissingVsBad(t *testing.T) {
	frame := EncodeFrame([]byte("payload-bytes"))

	// Truncated at each position: missing data.
	for n := 0; n < len(frame)-1; n++ {
		if _, _, err := DecodeFrame(frame[:n]); !errors.Is(err, ErrNeedMore) {
			t.Fatalf("truncated at %d: want ErrNeedMore got %v", n, err)
		}
	}

	// Trailing byte after a complete frame: bad data.
	trailing := append(append([]byte(nil), frame...), 0x00)
	if _, _, err := DecodeFrame(trailing); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("trailing bytes: want ErrBadFrame got %v", err)
	}

	// Flipped checksum byte: bad data.
	bad := append([]byte(nil), frame...)
	bad[4] ^= 0xFF
	if _, _, err := DecodeFrame(bad); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("bad checksum: want ErrBadFrame got %v", err)
	}

	// Flipped payload byte: bad data.
	bad2 := append([]byte(nil), frame...)
	bad2[len(bad2)-1] ^= 0x01
	if _, _, err := DecodeFrame(bad2); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("bad payload: want ErrBadFrame got %v", err)
	}
}

func TestReadFrameLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	// Declared length far exceeds the quota. Only the 4-byte header is
	// presented, which is exactly when the quota check must fire, before any
	// payload buffer is allocated.
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], 50_000_000)
	if _, err := ReadFrame(bytes.NewReader(head[:]), 1024); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("oversize declared length: want ErrBadFrame got %v", err)
	}

	// A cut connection after a valid header is missing data, not bad.
	var goodHead [4]byte
	binary.BigEndian.PutUint32(goodHead[:], 10)
	if _, err := ReadFrame(bytes.NewReader(goodHead[:]), 1024); !errors.Is(err, ErrNeedMore) {
		t.Fatalf("cut after header: want ErrNeedMore got %v", err)
	}

	// Clean EOF is io.EOF.
	if _, err := ReadFrame(bytes.NewReader(nil), 1024); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream: want io.EOF got %v", err)
	}
}

func TestReadFrameStreamedInFragments(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 1000)
	frame := EncodeFrame(payload)
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < len(frame); i += 7 {
			end := i + 7
			if end > len(frame) {
				end = len(frame)
			}
			pw.Write(frame[i:end])
		}
		pw.Close()
	}()
	got, err := ReadFrame(pr, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed payload mismatch")
	}
}

func TestManifestValidate(t *testing.T) {
	data := []byte("1234567")
	m := BuildManifest("v", data, 3)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.ChunkCount() != 3 {
		t.Fatalf("chunks=%d want 3", m.ChunkCount())
	}
	bad := *m
	bad.ChunkSHA256 = bad.ChunkSHA256[:2]
	if err := bad.Validate(); err == nil {
		t.Fatal("want digest count error")
	}
	bad2 := *m
	bad2.WholeSHA256 = make([]byte, sha256.Size-1)
	if err := bad2.Validate(); err == nil {
		t.Fatal("want whole digest length error")
	}
}
