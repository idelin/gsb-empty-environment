package h2guard

import (
	"bytes"
	"testing"
	"time"
)

// TestRunRapidResetEmitsGOAWAY drives the io.Reader entry point through a
// full rapid-reset attack and asserts the host can serialize the resulting
// GOAWAY straight from GoAwayFrame.
func TestRunRapidResetEmitsGOAWAY(t *testing.T) {
	clock := &fakeClock{t: time.Unix(42, 0)}
	cfg := aggressiveRRConfig(clock)
	p := NewParser(cfg)

	var wire bytes.Buffer
	id := uint32(1)
	for i := 0; i < 12; i++ {
		block := hpackBlock(requestHeaders())
		wire.Write(BuildFrame(FrameHeaders, FlagEndHeaders, id, block))
		wire.Write(BuildRSTStream(id, ErrCodeCancel))
		id += 2
		clock.advance(time.Millisecond)
	}

	err := p.Run(&wire, nil, nil)
	ce := assertConnError(t, err, ErrCodeEnhanceYourCalm)
	_ = ce

	goawayFrame := parseOne(t, p.GoAwayFrame())
	if goawayFrame.Type != FrameGoAway {
		t.Fatalf("want GOAWAY frame, got 0x%x", goawayFrame.Type)
	}
	if u32(goawayFrame.Payload[4:8]) != ErrCodeEnhanceYourCalm {
		t.Fatal("GOAWAY must carry 0xb")
	}
	// Further reads must fail fast rather than keep parsing attacker bytes.
	if _, err := p.Handle(pingFrame()); err == nil {
		t.Fatal("closed parser must reject new frames")
	}
}

// TestFirstFragmentOverByteCap rejects a huge HEADERS block immediately even
// before any CONTINUATION frame exists.
func TestFirstFragmentOverByteCap(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxHeaderBlockBytes = 16
	p := NewParser(&cfg)

	block := hpackBlock(requestHeaders(HeaderField{Name: "x-pad", Value: "0123456789abcdef"}))
	_, err := p.Handle(headersFrame(1, false, false, block))
	assertConnError(t, err, ErrCodeEnhanceYourCalm)
	assertGoAway(t, p, ErrCodeEnhanceYourCalm)
}

// TestUnknownFrameIgnored keeps the extension-point guarantee of RFC 9113.
func TestUnknownFrameIgnored(t *testing.T) {
	p := NewParser(nil)
	odd := &Frame{Type: 0x3f, StreamID: 1, Payload: []byte{1, 2, 3}}
	ev, err := p.Handle(odd)
	if err != nil || ev != nil {
		t.Fatalf("unknown frames must be ignored, got ev=%v err=%v", ev, err)
	}
}

// TestMalformedRSTRejected covers RST_STREAM framing violations.
func TestMalformedRSTRejected(t *testing.T) {
	p := NewParser(nil)
	if _, err := p.Handle(&Frame{Type: FrameRSTStream, StreamID: 0, Payload: make([]byte, 4)}); err == nil {
		t.Fatal("RST_STREAM on stream 0 must be rejected")
	}

	p2 := NewParser(nil)
	_, err := p2.Handle(&Frame{Type: FrameRSTStream, StreamID: 3, Payload: []byte{1, 2}})
	assertConnError(t, err, ErrCodeFrameSize)
}

// TestSettingsAdjustsFrameSize shows a peer SETTINGS frame changing the
// accepted payload size, and an out-of-range value being rejected.
func TestSettingsAdjustsFrameSize(t *testing.T) {
	p := NewParser(nil)
	settings := BuildSettings(false, map[uint16]uint32{5: 32768})
	f := parseOne(t, settings)
	if _, err := p.Handle(f); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	if p.cfg.MaxFrameSize != 32768 {
		t.Fatalf("max frame size not updated: %d", p.cfg.MaxFrameSize)
	}

	bad := BuildSettings(false, map[uint16]uint32{5: 1024})
	_, err := p.Handle(parseOne(t, bad))
	assertConnError(t, err, ErrCodeFrameSize)
}

// TestPaddedHeadersAndData verifies PADDED stripping end to end.
func TestPaddedHeadersAndData(t *testing.T) {
	p := NewParser(nil)
	block := hpackBlock(requestHeaders(HeaderField{Name: "content-length", Value: "3"}))

	payload := append([]byte{4}, block...)
	payload = append(payload, 0, 0, 0, 0)
	headers := &Frame{
		Type:     FrameHeaders,
		Flags:    FlagEndHeaders | FlagPadded,
		StreamID: 1,
		Payload:  payload,
	}
	if _, err := p.Handle(headers); err != nil {
		t.Fatalf("padded headers rejected: %v", err)
	}

	dataPayload := append([]byte{2}, []byte("abc")...)
	dataPayload = append(dataPayload, 0, 0)
	data := &Frame{Type: FrameData, Flags: FlagEndStream | FlagPadded, StreamID: 1, Payload: dataPayload}
	ev, err := p.Handle(data)
	if err != nil {
		t.Fatalf("padded data rejected: %v", err)
	}
	if string(ev.Data) != "abc" {
		t.Fatalf("padding not stripped: %q", ev.Data)
	}
}
