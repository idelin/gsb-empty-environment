package netsec

import (
	"bytes"
	"io"
	"testing"
)

func dataFrame(streamID uint32, flags uint8, body []byte) []byte {
	frame := make([]byte, frameHeaderLen, frameHeaderLen+len(body))
	frame[0] = byte(len(body) >> 16)
	frame[1] = byte(len(body) >> 8)
	frame[2] = byte(len(body))
	frame[3] = FrameTypeData
	frame[4] = flags
	frame[5] = byte(streamID >> 24)
	frame[6] = byte(streamID >> 16)
	frame[7] = byte(streamID >> 8)
	frame[8] = byte(streamID)
	return append(frame, body...)
}

func TestParseDataFrameUnpadded(t *testing.T) {
	body := []byte("hello")
	got, err := ParseDataFrame(0, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("got %q, want %q", got, body)
	}
}

func TestParseDataFramePaddedDeliversPayloadOnly(t *testing.T) {
	body := append([]byte{3}, []byte("hello")...)
	body = append(body, 0, 0, 0)
	got, err := ParseDataFrame(FlagDataPadded, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestParseDataFramePadEqualsRemainingFails(t *testing.T) {
	// Pad length set to the remaining frame length must fail.
	body := append([]byte{5}, []byte("hello")...)
	_, err := ParseDataFrame(FlagDataPadded, body)
	if !IsConnectionError(err) {
		t.Fatalf("got %v, want connection error", err)
	}
}

func TestParseDataFramePadExceedsRemainingFails(t *testing.T) {
	body := append([]byte{200}, []byte("hello")...)
	_, err := ParseDataFrame(FlagDataPadded, body)
	if !IsConnectionError(err) {
		t.Fatalf("got %v, want connection error", err)
	}
}

func TestParseDataFrameMissingPadLengthFails(t *testing.T) {
	_, err := ParseDataFrame(FlagDataPadded, nil)
	if !IsConnectionError(err) {
		t.Fatalf("got %v, want connection error", err)
	}
}

func TestDecoderPaddedFrameBoundariesAndContentLength(t *testing.T) {
	padded := append([]byte{4}, []byte("hi")...)
	padded = append(padded, 0, 0, 0, 0)
	stream := append(dataFrame(1, FlagDataPadded, padded), dataFrame(1, 0, []byte("yo"))...)

	d := NewDecoder(bytes.NewReader(stream))

	f1, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if string(f1.Payload) != "hi" {
		t.Fatalf("first payload %q, want %q", f1.Payload, "hi")
	}

	f2, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("second frame boundary shifted: %v", err)
	}
	if string(f2.Payload) != "yo" {
		t.Fatalf("second payload %q, want %q", f2.Payload, "yo")
	}

	if got := d.ContentLength(); got != 4 {
		t.Fatalf("content-length %d, want 4 (padding excluded)", got)
	}
	if _, err := d.ReadFrame(); err != io.EOF {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func TestDecoderRejectsPadEqualToRemaining(t *testing.T) {
	// Pad length set to the remaining frame length: connection fails.
	body := append([]byte{3}, []byte("abc")...)
	d := NewDecoder(bytes.NewReader(dataFrame(1, FlagDataPadded, body)))

	_, err := d.ReadFrame()
	if !IsConnectionError(err) {
		t.Fatalf("got %v, want connection error", err)
	}
	if got := d.ContentLength(); got != 0 {
		t.Fatalf("content-length %d, want 0 after rejected frame", got)
	}
}
