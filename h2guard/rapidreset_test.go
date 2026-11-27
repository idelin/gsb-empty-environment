package h2guard

import (
	"testing"
	"time"
)

func aggressiveRRConfig(clock *fakeClock) *Config {
	cfg := testConfig(clock)
	cfg.RapidReset = RapidResetConfig{
		Window:     time.Second,
		MaxResets:  10,
		ResetRatio: 0.5,
		MinOpens:   10,
	}
	return cfg
}

// openResetBurst performs openings paired with immediate RST_STREAM, the
// canonical CVE-2023-44487 pattern. Stream ids increase by two.
func openResetBurst(t *testing.T, p *Parser, clock *fakeClock, startID uint32, count int, interval time.Duration) (uint32, error) {
	t.Helper()
	streamID := startID
	for i := 0; i < count; i++ {
		block := hpackBlock(requestHeaders())
		if _, err := p.Handle(headersFrame(streamID, true, false, block)); err != nil {
			return streamID, err
		}
		if _, err := p.Handle(rstFrame(streamID, ErrCodeCancel)); err != nil {
			return streamID, err
		}
		streamID += 2
		clock.advance(interval)
	}
	return streamID, nil
}

// TestRapidResetTripsGOAWAY verifies ENHANCE_YOUR_CALM (0xb) + GOAWAY once
// both count and ratio thresholds are crossed.
func TestRapidResetTripsGOAWAY(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	p := NewParser(aggressiveRRConfig(clock))

	_, err := openResetBurst(t, p, clock, 1, 12, time.Millisecond)
	if err == nil {
		t.Fatal("rapid reset pattern was not detected")
	}
	assertConnError(t, err, ErrCodeEnhanceYourCalm)
	assertGoAway(t, p, ErrCodeEnhanceYourCalm)

	// After tripping, the parser refuses further frames.
	if _, err := p.Handle(pingFrame()); err == nil {
		t.Fatal("parser must stay closed after GOAWAY")
	}
}

// TestRapidResetRatioGuard ensures a few resets among many healthy streams do
// not trip even if the count alone is large across a quieter setup.
func TestRapidResetRatioGuard(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	cfg := aggressiveRRConfig(clock)
	cfg.RapidReset.MaxResets = 5
	p := NewParser(cfg)

	streamID := uint32(1)
	// 5 resets but 40 healthy streams: ratio 5/45 = 0.11 < 0.5.
	for i := 0; i < 45; i++ {
		block := hpackBlock(requestHeaders())
		if _, err := p.Handle(headersFrame(streamID, true, false, block)); err != nil {
			t.Fatalf("open stream %d: %v", streamID, err)
		}
		if i < 5 {
			if _, err := p.Handle(rstFrame(streamID, ErrCodeCancel)); err != nil {
				t.Fatalf("reset: %v", err)
			}
		}
		streamID += 2
		clock.advance(time.Millisecond)
	}
	if p.Closed() {
		t.Fatalf("healthy-heavy traffic must not trip: %v", p.FatalError())
	}
}

// TestRapidResetSlidingWindowExpiry shows events aging out of the window.
func TestRapidResetSlidingWindowExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	p := NewParser(aggressiveRRConfig(clock))

	// 9 resets, one short of the count threshold.
	nextID, err := openResetBurst(t, p, clock, 1, 9, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("burst below threshold tripped early: %v", err)
	}
	// Let the whole window expire.
	clock.advance(2 * time.Second)
	stats := p.RapidReset().Snapshot()
	if stats.Opens != 0 || stats.Resets != 0 {
		t.Fatalf("window did not expire: %+v", stats)
	}
	// A fresh burst starting from an empty window must still trip.
	_, err = openResetBurst(t, p, clock, nextID, 11, time.Millisecond)
	if err == nil {
		t.Fatal("expected trip after window reset")
	}
	assertConnError(t, err, ErrCodeEnhanceYourCalm)
}

// TestRSTAfterClientEndStreamNotCounted: a reset arriving after the client
// already sent END_STREAM is ordinary teardown, not a rapid reset.
func TestRSTAfterClientEndStreamNotCounted(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	cfg := aggressiveRRConfig(clock)
	cfg.RapidReset.MaxResets = 3
	p := NewParser(cfg)

	for i, id := 0, uint32(1); i < 10; i, id = i+1, id+2 {
		block := hpackBlock(requestHeaders())
		// END_STREAM on HEADERS: fully terminated client request.
		if _, err := p.Handle(headersFrame(id, true, true, block)); err != nil {
			t.Fatalf("headers: %v", err)
		}
		if _, err := p.Handle(rstFrame(id, ErrCodeCancel)); err != nil {
			t.Fatalf("post-end reset: %v", err)
		}
		clock.advance(time.Millisecond)
	}
	if p.Closed() {
		t.Fatalf("resets after END_STREAM must not count as rapid reset: %v", p.FatalError())
	}
}

// TestRapidResetGOAWAYPayload inspects the actual serialized GOAWAY frame.
func TestRapidResetGOAWAYPayload(t *testing.T) {
	clock := &fakeClock{t: time.Unix(10, 0)}
	p := NewParser(aggressiveRRConfig(clock))
	_, err := openResetBurst(t, p, clock, 1, 12, time.Millisecond)
	if err == nil {
		t.Fatal("expected trip")
	}
	goaway := p.GoAwayFrame()
	f := parseOne(t, goaway)
	if f.StreamID != 0 {
		t.Fatalf("GOAWAY must be on stream 0, got %d", f.StreamID)
	}
	if u32(f.Payload[4:8]) != ErrCodeEnhanceYourCalm {
		t.Fatalf("GOAWAY code must be 0xb, got 0x%x", u32(f.Payload[4:8]))
	}
	lastID := u32(f.Payload[0:4])
	if lastID == 0 {
		t.Fatal("GOAWAY last stream id should track processed streams")
	}
}

func pingFrame() *Frame {
	return &Frame{Type: FramePing, StreamID: 0, Payload: make([]byte, 8)}
}
