package h2guard

import (
	"testing"
	"time"
)

func assertConnError(t *testing.T, err error, wantCode uint32) *ConnError {
	t.Helper()
	ce, ok := AsConnError(err)
	if !ok {
		t.Fatalf("want *ConnError code=0x%x, got %T: %v", wantCode, err, err)
	}
	if ce.Code != wantCode {
		t.Fatalf("want error code 0x%x, got 0x%x (%s)", wantCode, ce.Code, ce.Message)
	}
	return ce
}

func assertGoAway(t *testing.T, p *Parser, wantCode uint32) {
	t.Helper()
	if !p.Closed() {
		t.Fatal("parser should be closed after connection error")
	}
	goaway := p.GoAwayFrame()
	if goaway == nil {
		t.Fatal("GOAWAY frame should be available after connection error")
	}
	f := parseOne(t, goaway)
	if f.Type != FrameGoAway {
		t.Fatalf("want GOAWAY, got type 0x%x", f.Type)
	}
	if len(f.Payload) < 8 {
		t.Fatalf("GOAWAY payload too short: %d", len(f.Payload))
	}
	gotCode := u32(f.Payload[4:8])
	if gotCode != wantCode {
		t.Fatalf("GOAWAY code: want 0x%x, got 0x%x", wantCode, gotCode)
	}
}

// TestContinuationFlood: a header block that keeps emitting CONTINUATION
// frames without END_HEADERS must trip ENHANCE_YOUR_CALM + GOAWAY.
func TestContinuationFlood(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1, 0)}
	cfg := testConfig(clock)
	cfg.MaxContinuationFrames = 3
	cfg.MaxHeaderBlockBytes = 1 << 16
	p := NewParser(cfg)

	block := hpackBlock(requestHeaders())
	frames := splitBlock(1, block, 1, false)

	var sawFlood bool
	for i, f := range frames {
		_, err := p.Handle(f)
		if err != nil {
			ce := assertConnError(t, err, ErrCodeEnhanceYourCalm)
			if i <= cfg.MaxContinuationFrames {
				t.Fatalf("flood tripped too early at frame %d", i)
			}
			if ce.Message == "" {
				t.Fatal("enhance-your-calm error has no message")
			}
			sawFlood = true
			break
		}
	}
	if !sawFlood {
		t.Fatal("CONTINUATION flood was not detected")
	}
	assertGoAway(t, p, ErrCodeEnhanceYourCalm)
}

// TestHeaderBlockByteCap: even few frames must be rejected once accumulated
// header-block bytes cross the hard byte ceiling.
func TestHeaderBlockByteCap(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1, 0)}
	cfg := testConfig(clock)
	cfg.MaxContinuationFrames = 1000
	cfg.MaxHeaderBlockBytes = 8
	p := NewParser(cfg)

	big := hpackBlock(requestHeaders(HeaderField{Name: "x-evidence", Value: "aaaaaaaaaaaaaaaa"}))
	frames := splitBlock(1, big, 4, false)

	var err error
	for _, f := range frames {
		_, err = p.Handle(f)
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("expected header-block byte cap to trip")
	}
	assertConnError(t, err, ErrCodeEnhanceYourCalm)
	assertGoAway(t, p, ErrCodeEnhanceYourCalm)
}

// TestCrossStreamInterleave: while stream 1's block is open, any frame that is
// not stream-1 CONTINUATION — including another stream's HEADERS, same-stream
// DATA, and a wrong-stream CONTINUATION — must be a PROTOCOL_ERROR + GOAWAY.
func TestCrossStreamInterleave(t *testing.T) {
	block := hpackBlock(requestHeaders())

	cases := []struct {
		name  string
		frame *Frame
	}{
		{
			name:  "headers on another stream",
			frame: headersFrame(3, true, false, hpackBlock(requestHeaders())),
		},
		{
			name:  "data interleaved on same stream",
			frame: dataFrame(1, false, []byte("x")),
		},
		{
			name:  "continuation for wrong stream",
			frame: continuationFrame(3, true, block[:1]),
		},
		{
			name:  "settings on connection while block open",
			frame: &Frame{Type: FrameSettings, StreamID: 0, Payload: nil, Flags: FlagAck},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{t: time.Unix(1, 0)}
			p := NewParser(testConfig(clock))

			open := headersFrame(1, false, false, block[:2])
			if _, err := p.Handle(open); err != nil {
				t.Fatalf("opening block: %v", err)
			}
			if !p.block.active {
				t.Fatal("header block should remain open")
			}
			_, err := p.Handle(tc.frame)
			assertConnError(t, err, ErrCodeProtocol)
			assertGoAway(t, p, ErrCodeProtocol)
		})
	}
}

// TestContinuationWithoutBlock is the mirror side of the state machine.
func TestContinuationWithoutBlock(t *testing.T) {
	p := NewParser(nil)
	_, err := p.Handle(continuationFrame(1, true, []byte{0x82}))
	assertConnError(t, err, ErrCodeProtocol)
	assertGoAway(t, p, ErrCodeProtocol)
}

// TestValidBlockAcrossContinuations proves the state machine still accepts a
// legitimate block spread over CONTINUATION frames and decodes it correctly.
func TestValidBlockAcrossContinuations(t *testing.T) {
	p := NewParser(nil)
	block := hpackBlock(requestHeaders(HeaderField{Name: "user-agent", Value: "h2guard-test"}))
	var ev *Event
	for _, f := range splitBlock(1, block, 3, true) {
		var err error
		ev, err = p.Handle(f)
		if err != nil {
			t.Fatalf("legitimate continuation rejected: %v", err)
		}
	}
	if ev == nil || ev.Type != EventHeaders {
		t.Fatalf("want headers event, got %+v", ev)
	}
	if ev.Headers.Method != "POST" || ev.Headers.Path != "/api" || ev.Headers.Authority != "example.com" {
		t.Fatalf("pseudo-headers decoded wrong: %+v", ev.Headers)
	}
}
