package h2guard

import (
	"bytes"
	"testing"
	"time"
)

func TestReadFrameRoundTrip(t *testing.T) {
	payload := []byte("hello-http2")
	wire := BuildFrame(FrameHeaders, FlagEndHeaders, 7, payload)
	f := parseOne(t, wire)
	if f.Type != FrameHeaders || !f.EndHeaders() || f.StreamID != 7 {
		t.Fatalf("header mismatch: %+v", f)
	}
	if string(f.Payload) != string(payload) {
		t.Fatalf("payload mismatch: %q", f.Payload)
	}
}

func TestReadFrameReservedBitStripped(t *testing.T) {
	// Set the reserved bit (top bit of stream id); parser must mask it.
	wire := BuildFrame(FramePing, 0, 0, make([]byte, 8))
	wire[5] |= 0x80
	f := parseOne(t, wire)
	if f.StreamID != 0 {
		t.Fatalf("reserved bit not stripped: %d", f.StreamID)
	}
}

func TestReadFrameOversizeRejected(t *testing.T) {
	wire := BuildFrame(FrameData, 0, 1, make([]byte, 200))
	_, err := ReadFrame(bytes.NewReader(wire), 100)
	assertConnError(t, err, ErrCodeFrameSize)
}

func TestReadFrameTruncated(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader([]byte{0, 0, 3, 1}), DefaultMaxFrameSize)
	if err != ErrShortRead {
		t.Fatalf("want ErrShortRead, got %v", err)
	}
}

func TestBuildRSTAndGoAway(t *testing.T) {
	rst := parseOne(t, BuildRSTStream(9, ErrCodeEnhanceYourCalm))
	if rst.Type != FrameRSTStream || rst.StreamID != 9 || u32(rst.Payload) != ErrCodeEnhanceYourCalm {
		t.Fatalf("RST frame wrong: %+v", rst)
	}

	goaway := parseOne(t, BuildGoAway(15, ErrCodeProtocol, []byte("boom")))
	if goaway.Type != FrameGoAway || goaway.StreamID != 0 {
		t.Fatalf("GOAWAY framing wrong: %+v", goaway)
	}
	if u32(goaway.Payload[0:4]) != 15 || u32(goaway.Payload[4:8]) != ErrCodeProtocol {
		t.Fatalf("GOAWAY payload wrong: %x", goaway.Payload)
	}
	if string(goaway.Payload[8:]) != "boom" {
		t.Fatalf("GOAWAY debug data lost: %q", goaway.Payload[8:])
	}
}

func TestStreamLevelErrorKeepsConnectionAlive(t *testing.T) {
	p := NewParser(nil)
	// DATA on an unopened stream is stream-scoped, not connection-scoped.
	_, err := p.Handle(dataFrame(5, true, []byte("x")))
	se, ok := AsStreamError(err)
	if !ok {
		t.Fatalf("want StreamError, got %T: %v", err, err)
	}
	if se.StreamID != 5 || se.Code != ErrCodeStreamClosed {
		t.Fatalf("stream error wrong: %+v", se)
	}
	if p.Closed() {
		t.Fatal("stream error must not close the connection")
	}
	// A subsequent legitimate request still parses.
	block := hpackBlock(requestHeaders())
	if _, err := p.Handle(headersFrame(1, true, true, block)); err != nil {
		t.Fatalf("connection unusable after stream error: %v", err)
	}
}

func TestEvenAndZeroStreamRejected(t *testing.T) {
	p := NewParser(nil)
	block := hpackBlock(requestHeaders())
	if _, err := p.Handle(headersFrame(2, true, false, block)); err == nil {
		t.Fatal("even client stream id must be rejected")
	}

	p2 := NewParser(nil)
	if _, err := p2.Handle(headersFrame(0, true, false, block)); err == nil {
		t.Fatal("stream id 0 HEADERS must be rejected")
	}
}

func TestPushPromiseRejectedByDefault(t *testing.T) {
	p := NewParser(nil)
	promised := make([]byte, 4)
	promised = append(promised, hpackBlock(requestHeaders())...)
	f := &Frame{Type: FramePushPromise, Flags: FlagEndHeaders, StreamID: 1, Payload: promised}
	_, err := p.Handle(f)
	assertConnError(t, err, ErrCodeProtocol)
}

func TestRunEndToEndAndGOAWAY(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	cfg := aggressiveRRConfig(clock)
	p := NewParser(cfg)

	var buf bytes.Buffer
	for _, f := range splitBlock(1, hpackBlock(requestHeaders()), 4, true) {
		buf.Write(BuildFrame(f.Type, f.Flags, f.StreamID, f.Payload))
	}
	buf.Write(BuildRSTStream(1, ErrCodeCancel))

	var events int
	err := p.Run(&buf, func(ev *Event) {
		events++
	}, func(se *StreamError) {
		t.Errorf("unexpected stream error: %v", se)
	})
	if err != nil {
		t.Fatalf("single reset must not trip: %v", err)
	}
	if events == 0 {
		t.Fatal("no events observed")
	}
}
