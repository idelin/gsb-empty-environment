package bridge

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestFrameDecoderArbitraryChunks(t *testing.T) {
	payload := []byte{9, 8, 7, 6, 5}
	frame := EncodeFrame(payload)
	d := NewFrameDecoder(64)
	var got [][]byte
	for i := 0; i < len(frame); i++ { // one byte at a time
		frames, err := d.Feed(frame[i : i+1])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, frames...)
	}
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("got %d frames %x, want 1 frame %x", len(got), got, payload)
	}
}

func TestFrameDecoderMultipleFramesOneChunk(t *testing.T) {
	f1 := EncodeFrame([]byte{1})
	f2 := EncodeFrame([]byte{2, 3})
	d := NewFrameDecoder(64)
	frames, err := d.Feed(append(f1, f2...))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
}

func TestNeedMoreVsBadData(t *testing.T) {
	d := NewFrameDecoder(64)
	frame := EncodeFrame([]byte{1, 2, 3})
	frames, err := d.Feed(frame[:2])
	if err != nil || len(frames) != 0 {
		t.Fatalf("partial header: frames=%v err=%v, want need-more-data", frames, err)
	}
	frames, err = d.Feed(frame[2:])
	if err != nil || len(frames) != 1 {
		t.Fatalf("completed frame: frames=%v err=%v", frames, err)
	}

	d2 := NewFrameDecoder(4)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1000)
	if _, err := d2.Feed(hdr[:]); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversized frame: err=%v, want ErrBadData", err)
	}
}

func TestFrameQuotaCheckedBeforeAllocation(t *testing.T) {
	d := NewFrameDecoder(16)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<30) // 1 GiB declared
	if _, err := d.Feed(hdr[:]); !errors.Is(err, ErrBadData) {
		t.Fatalf("err = %v, want ErrBadData", err)
	}
	if len(d.buf) != 0 || d.frameLen != -1 {
		t.Fatalf("decoder buffered or committed after quota violation: buf=%d frameLen=%d", len(d.buf), d.frameLen)
	}

	d2 := NewFrameDecoder(8)
	frames, err := d2.Feed(EncodeFrame(make([]byte, 8))) // exactly at quota
	if err != nil || len(frames) != 1 {
		t.Fatalf("at-quota frame: frames=%d err=%v", len(frames), err)
	}
}

func TestFrameStartCallback(t *testing.T) {
	d := NewFrameDecoder(64)
	starts := 0
	d.OnFrameStart = func() { starts++ }
	frame := EncodeFrame([]byte{1, 2})
	if _, err := d.Feed(append(frame, frame...)); err != nil {
		t.Fatal(err)
	}
	if starts != 2 {
		t.Fatalf("OnFrameStart fired %d times, want 2", starts)
	}
}
