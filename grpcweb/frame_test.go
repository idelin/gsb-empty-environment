package grpcweb

import (
	"errors"
	"testing"
)

func makeFrame(flag byte, payload []byte) []byte {
	frame := make([]byte, FrameHeaderSize+len(payload))
	PutFrameHeader(frame, flag, len(payload))
	copy(frame[FrameHeaderSize:], payload)
	return frame
}

func TestSplitFrames_HappyPath(t *testing.T) {
	body := append(
		makeFrame(FlagData, []byte("hello")),
		makeFrame(FlagData, []byte("world"))...,
	)
	body = append(body, makeFrame(FlagTrailer, []byte("grpc-status: 0\r\n"))...)

	frames, err := SplitFrames(body)
	if err != nil {
		t.Fatalf("SplitFrames error = %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	if frames[0].Flag != FlagData || string(frames[0].Payload) != "hello" {
		t.Fatalf("first frame mismatch: %+v", frames[0])
	}
	if frames[2].Flag != FlagTrailer {
		t.Fatalf("last frame flag = 0x%x, want FlagTrailer", frames[2].Flag)
	}
}

func TestSplitFrames_Truncated(t *testing.T) {
	body := makeFrame(FlagData, []byte("full"))

	if _, err := SplitFrames(body[:FrameHeaderSize]); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("short header error = %v, want ErrTruncatedFrame", err)
	}
	if _, err := SplitFrames(body[:len(body)-1]); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("short payload error = %v, want ErrTruncatedFrame", err)
	}
}

func TestSplitFrames_UnknownFlag(t *testing.T) {
	body := makeFrame(0x01, []byte("x"))
	if _, err := SplitFrames(body); !errors.Is(err, ErrUnknownFrame) {
		t.Fatalf("error = %v, want ErrUnknownFrame", err)
	}
}

func TestSplitFrames_TrailerOrdering(t *testing.T) {
	trailer := makeFrame(FlagTrailer, []byte("grpc-status: 0\r\n"))
	data := makeFrame(FlagData, []byte("x"))

	// Trailer followed by another frame.
	body := append(append([]byte{}, trailer...), data...)
	if _, err := SplitFrames(body); !errors.Is(err, ErrTrailerOrder) {
		t.Fatalf("trailer-first error = %v, want ErrTrailerOrder", err)
	}

	// Two trailer frames.
	two := append(append([]byte{}, trailer...), trailer...)
	if _, err := SplitFrames(two); !errors.Is(err, ErrTrailerOrder) {
		t.Fatalf("double-trailer error = %v, want ErrTrailerOrder", err)
	}
}

func TestMarshalDataFrame(t *testing.T) {
	frame := MarshalDataFrame([]byte{0xde, 0xad})
	if frame[0] != FlagData {
		t.Fatalf("flag = 0x%x, want FlagData", frame[0])
	}
	if got := len(frame) - FrameHeaderSize; got != 2 {
		t.Fatalf("payload length = %d, want 2", got)
	}
}
