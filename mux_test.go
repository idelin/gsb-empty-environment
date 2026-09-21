package framemux

import (
	"bytes"
	"errors"
	"testing"
)

// endpoint is a Mux plus its outbound wire buffer; it implements
// io.Writer so it can be handed to NewMux.
type endpoint struct {
	m         *Mux
	peer      *endpoint
	out       [][]byte // one entry per Write, i.e. one whole frame
	sent      int
	dropEvery int // if >0, every dropEvery-th flushed chunk is lost
}

func (e *endpoint) Write(p []byte) (int, error) {
	e.out = append(e.out, append([]byte(nil), p...))
	return len(p), nil
}

func pair(cfgA, cfgB Config) (a, b *endpoint) {
	a = &endpoint{}
	b = &endpoint{}
	a.peer, b.peer = b, a
	a.m = NewMux(a, cfgA)
	b.m = NewMux(b, cfgB)
	return a, b
}

func feedChunked(t *testing.T, m *Mux, data []byte, chunk int) {
	t.Helper()
	for len(data) > 0 {
		n := chunk
		if n <= 0 || n > len(data) {
			n = len(data)
		}
		if err := m.Feed(data[:n]); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		data = data[n:]
	}
}

// flush delivers all pending outbound chunks of e to its peer, in
// chunks of `chunk` bytes, honouring the drop pattern.
func (e *endpoint) flush(t *testing.T, chunk int) {
	t.Helper()
	out := e.out
	e.out = nil
	for _, c := range out {
		e.sent++
		if e.dropEvery > 0 && e.sent%e.dropEvery == 0 {
			continue // lost on the wire
		}
		feedChunked(t, e.peer.m, c, chunk)
	}
}

// takeOut detaches and returns the pending outbound chunks.
func (e *endpoint) takeOut() [][]byte {
	out := e.out
	e.out = nil
	return out
}

func drain(t *testing.T, m *Mux, stream uint8) []byte {
	t.Helper()
	var out []byte
	for {
		p, ok := m.Receive(stream)
		if !ok {
			return out
		}
		out = append(out, p...)
	}
}

func badEvents(m *Mux) []Event {
	var out []Event
	for _, ev := range m.Events() {
		if ev.Kind == EventBadData {
			out = append(out, ev)
		}
	}
	return out
}

func TestRoundtripAllBytesMultiStream(t *testing.T) {
	a, b := pair(Config{}, Config{})
	var all [256]byte
	for i := range all {
		all[i] = byte(i)
	}
	msgs := map[uint8][][]byte{
		1: {[]byte("hello stream one"), all[:]},
		2: {[]byte("stream two"), {flagByte, escByte, flagByte}},
		3: {bytes.Repeat([]byte("x"), 1000)},
	}
	for s, list := range msgs {
		for _, p := range list {
			if err := a.m.Send(s, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.flush(t, 7) // arbitrary small chunks
	b.flush(t, 7) // ACKs back
	if a.m.Unacked() != 0 {
		t.Fatalf("unacked=%d", a.m.Unacked())
	}
	for s, list := range msgs {
		var want []byte
		for _, p := range list {
			want = append(want, p...)
		}
		if got := drain(t, b.m, s); !bytes.Equal(got, want) {
			t.Fatalf("stream %d: got %d bytes, want %d", s, len(got), len(want))
		}
	}
	if evs := badEvents(b.m); len(evs) != 0 {
		t.Fatalf("bad events: %v", evs)
	}
}

func TestBackpressureStreamAndTotalQuota(t *testing.T) {
	cfg := Config{StreamQuota: 10, TotalQuota: 15}
	a, b := pair(Config{}, cfg)
	payload := []byte("12345678") // 8 bytes

	// sendOne sends one frame and delivers exactly that frame.
	sendOne := func(stream uint8) error {
		if err := a.m.Send(stream, payload); err != nil {
			t.Fatal(err)
		}
		var err error
		for _, c := range a.takeOut() {
			if e := b.m.Feed(c); e != nil {
				err = e
			}
		}
		return err
	}

	if err := sendOne(1); err != nil { // stream1: 8/10, total 8/15
		t.Fatalf("frame 1: %v", err)
	}
	if err := sendOne(1); !errors.Is(err, ErrBackpressure) { // 16 > StreamQuota
		t.Fatalf("frame 2: want stream-quota backpressure, got %v", err)
	}
	// While blocked, further input is accepted but stays held; the
	// parser and negotiation state are preserved.
	if err := sendOne(2); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("frame 3: want backpressure while blocked, got %v", err)
	}
	if q := b.m.Queued(1); q != 8 {
		t.Fatalf("stream1 queued=%d, want 8", q)
	}
	// Drain stream 1 and resume: frame 2 is admitted (stream1 8,
	// total 8), then frame 3 blocks on the TOTAL quota (8+8 > 15)
	// even though stream 2's own quota has room.
	if _, ok := b.m.Receive(1); !ok {
		t.Fatal("nothing queued on stream 1")
	}
	if err := b.m.Resume(); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("resume: want total-quota backpressure, got %v", err)
	}
	if q := b.m.Queued(1); q != 8 {
		t.Fatalf("stream1 queued=%d after resume, want 8", q)
	}
	// Drain stream 1 again; now frame 3 fits under both quotas.
	if _, ok := b.m.Receive(1); !ok {
		t.Fatal("held frame not admitted")
	}
	if err := b.m.Resume(); err != nil {
		t.Fatalf("resume 2: %v", err)
	}
	if got, ok := b.m.Receive(2); !ok || !bytes.Equal(got, payload) {
		t.Fatalf("stream 2 delivery: %q ok=%v", got, ok)
	}
	if evs := badEvents(b.m); len(evs) != 0 {
		t.Fatalf("bad events: %v", evs)
	}
}

func TestEpochNegotiation(t *testing.T) {
	a, b := pair(Config{}, Config{})
	if err := a.m.ProposeEpoch(1); err != nil {
		t.Fatal(err)
	}
	if err := a.m.ProposeEpoch(1); err == nil {
		t.Fatal("second concurrent proposal not rejected")
	}
	a.flush(t, 0) // PROPOSE -> b confirms and switches
	if b.m.Epoch() != 1 {
		t.Fatalf("b epoch=%d, want 1", b.m.Epoch())
	}
	b.flush(t, 0) // ACK -> a switches
	if a.m.Epoch() != 1 {
		t.Fatalf("a epoch=%d, want 1", a.m.Epoch())
	}
	// Data flows after the switch, encoded with the new epoch.
	if err := a.m.Send(4, []byte("post-switch")); err != nil {
		t.Fatal(err)
	}
	a.flush(t, 0)
	if got := drain(t, b.m, 4); string(got) != "post-switch" {
		t.Fatalf("got %q", got)
	}
	// Both sides observed exactly one epoch change, no bad data.
	count := func(m *Mux) (changes, bad int) {
		for _, ev := range m.Events() {
			switch ev.Kind {
			case EventEpochChanged:
				changes++
			case EventBadData:
				bad++
			}
		}
		return
	}
	ca, ba := count(a.m)
	cb, bb := count(b.m)
	if ca != 1 || cb != 1 || ba != 0 || bb != 0 {
		t.Fatalf("events a=(%d,%d) b=(%d,%d)", ca, ba, cb, bb)
	}
}

// The switch confirmation crosses with old-epoch tail frames: frames
// sent before the ACK arrives must still be accepted after the peer has
// switched, and the switch itself must only happen at a frame boundary.
func TestEpochTailFramesInterleaved(t *testing.T) {
	a, b := pair(Config{}, Config{})

	if err := a.m.ProposeEpoch(1); err != nil { // a.out: [PROPOSE]
		t.Fatal(err)
	}
	if err := b.m.Send(2, []byte("tail-from-b")); err != nil { // b.out: [DATA e0]
		t.Fatal(err)
	}
	a.flush(t, 0) // b gets PROPOSE, queues ACK behind its tail frame, switches
	if b.m.Epoch() != 1 {
		t.Fatalf("b epoch=%d", b.m.Epoch())
	}
	if err := a.m.Send(1, []byte("tail-from-a")); err != nil { // a still epoch 0
		t.Fatal(err)
	}
	b.flush(t, 0) // a gets DATA(e0) then ACK(e0), then switches
	if a.m.Epoch() != 1 {
		t.Fatalf("a epoch=%d", a.m.Epoch())
	}
	a.flush(t, 0) // b gets a's old-epoch tail frame AFTER switching
	if got := drain(t, b.m, 1); string(got) != "tail-from-a" {
		t.Fatalf("b got %q", got)
	}
	if got := drain(t, a.m, 2); string(got) != "tail-from-b" {
		t.Fatalf("a got %q", got)
	}
	// New-epoch frames close the grace window on both sides.
	if err := a.m.Send(1, []byte("new-era")); err != nil {
		t.Fatal(err)
	}
	a.flush(t, 0)
	if got := drain(t, b.m, 1); string(got) != "new-era" {
		t.Fatalf("b got %q", got)
	}
	if evs := badEvents(a.m); len(evs) != 0 {
		t.Fatalf("a bad events: %v", evs)
	}
	if evs := badEvents(b.m); len(evs) != 0 {
		t.Fatalf("b bad events: %v", evs)
	}
}

// A reset (error) on one stream does not disturb the other stream.
func TestStreamErrorIsolation(t *testing.T) {
	a, b := pair(Config{}, Config{})
	a.m.Send(1, []byte("doomed"))
	a.m.Send(2, []byte("survivor"))
	a.flush(t, 0)

	if err := b.m.ResetStream(1); err != nil {
		t.Fatal(err)
	}
	b.flush(t, 0) // RESET(1) + ACKs reach a

	// b dropped stream 1's queued data; stream 2 is intact.
	if _, ok := b.m.Receive(1); ok {
		t.Fatal("stream 1 should have been cleared")
	}
	if got := drain(t, b.m, 2); string(got) != "survivor" {
		t.Fatalf("stream 2 damaged: %q", got)
	}
	// a sees the reset event for stream 1 only; and stream 2 still
	// works in both directions afterwards.
	sawReset := false
	for _, ev := range a.m.Events() {
		if ev.Kind == EventStreamReset && ev.Stream == 1 {
			sawReset = true
		}
		if ev.Kind == EventBadData {
			t.Fatalf("bad data: %v", ev.Err)
		}
	}
	if !sawReset {
		t.Fatal("a did not observe the reset of stream 1")
	}
	b.m.Send(2, []byte("back at you"))
	b.flush(t, 0)
	if got := drain(t, a.m, 2); string(got) != "back at you" {
		t.Fatalf("stream 2 reply broken: %q", got)
	}
}

// Corrupt wire bytes damage one frame but the connection and all other
// streams survive.
func TestCorruptionIsolation(t *testing.T) {
	a, b := pair(Config{}, Config{})
	a.m.Send(1, []byte("first"))
	a.m.Send(2, []byte("second"))
	out := a.takeOut()
	// Corrupt the first frame's payload byte (CRC catches it).
	out[0][len(out[0])/2] ^= 0xFF
	for _, c := range out {
		feedChunked(t, b.m, c, 0)
	}
	if evs := badEvents(b.m); len(evs) != 1 {
		t.Fatalf("want 1 bad-data event, got %v", evs)
	}
	if got := drain(t, b.m, 2); string(got) != "second" {
		t.Fatalf("stream 2 damaged by stream 1 corruption: %q", got)
	}
	// Sender retransmits the lost frame; receiver recovers it in order.
	if err := a.m.Retransmit(); err != nil {
		t.Fatal(err)
	}
	a.flush(t, 0)
	if got := drain(t, b.m, 1); string(got) != "first" {
		t.Fatalf("stream 1 not recovered: %q", got)
	}
}

// With a lossy wire, retransmission recovers the exact original streams
// and acknowledged frames are never delivered twice.
func TestRecoveryNoDuplicates(t *testing.T) {
	a, b := pair(Config{}, Config{})
	a.dropEvery = 5 // lose every 5th frame a->b
	b.dropEvery = 7 // and every 7th frame b->a (ACKs too)

	var all [256]byte
	for i := range all {
		all[i] = byte(i)
	}
	var want1, want2 []byte
	for i := 0; i < 30; i++ {
		p1 := append([]byte{byte(i)}, all[:]...)
		p2 := bytes.Repeat([]byte{byte(255 - i)}, 40)
		a.m.Send(1, p1)
		a.m.Send(2, p2)
		want1 = append(want1, p1...)
		want2 = append(want2, p2...)
	}
	var got1, got2 []byte
	for i := 0; i < 200 && a.m.Unacked() > 0; i++ {
		a.flush(t, 0)
		b.flush(t, 0)
		got1 = append(got1, drain(t, b.m, 1)...)
		got2 = append(got2, drain(t, b.m, 2)...)
		if err := a.m.Retransmit(); err != nil {
			t.Fatal(err)
		}
	}
	a.flush(t, 0)
	b.flush(t, 0)
	got1 = append(got1, drain(t, b.m, 1)...)
	got2 = append(got2, drain(t, b.m, 2)...)

	if a.m.Unacked() != 0 {
		t.Fatalf("still unacked after recovery: %d", a.m.Unacked())
	}
	if !bytes.Equal(got1, want1) {
		t.Fatalf("stream 1: got %d bytes, want %d (dup/loss)", len(got1), len(want1))
	}
	if !bytes.Equal(got2, want2) {
		t.Fatalf("stream 2: got %d bytes, want %d (dup/loss)", len(got2), len(want2))
	}
	// Duplicates must have been detected (and suppressed) at least once.
	dups := 0
	for _, ev := range b.m.Events() {
		if ev.Kind == EventDuplicate {
			dups++
		}
		if ev.Kind == EventBadData {
			t.Fatalf("unexpected bad data: %v", ev.Err)
		}
	}
	if dups == 0 {
		t.Fatal("expected some suppressed duplicate deliveries")
	}
}

// Negotiation state survives a pause: the ACK frame is fed in two
// chunks with a pause in between, and the epoch only switches once the
// whole confirming frame has arrived (frame boundary).
func TestPauseResumeDuringNegotiation(t *testing.T) {
	a, b := pair(Config{}, Config{})
	a.m.ProposeEpoch(1)
	a.flush(t, 0) // b confirms; ACK sits in b.out
	ack := b.takeOut()
	if len(ack) != 1 {
		t.Fatalf("expected 1 ACK frame, got %d", len(ack))
	}
	wire := ack[0]
	half := len(wire) / 2
	if err := a.m.Feed(wire[:half]); err != nil { // pause happens here
		t.Fatal(err)
	}
	if a.m.Epoch() != 0 {
		t.Fatal("epoch switched mid-frame")
	}
	// ... application pauses; parser keeps the partial ACK, the mux
	// keeps the pending negotiation ...
	if err := a.m.Feed(wire[half:]); err != nil { // resume
		t.Fatal(err)
	}
	if a.m.Epoch() != 1 {
		t.Fatalf("epoch=%d after resume, want 1", a.m.Epoch())
	}
}
