package netsec

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestHTTP1PreservesWireOrderAndDuplicates(t *testing.T) {
	block := requestHeaders(
		[2]string{"x-a", "1"},
		[2]string{"x-b", "2"},
		[2]string{"x-a", "3"},
	)
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var raw bytes.Buffer
	if err := conv.WriteHTTP1(&raw); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	wire := raw.String()
	ia := strings.Index(wire, "X-A: 1")
	ib := strings.Index(wire, "X-B: 2")
	ia2 := strings.Index(wire, "X-A: 3")
	if !(ia < ib && ib < ia2) {
		t.Fatalf("header order not preserved:\n%s", wire)
	}
}

func TestFrameSizeLimitIs24BitMax(t *testing.T) {
	// The 24-bit length field cannot physically encode larger values, so the
	// bound is the 2^24-1 ceiling enforced alongside io.ReadFull.
	if MaxFramePayloadSize != 1<<24-1 {
		t.Fatalf("MaxFramePayloadSize = %d, want %d", MaxFramePayloadSize, 1<<24-1)
	}
	// A frame claiming a large payload but truncating the connection yields a
	// read error rather than an under-allocated buffer.
	raw := buildFrame(FrameData, 0, testStream, []byte{0})
	raw[0], raw[1], raw[2] = 0x00, 0x01, 0x00 // claim 256 bytes, supply one
	r := NewFrameReader(bytes.NewReader(raw))
	if _, err := r.ReadFrame(); err == nil {
		t.Fatal("truncated oversized frame must return an error")
	}
}

func TestRSTStreamFrameShape(t *testing.T) {
	raw := buildFrame(FrameRSTStream, 0, testStream, RSTStreamFrame(ErrCodeCancel))
	r := NewFrameReader(bytes.NewReader(raw))
	f, err := r.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	code, err := f.RSTCode()
	if err != nil || code != ErrCodeCancel || f.Stream != testStream {
		t.Fatalf("bad RST_STREAM: code=%#x err=%v stream=%d", code, err, f.Stream)
	}
}

func TestMalformedFramesRejected(t *testing.T) {
	cases := map[string][]byte{
		"settings_on_stream": buildFrame(FrameSettings, 0, 1, nil),
		"bad_settings_len":   buildFrame(FrameSettings, 0, 0, make([]byte, 3)),
		"ping_bad_len":       buildFrame(FramePing, 0, 0, make([]byte, 4)),
		"rst_on_zero":        buildFrame(FrameRSTStream, 0, 0, RSTStreamFrame(ErrCodeCancel)),
		"headers_on_zero":    buildFrame(FrameHeaders, 0, 0, []byte{0}),
		"window_update_zero": buildFrame(FrameWindowUpdate, 0, 3, []byte{0, 0, 0, 0}),
		"push_promise":       buildFrame(FramePushPromise, 0, 1, []byte{0, 0, 0, 1}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewFrameReader(bytes.NewReader(raw))
			if _, err := r.ReadFrame(); err == nil {
				t.Fatal("malformed frame must be rejected")
			}
		})
	}
}

func TestPaddingLongerThanPayloadRejected(t *testing.T) {
	// PADDED DATA claiming 10 padding bytes with only 3 bytes after the length.
	raw := buildFrame(FrameData, flagPadded|flagEndStream, testStream,
		[]byte{10, 1, 2, 3})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		raw,
	)
	if err == nil || conv.Ready() {
		t.Fatalf("oversized padding must fail the stream, err=%v ready=%v", err, conv.Ready())
	}
}

func TestContinuationOnWrongStreamRejected(t *testing.T) {
	block := requestHeaders()
	half := len(block) / 2
	var raw []byte
	raw = append(raw, headersFrame(testStream, 0, block[:half])...)
	raw = append(raw, continuationFrame(testStream+2, flagEndHeaders, block[half:])...)
	conn := NewConnection(bytes.NewReader(raw), 4096)
	var err error
	for {
		if _, nextErr := conn.NextFrame(); nextErr != nil {
			if nextErr == io.EOF {
				break
			}
			err = nextErr
			break
		}
	}
	if err == nil {
		t.Fatal("CONTINUATION on a different stream must be rejected")
	}
}

func TestContinuationWithoutHeadersRejected(t *testing.T) {
	conn := NewConnection(
		bytes.NewReader(continuationFrame(testStream, flagEndHeaders, []byte{0})),
		4096,
	)
	_, err := conn.NextFrame()
	if err == nil {
		t.Fatal("CONTINUATION without HEADERS must be rejected")
	}
}

func TestConnectionHappyPath(t *testing.T) {
	var raw []byte
	raw = append(raw, buildFrame(FrameSettings, 0, 0, nil)...)
	raw = append(raw, headersFrame(testStream, flagEndHeaders, requestHeaders())...)
	raw = append(raw, dataFrame(testStream, true, []byte("ab"))...)
	conn := NewConnection(bytes.NewReader(raw), 4096)
	var ready *Converter
	for {
		conv, err := conn.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextFrame: %v", err)
		}
		if conv != nil && conv.Ready() {
			ready = conv
		}
	}
	if ready == nil {
		t.Fatal("connection never produced a ready converter")
	}
	body, _ := io.ReadAll(ready.Request().Body)
	if string(body) != "ab" {
		t.Fatalf("body = %q", body)
	}
}

func TestConnectionRejectsInterleavedHeaderBlock(t *testing.T) {
	block := requestHeaders()
	half := len(block) / 2
	var raw []byte
	raw = append(raw, headersFrame(testStream, 0, block[:half])...)
	// A DATA frame on another stream must not interrupt the open header block.
	raw = append(raw, dataFrame(testStream+2, true, []byte("x"))...)
	conn := NewConnection(bytes.NewReader(raw), 4096)
	var err error
	for {
		if _, nextErr := conn.NextFrame(); nextErr != nil {
			err = nextErr
			break
		}
	}
	if err == nil {
		t.Fatal("interleaving a header block must be rejected")
	}
}
