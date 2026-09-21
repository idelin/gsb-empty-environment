package mux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

const (
	testMaxPayload = 1024
	testQuota      = 1 << 20
	testWindow     = 4096
)

func newPair(t *testing.T) (a, b *Session) {
	t.Helper()
	a = NewSession(testMaxPayload, testQuota, testWindow)
	b = NewSession(testMaxPayload, testQuota, testWindow)
	return a, b
}

// pump flushes a and delivers the bytes to b in chunks of the given sizes.
func pump(t *testing.T, from, to *Session, chunkSizes ...int) {
	t.Helper()
	wire := from.Flush()
	for len(wire) > 0 {
		n := len(wire)
		if len(chunkSizes) > 0 {
			n = chunkSizes[0]
			chunkSizes = chunkSizes[1:]
			if n > len(wire) {
				n = len(wire)
			}
		}
		if err := to.Receive(wire[:n]); err != nil {
			t.Fatalf("Receive: %v", err)
		}
		wire = wire[n:]
	}
}

func TestParserArbitraryFragmentation(t *testing.T) {
	f1 := Frame{Type: FrameData, StreamID: 7, Payload: []byte("hello world")}
	f2 := Frame{Type: FrameClose, StreamID: 7}
	wire, err := EncodeFrame(f1, testMaxPayload)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := EncodeFrame(f2, testMaxPayload)
	if err != nil {
		t.Fatal(err)
	}
	wire = append(wire, w2...)

	p := NewParser(testMaxPayload)
	var got []Frame
	rng := rand.New(rand.NewSource(1))
	for len(wire) > 0 {
		n := 1 + rng.Intn(len(wire)) // random chunk sizes, often 1 byte
		p.Feed(wire[:n])
		wire = wire[n:]
		for {
			f, err := p.Next()
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2", len(got))
	}
	if got[0].Type != FrameData || got[0].StreamID != 7 || !bytes.Equal(got[0].Payload, []byte("hello world")) {
		t.Fatalf("frame 1 mismatch: %+v", got[0])
	}
	if got[1].Type != FrameClose || got[1].StreamID != 7 {
		t.Fatalf("frame 2 mismatch: %+v", got[1])
	}
}

func TestParserDistinguishesNeedMoreFromBad(t *testing.T) {
	// Truncated header: need more data, not an error.
	p := NewParser(testMaxPayload)
	p.Feed([]byte{byte(FrameData), 0, 0})
	if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("truncated header: got %v, want ErrNeedMoreData", err)
	}
	// Truncated payload: still need more data.
	p = NewParser(testMaxPayload)
	full, _ := EncodeFrame(Frame{Type: FrameData, StreamID: 1, Payload: []byte("0123456789")}, testMaxPayload)
	p.Feed(full[:len(full)-3])
	if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("truncated payload: got %v, want ErrNeedMoreData", err)
	}
	// Unknown frame type: bad data immediately.
	p = NewParser(testMaxPayload)
	p.Feed([]byte{0xEE, 0, 0, 0, 1, 0, 0, 0, 0})
	var pe *ProtocolError
	if _, err := p.Next(); !errors.As(err, &pe) {
		t.Fatalf("bad type: got %v, want *ProtocolError", err)
	}
}

func TestParserChecksLengthQuotaBeforeAlloc(t *testing.T) {
	p := NewParser(64)
	// Header declares a huge payload but no payload bytes have arrived.
	hdr := make([]byte, HeaderSize)
	hdr[0] = byte(FrameData)
	binary.BigEndian.PutUint32(hdr[5:9], 1<<30) // 1 GiB declared
	p.Feed(hdr)
	var pe *ProtocolError
	if _, err := p.Next(); !errors.As(err, &pe) {
		t.Fatalf("oversize length: got %v, want *ProtocolError before allocation", err)
	}
}

func TestWindowExhaustionDoesNotBlockOtherStream(t *testing.T) {
	a, b := newPair(t)
	received := map[uint32]int{}
	b.OnData = func(id uint32, d []byte) { received[id] += len(d) }

	for _, id := range []uint32{1, 2} {
		if err := a.OpenStream(id); err != nil {
			t.Fatal(err)
		}
	}
	pump(t, a, b) // deliver OPENs

	// Stream 1 gets far more data than its window; stream 2 a small amount.
	big := bytes.Repeat([]byte("A"), 3*testWindow)
	if err := a.SendData(1, big); err != nil {
		t.Fatal(err)
	}
	if err := a.SendData(2, []byte("B-payload")); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b)

	if received[1] != testWindow {
		t.Fatalf("stream 1 delivered %d bytes, want exactly window %d", received[1], testWindow)
	}
	if received[2] != len("B-payload") {
		t.Fatalf("stream 2 delivered %d bytes, want %d: window exhaustion of stream 1 blocked stream 2", received[2], len("B-payload"))
	}

	// After b returns credit, stream 1 makes progress again.
	pump(t, b, a) // WINDOW_UPDATEs
	pump(t, a, b) // more DATA
	if received[1] <= testWindow {
		t.Fatalf("stream 1 did not resume after window update: %d", received[1])
	}
}

func TestDuplicateWindowUpdateNotDoubleCounted(t *testing.T) {
	a, b := newPair(t)
	if err := a.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b)
	if err := b.SendData(1, []byte("1234567890")); err != nil {
		t.Fatal(err)
	}
	pump(t, b, a) // a consumes 10 bytes, queues WINDOW_UPDATE(seq=1, inc=10)

	wire := a.Flush()
	before := b.streams[1].sendWindow
	if err := b.Receive(wire); err != nil {
		t.Fatal(err)
	}
	afterFirst := b.streams[1].sendWindow
	if afterFirst != before+10 {
		t.Fatalf("first update: window %d -> %d, want +10", before, afterFirst)
	}
	// Re-deliver the exact same bytes (e.g. a duplicated network segment).
	if err := b.Receive(wire); err != nil {
		t.Fatal(err)
	}
	if got := b.streams[1].sendWindow; got != afterFirst {
		t.Fatalf("duplicate WINDOW_UPDATE double-counted: window %d, want %d", got, afterFirst)
	}
}

func TestDataAfterCloseIsProtocolError(t *testing.T) {
	a, b := newPair(t)
	if err := a.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b)
	if err := a.Close(1); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b) // b now sees stream 1 half-closed remote

	// Late data frame from a after CLOSE.
	late, _ := EncodeFrame(Frame{Type: FrameData, StreamID: 1, Payload: []byte("late")}, testMaxPayload)
	var pe *ProtocolError
	if err := b.Receive(late); !errors.As(err, &pe) {
		t.Fatalf("data after close: got %v, want *ProtocolError", err)
	}
}

func TestHalfCloseAllowsResponse(t *testing.T) {
	a, b := newPair(t)
	var bGot []byte
	b.OnData = func(id uint32, d []byte) { bGot = append(bGot, d...) }
	var aGot []byte
	a.OnData = func(id uint32, d []byte) { aGot = append(aGot, d...) }

	if err := a.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b)
	if err := a.SendData(1, []byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(1); err != nil { // a half-closes its send side
		t.Fatal(err)
	}
	pump(t, a, b)
	if string(bGot) != "request" {
		t.Fatalf("b got %q, want %q", bGot, "request")
	}
	// b may still send the response on the half-closed stream.
	if err := b.SendData(1, []byte("response")); err != nil {
		t.Fatalf("b send on half-closed stream: %v", err)
	}
	pump(t, b, a)
	if string(aGot) != "response" {
		t.Fatalf("a got %q, want %q", aGot, "response")
	}
	// But a can no longer send.
	if err := a.SendData(1, []byte("nope")); err == nil {
		t.Fatal("a send after local close: want error, got nil")
	}
}

func TestControlFramesPrioritizedUnderQuota(t *testing.T) {
	// Quota fits exactly one control frame plus nothing else.
	a := NewSession(testMaxPayload, HeaderSize+8, testWindow)
	if err := a.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	if err := a.SendData(1, []byte("data")); err != nil {
		t.Fatal(err)
	}
	wire := a.Flush()
	p := NewParser(testMaxPayload)
	p.Feed(wire)
	f, err := p.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.Type != FrameOpen {
		t.Fatalf("first frame under tight quota = %v, want control OPEN", f.Type)
	}
	if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("data frame should have been deferred by quota, got %v", err)
	}
	// Next flush emits the deferred DATA.
	wire = a.Flush()
	p.Feed(wire)
	f, err = p.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.Type != FrameData {
		t.Fatalf("second flush first frame = %v, want DATA", f.Type)
	}
}

func TestFlowControlViolationIsProtocolError(t *testing.T) {
	a, b := newPair(t)
	if err := a.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	pump(t, a, b)
	// Craft a DATA frame larger than the receive window.
	big := make([]byte, testWindow+1)
	enc, _ := EncodeFrame(Frame{Type: FrameData, StreamID: 1, Payload: big}, testWindow+1)
	b.parser = NewParser(testWindow + 1) // allow the size past the parser
	var pe *ProtocolError
	if err := b.Receive(enc); !errors.As(err, &pe) {
		t.Fatalf("window overflow: got %v, want *ProtocolError", err)
	}
}

func TestEndToEndMultiStreamTransfer(t *testing.T) {
	a, b := newPair(t)
	got := map[uint32][]byte{}
	b.OnData = func(id uint32, d []byte) { got[id] = append(got[id], d...) }

	payloads := map[uint32][]byte{
		1: bytes.Repeat([]byte("alpha-"), 3000),
		2: bytes.Repeat([]byte("beta-"), 2000),
		3: bytes.Repeat([]byte("gamma-"), 1000),
	}
	for id := range payloads {
		if err := a.OpenStream(id); err != nil {
			t.Fatal(err)
		}
	}
	pump(t, a, b)
	for id, p := range payloads {
		if err := a.SendData(id, p); err != nil {
			t.Fatal(err)
		}
	}
	// Alternate flushes both ways until everything lands, delivering in
	// random small chunks to exercise the incremental parser.
	rng := rand.New(rand.NewSource(42))
	deliver := func(from, to *Session) {
		wire := from.Flush()
		for len(wire) > 0 {
			n := 1 + rng.Intn(len(wire))
			if err := to.Receive(wire[:n]); err != nil {
				t.Fatalf("Receive: %v", err)
			}
			wire = wire[n:]
		}
	}
	for i := 0; i < 1000; i++ {
		deliver(a, b)
		deliver(b, a) // window updates flow back
		done := true
		for id, p := range payloads {
			if len(got[id]) != len(p) {
				done = false
			}
		}
		if done {
			break
		}
	}
	for id, p := range payloads {
		if !bytes.Equal(got[id], p) {
			t.Fatalf("stream %d: got %d bytes, want %d (content match=%v)",
				id, len(got[id]), len(p), bytes.Equal(got[id], p))
		}
	}
}
