package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const testOrigin = "https://app.example"

type testStream struct {
	id   StreamID
	data []byte

	mu     sync.Mutex
	resets []uint64
}

func (s *testStream) ID() StreamID { return s.id }

func (s *testStream) Data() []byte { return s.data }

func (s *testStream) Reset(code uint64) {
	s.mu.Lock()
	s.resets = append(s.resets, code)
	s.mu.Unlock()
}

func (s *testStream) resetCodes() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.resets...)
}

func (s *testStream) resetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resets)
}

type receivedStream struct {
	session ConnectID
	data    StreamData
}

type recorder struct {
	mu        sync.Mutex
	streams   []receivedStream
	datagrams []struct {
		session ConnectID
		data    []byte
	}
}

func (r *recorder) DeliverStream(session *Session, stream StreamData) {
	r.mu.Lock()
	r.streams = append(r.streams, receivedStream{session: session.ID(), data: stream})
	r.mu.Unlock()
}

func (r *recorder) DeliverDatagram(session *Session, data []byte) {
	r.mu.Lock()
	r.datagrams = append(r.datagrams, struct {
		session ConnectID
		data    []byte
	}{session: session.ID(), data: append([]byte(nil), data...)})
	r.mu.Unlock()
}

func (r *recorder) streamCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

func (r *recorder) streamsSnapshot() []receivedStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receivedStream(nil), r.streams...)
}

func (r *recorder) datagramCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.datagrams)
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

func newTestRegistry(opts Options) *Registry {
	if len(opts.AllowedOrigins) == 0 {
		opts.AllowedOrigins = []string{testOrigin}
	}
	return New(opts)
}

func TestRegisterConnectValidatesOrigin(t *testing.T) {
	t.Parallel()

	registry := New(Options{
		AllowedOrigins:     []string{"HTTPS://APP.EXAMPLE:443"},
		PendingBufferLimit: 64,
		PendingTimeout:     time.Second,
	})
	sink := &recorder{}

	session, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   "https://app.example",
	}, sink)
	if err != nil {
		t.Fatalf("RegisterConnect with allowed origin: %v", err)
	}
	if session.ID() != ConnectID(4) {
		t.Fatalf("session ID = %d; want 4", session.ID())
	}
	if session.Origin() != testOrigin {
		t.Fatalf("session origin = %q; want %q", session.Origin(), testOrigin)
	}

	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, sink); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate RegisterConnect error = %v; want %v", err, ErrSessionExists)
	}

	badOrigins := []string{
		"",
		"null",
		"https://evil.example",
		"https://app.example.evil.example",
		"https://app.example/",
		"https://app.example@evil.example",
		"http://app.example",
	}
	for i, origin := range badOrigins {
		id := ConnectID(8 + i*4)
		if _, err := registry.RegisterConnect(ConnectRequest{StreamID: id, Origin: origin}, sink); !errors.Is(err, ErrOriginNotAllowed) {
			t.Fatalf("RegisterConnect(%q) error = %v; want %v", origin, err, ErrOriginNotAllowed)
		}
	}
	if got, want := registry.ActiveSessions(), 1; got != want {
		t.Fatalf("active sessions = %d; want %d", got, want)
	}

	// A rejected CONNECT must not accidentally authorize future traffic for
	// that stream ID.
	stream := &testStream{id: StreamID(200), data: []byte("not authorized")}
	if got := registry.HandleStream(ConnectID(8), stream); got != OutcomeBuffered {
		t.Fatalf("stream for rejected CONNECT = %v; want %v", got, OutcomeBuffered)
	}
	if sink.streamCount() != 0 {
		t.Fatal("stream for rejected CONNECT was delivered")
	}
}

func TestEmptyAllowlistDeniesConnect(t *testing.T) {
	t.Parallel()

	registry := New(Options{PendingBufferLimit: 64})
	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, &recorder{}); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("RegisterConnect error = %v; want %v", err, ErrOriginNotAllowed)
	}
}

func TestStreamsAndDatagramsBindByConnectID(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 64,
		PendingTimeout:     time.Second,
	})
	victimSink := &recorder{}
	attackerSink := &recorder{}

	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, victimSink); err != nil {
		t.Fatalf("register victim: %v", err)
	}

	original := []byte("victim-data")
	stream := &testStream{id: StreamID(8), data: original}
	if got := registry.HandleStream(ConnectID(4), stream); got != OutcomeDelivered {
		t.Fatalf("victim stream outcome = %v; want %v", got, OutcomeDelivered)
	}
	received := victimSink.streamsSnapshot()
	if len(received) != 1 {
		t.Fatalf("victim received %d streams; want 1", len(received))
	}
	if received[0].session != ConnectID(4) || received[0].data.ConnectID != ConnectID(4) ||
		received[0].data.StreamID != StreamID(8) || string(received[0].data.Data) != "victim-data" {
		t.Fatalf("unexpected victim delivery: %+v", received[0])
	}

	// Mutating the dispatcher's buffer after HandleStream must not change
	// what the session observed.
	original[0] = 'X'
	if string(received[0].data.Data) != "victim-data" {
		t.Fatal("delivered data aliases the input buffer")
	}

	if got := registry.HandleDatagram(ConnectID(4), []byte("victim-dgram")); got != OutcomeDelivered {
		t.Fatalf("victim datagram outcome = %v; want %v", got, OutcomeDelivered)
	}
	if victimSink.datagramCount() != 1 {
		t.Fatalf("victim received %d datagrams; want 1", victimSink.datagramCount())
	}

	// Traffic for a different (not yet established) CONNECT ID must be
	// buffered, never delivered to the victim session.
	forgedStream := &testStream{id: StreamID(100), data: []byte("forged")}
	if got := registry.HandleStream(ConnectID(12), forgedStream); got != OutcomeBuffered {
		t.Fatalf("forged stream outcome = %v; want %v", got, OutcomeBuffered)
	}
	if got := registry.HandleDatagram(ConnectID(12), []byte("forged-dgram")); got != OutcomeDropped {
		t.Fatalf("forged datagram outcome = %v; want %v", got, OutcomeDropped)
	}
	if victimSink.streamCount() != 1 || victimSink.datagramCount() != 1 {
		t.Fatal("traffic for another CONNECT ID reached the victim session")
	}

	// Once the legitimate session for ID 12 registers, its buffered stream
	// is delivered to that session only.
	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(12),
		Origin:   testOrigin,
	}, attackerSink); err != nil {
		t.Fatalf("register ID 12: %v", err)
	}
	attackerStreams := attackerSink.streamsSnapshot()
	if len(attackerStreams) != 1 {
		t.Fatalf("ID 12 received %d buffered streams; want 1", len(attackerStreams))
	}
	if attackerStreams[0].session != ConnectID(12) ||
		attackerStreams[0].data.ConnectID != ConnectID(12) ||
		attackerStreams[0].data.StreamID != StreamID(100) ||
		string(attackerStreams[0].data.Data) != "forged" {
		t.Fatalf("unexpected buffered delivery: %+v", attackerStreams[0])
	}
	if victimSink.streamCount() != 1 {
		t.Fatal("buffered stream for ID 12 was delivered to the victim")
	}

	if got := registry.HandleDatagram(ConnectID(12), []byte("id12-dgram")); got != OutcomeDelivered {
		t.Fatalf("ID 12 datagram outcome = %v; want %v", got, OutcomeDelivered)
	}
	if attackerSink.datagramCount() != 1 || victimSink.datagramCount() != 1 {
		t.Fatal("datagram was delivered to the wrong session")
	}
}

func TestForgedIDTimesOutWithoutReachingVictim(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 64,
		PendingTimeout:     20 * time.Millisecond,
	})
	victimSink := &recorder{}
	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, victimSink); err != nil {
		t.Fatalf("register victim: %v", err)
	}

	forged := &testStream{id: StreamID(100), data: []byte("attack")}
	if got := registry.HandleStream(ConnectID(12), forged); got != OutcomeBuffered {
		t.Fatalf("forged stream outcome = %v; want %v", got, OutcomeBuffered)
	}
	if victimSink.streamCount() != 0 {
		t.Fatal("forged stream reached the victim session")
	}
	if registry.PendingBytes() != len("attack") || registry.PendingConnects() != 1 {
		t.Fatalf("pending = (%d bytes, %d IDs); want (%d, 1)",
			registry.PendingBytes(), registry.PendingConnects(), len("attack"))
	}

	waitFor(t, 2*time.Second, func() bool { return forged.resetCount() > 0 })
	codes := forged.resetCodes()
	if len(codes) != 1 || codes[0] != ResetPendingTimeout {
		t.Fatalf("forged stream reset codes = %v; want [%d]", codes, ResetPendingTimeout)
	}
	if victimSink.streamCount() != 0 {
		t.Fatal("forged stream reached the victim session")
	}
	if registry.PendingBytes() != 0 || registry.PendingConnects() != 0 {
		t.Fatalf("pending state after timeout = (%d bytes, %d IDs); want empty",
			registry.PendingBytes(), registry.PendingConnects())
	}

	// The victim session must remain fully usable after the forged queue
	// expires.
	victimStream := &testStream{id: StreamID(8), data: []byte("legitimate")}
	if got := registry.HandleStream(ConnectID(4), victimStream); got != OutcomeDelivered {
		t.Fatalf("victim stream after timeout = %v; want %v", got, OutcomeDelivered)
	}
	if victimSink.streamCount() != 1 {
		t.Fatalf("victim received %d streams; want 1", victimSink.streamCount())
	}
}

func TestClosedSessionResetsImmediately(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 64,
		PendingTimeout:     time.Second,
	})
	sink := &recorder{}
	session, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, sink)
	if err != nil {
		t.Fatalf("register session: %v", err)
	}
	session.Close()
	if session.State() != SessionClosed {
		t.Fatalf("session state = %v; want closed", session.State())
	}
	if registry.ActiveSessions() != 0 {
		t.Fatal("closed session is still active")
	}
	session.Close() // idempotent

	lateStream := &testStream{id: StreamID(8), data: []byte("late")}
	if got := registry.HandleStream(ConnectID(4), lateStream); got != OutcomeReset {
		t.Fatalf("late stream outcome = %v; want %v", got, OutcomeReset)
	}
	codes := lateStream.resetCodes()
	if len(codes) != 1 || codes[0] != ResetSessionClosed {
		t.Fatalf("late stream reset codes = %v; want [%d]", codes, ResetSessionClosed)
	}
	if got := registry.HandleDatagram(ConnectID(4), []byte("late-dgram")); got != OutcomeDropped {
		t.Fatalf("late datagram outcome = %v; want %v", got, OutcomeDropped)
	}
	if sink.streamCount() != 0 || sink.datagramCount() != 0 {
		t.Fatal("traffic after close reached the session sink")
	}

	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, sink); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("re-register closed ID error = %v; want %v", err, ErrSessionClosed)
	}
	if registry.CloseSession(ConnectID(4)) {
		t.Fatal("CloseSession reported a transition for an already closed ID")
	}

	// A tombstone is per CONNECT ID and must not poison other sessions.
	otherSink := &recorder{}
	other, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(8),
		Origin:   testOrigin,
	}, otherSink)
	if err != nil {
		t.Fatalf("register other session: %v", err)
	}
	if got := registry.HandleStream(ConnectID(8), &testStream{id: StreamID(12), data: []byte("ok")}); got != OutcomeDelivered {
		t.Fatalf("stream for other session = %v; want %v", got, OutcomeDelivered)
	}
	if otherSink.streamCount() != 1 {
		t.Fatalf("other session received %d streams; want 1", otherSink.streamCount())
	}
	other.Close()
}

func TestPendingBufferOverflowAndTimeout(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 8,
		PendingTimeout:     50 * time.Millisecond,
	})

	first := &testStream{id: StreamID(100), data: []byte("12345")}
	if got := registry.HandleStream(ConnectID(12), first); got != OutcomeBuffered {
		t.Fatalf("first stream outcome = %v; want %v", got, OutcomeBuffered)
	}

	overflow := &testStream{id: StreamID(104), data: []byte("67890")}
	if got := registry.HandleStream(ConnectID(12), overflow); got != OutcomeReset {
		t.Fatalf("overflow stream outcome = %v; want %v", got, OutcomeReset)
	}
	if codes := overflow.resetCodes(); len(codes) != 1 || codes[0] != ResetBufferOverflow {
		t.Fatalf("overflow reset codes = %v; want [%d]", codes, ResetBufferOverflow)
	}
	if registry.PendingBytes() != 5 {
		t.Fatalf("pending bytes after overflow = %d; want 5", registry.PendingBytes())
	}

	empty := &testStream{id: StreamID(108)}
	if got := registry.HandleStream(ConnectID(12), empty); got != OutcomeBuffered {
		t.Fatalf("zero-length stream outcome = %v; want %v", got, OutcomeBuffered)
	}

	huge := &testStream{id: StreamID(112), data: []byte("123456789")}
	if got := registry.HandleStream(ConnectID(12), huge); got != OutcomeReset {
		t.Fatalf("single oversized stream outcome = %v; want %v", got, OutcomeReset)
	}
	if codes := huge.resetCodes(); len(codes) != 1 || codes[0] != ResetBufferOverflow {
		t.Fatalf("oversized reset codes = %v; want [%d]", codes, ResetBufferOverflow)
	}

	waitFor(t, 2*time.Second, func() bool {
		return first.resetCount() > 0 && empty.resetCount() > 0
	})
	if codes := first.resetCodes(); len(codes) != 1 || codes[0] != ResetPendingTimeout {
		t.Fatalf("first reset codes = %v; want [%d]", codes, ResetPendingTimeout)
	}
	if codes := empty.resetCodes(); len(codes) != 1 || codes[0] != ResetPendingTimeout {
		t.Fatalf("empty reset codes = %v; want [%d]", codes, ResetPendingTimeout)
	}
	if registry.PendingBytes() != 0 || registry.PendingConnects() != 0 {
		t.Fatalf("pending state after timeout = (%d bytes, %d IDs); want empty",
			registry.PendingBytes(), registry.PendingConnects())
	}

	// Expired data is gone, so a later legitimate registration starts clean.
	sink := &recorder{}
	if _, err := registry.RegisterConnect(ConnectRequest{StreamID: ConnectID(12), Origin: testOrigin}, sink); err != nil {
		t.Fatalf("register after expiry: %v", err)
	}
	if sink.streamCount() != 0 {
		t.Fatalf("expired streams were delivered: %d", sink.streamCount())
	}
}

func TestBufferLimitZeroRejectsNonEmptyPendingStream(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 0,
		PendingTimeout:     time.Second,
	})
	stream := &testStream{id: StreamID(100), data: []byte("x")}
	if got := registry.HandleStream(ConnectID(12), stream); got != OutcomeReset {
		t.Fatalf("stream outcome = %v; want %v", got, OutcomeReset)
	}
	if codes := stream.resetCodes(); len(codes) != 1 || codes[0] != ResetBufferOverflow {
		t.Fatalf("reset codes = %v; want [%d]", codes, ResetBufferOverflow)
	}
	if registry.PendingBytes() != 0 || registry.PendingConnects() != 0 {
		t.Fatal("zero buffer limit retained pending state")
	}
}

func TestRegisterFlushesPendingInOrderAndStopsTimer(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 64,
		PendingTimeout:     20 * time.Millisecond,
	})
	sink := &recorder{}

	one := &testStream{id: StreamID(100), data: []byte("one")}
	two := &testStream{id: StreamID(104), data: []byte("two")}
	if got := registry.HandleStream(ConnectID(12), one); got != OutcomeBuffered {
		t.Fatalf("first pending stream = %v; want %v", got, OutcomeBuffered)
	}
	if got := registry.HandleStream(ConnectID(12), two); got != OutcomeBuffered {
		t.Fatalf("second pending stream = %v; want %v", got, OutcomeBuffered)
	}

	session, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(12),
		Origin:   testOrigin,
	}, sink)
	if err != nil {
		t.Fatalf("register pending session: %v", err)
	}
	received := sink.streamsSnapshot()
	if len(received) != 2 {
		t.Fatalf("flushed %d streams; want 2", len(received))
	}
	if received[0].data.StreamID != StreamID(100) || string(received[0].data.Data) != "one" ||
		received[1].data.StreamID != StreamID(104) || string(received[1].data.Data) != "two" {
		t.Fatalf("pending flush order/content = %+v", received)
	}
	if registry.PendingBytes() != 0 || registry.PendingConnects() != 0 {
		t.Fatal("pending state survived registration")
	}

	// The old queue timer must have been stopped; waiting beyond the timeout
	// must not reset already delivered streams.
	time.Sleep(60 * time.Millisecond)
	if one.resetCount() != 0 || two.resetCount() != 0 {
		t.Fatalf("delivered streams were reset by stale timer: %v, %v", one.resetCodes(), two.resetCodes())
	}
	if sink.streamCount() != 2 {
		t.Fatalf("delivered stream count changed to %d", sink.streamCount())
	}
	session.Close()
}

func TestPendingMetadataLimits(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 64,
		PendingTimeout:     20 * time.Millisecond,
		MaxPendingConnects: 1,
		MaxPendingStreams:  1,
	})

	first := &testStream{id: StreamID(100), data: []byte("a")}
	if got := registry.HandleStream(ConnectID(12), first); got != OutcomeBuffered {
		t.Fatalf("first pending stream = %v; want %v", got, OutcomeBuffered)
	}

	secondQueue := &testStream{id: StreamID(104), data: []byte("b")}
	if got := registry.HandleStream(ConnectID(16), secondQueue); got != OutcomeReset {
		t.Fatalf("second CONNECT ID outcome = %v; want %v", got, OutcomeReset)
	}
	if codes := secondQueue.resetCodes(); len(codes) != 1 || codes[0] != ResetPendingLimit {
		t.Fatalf("second queue reset codes = %v; want [%d]", codes, ResetPendingLimit)
	}

	secondStream := &testStream{id: StreamID(108), data: []byte("c")}
	if got := registry.HandleStream(ConnectID(12), secondStream); got != OutcomeReset {
		t.Fatalf("second queued stream outcome = %v; want %v", got, OutcomeReset)
	}
	if codes := secondStream.resetCodes(); len(codes) != 1 || codes[0] != ResetPendingLimit {
		t.Fatalf("second queued stream reset codes = %v; want [%d]", codes, ResetPendingLimit)
	}

	waitFor(t, 2*time.Second, func() bool { return first.resetCount() > 0 })
	if registry.PendingConnects() != 0 || registry.PendingBytes() != 0 {
		t.Fatalf("pending state after expiry = (%d IDs, %d bytes)", registry.PendingConnects(), registry.PendingBytes())
	}
}

func TestConcurrentRouting(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(Options{
		PendingBufferLimit: 1024,
		PendingTimeout:     time.Second,
	})
	sink := &recorder{}
	if _, err := registry.RegisterConnect(ConnectRequest{
		StreamID: ConnectID(4),
		Origin:   testOrigin,
	}, sink); err != nil {
		t.Fatalf("register session: %v", err)
	}

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			stream := &testStream{id: StreamID(8 + i*4), data: []byte("x")}
			if got := registry.HandleStream(ConnectID(4), stream); got != OutcomeDelivered {
				t.Errorf("concurrent stream %d outcome = %v; want %v", i, got, OutcomeDelivered)
			}
		}(i)
	}
	wg.Wait()
	waitFor(t, 2*time.Second, func() bool { return sink.streamCount() == goroutines })
	if registry.PendingBytes() != 0 || registry.PendingConnects() != 0 {
		t.Fatalf("concurrent delivery left pending state")
	}
}
