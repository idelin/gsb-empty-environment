package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// appSink collects every payload the machine authorizes for delivery. It lets
// tests assert the core security invariant: delivered bytes never exceed
// max_early_data and never include a withheld suffix.
type appSink struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	dropped uint64
}

func (s *appSink) apply(actions []Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range actions {
		switch a.Kind {
		case ActionDeliver:
			s.buf.Write(a.Payload)
		case ActionWithhold, ActionFail, ActionDrop:
			s.dropped += a.WithheldBytes
		}
	}
}

func (s *appSink) delivered() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	return out
}

func (s *appSink) deliveredLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

// startEarly drives a machine into the early-data state with the given limit.
func startEarly(t *testing.T, limit uint64) *Machine {
	t.Helper()
	m := New()
	if _, err := m.Handle(EventStart{MaxEarlyData: limit}); err != nil {
		t.Fatalf("EventStart: %v", err)
	}
	if _, err := m.Handle(EventBeginEarlyData{}); err != nil {
		t.Fatalf("EventBeginEarlyData: %v", err)
	}
	return m
}

func TestWithinLimitDelivered(t *testing.T) {
	const limit = uint64(10)
	m := startEarly(t, limit)
	sink := &appSink{}

	first := []byte("hello ") // 6
	second := []byte("world") // 5 -> cumulative 11 would cross, so split here

	actions, err := m.Handle(EventEarlyData{Payload: first})
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	sink.apply(actions)

	actions, err = m.Handle(EventEarlyData{Payload: second})
	if !errors.Is(err, ErrEarlyDataLimitExceeded) {
		t.Fatalf("expected limit error, got %v", err)
	}
	sink.apply(actions)

	// Only the 4 remaining in-limit bytes ("worl") may be delivered; the
	// fifth byte ("d") is the over-limit byte and must be withheld.
	if got, want := string(sink.delivered()), "hello worl"; got != want {
		t.Fatalf("delivered = %q, want %q", got, want)
	}
	if got := m.EarlyAccepted(); got != limit {
		t.Fatalf("accepted = %d, want %d", got, limit)
	}
	if got := m.EarlyWithheld(); got != 1 {
		t.Fatalf("withheld = %d, want 1", got)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want Failed", m.State())
	}
}

func TestOvershootByOneByte(t *testing.T) {
	const limit = uint64(5)
	m := startEarly(t, limit)
	sink := &appSink{}

	// The adversary sends exactly one byte more than the ticket allows.
	payload := []byte("ABCDEF") // limit + 1
	actions, err := m.Handle(EventEarlyData{Payload: payload})
	if !errors.Is(err, ErrEarlyDataLimitExceeded) {
		t.Fatalf("expected ErrEarlyDataLimitExceeded, got %v", err)
	}
	sink.apply(actions)

	if got, want := sink.deliveredLen(), int(limit); got != want {
		t.Fatalf("delivered %d bytes, want %d", got, want)
	}
	if got, want := string(sink.delivered()), "ABCDE"; got != want {
		t.Fatalf("delivered = %q, want %q", got, want)
	}

	var fail, withhold bool
	var withheldTotal uint64
	for _, a := range actions {
		switch a.Kind {
		case ActionFail:
			fail = true
			withheldTotal += a.WithheldBytes
		case ActionWithhold:
			withhold = true
			withheldTotal += a.WithheldBytes
		}
	}
	if !fail {
		t.Fatal("expected an ActionFail directive")
	}
	if withhold {
		t.Fatal("limit breach must be reported as ActionFail, not a bare Withhold")
	}
	if withheldTotal != 1 {
		t.Fatalf("withheld %d bytes, want 1", withheldTotal)
	}
	if m.State() != StateFailed || !errors.Is(m.Err(), ErrEarlyDataLimitExceeded) {
		t.Fatalf("state=%s err=%v, want Failed with limit error", m.State(), m.Err())
	}

	// Any further decrypted early data stays withheld after the failure.
	more, err := m.Handle(EventEarlyData{Payload: []byte("zzz")})
	if err != nil {
		t.Fatalf("post-failure record should not re-report an error, got %v", err)
	}
	sink.apply(more)
	if got := sink.deliveredLen(); got != int(limit) {
		t.Fatalf("delivered grew to %d after failure, want %d", got, limit)
	}
	if m.EarlyWithheld() != 4 { // 1 over-limit byte + 3 post-failure bytes
		t.Fatalf("withheld = %d, want 4", m.EarlyWithheld())
	}
	for _, a := range more {
		if a.Kind == ActionDeliver {
			t.Fatal("post-failure early data must never be delivered")
		}
	}
}

func TestExactLimitDoesNotFail(t *testing.T) {
	const limit = uint64(4)
	m := startEarly(t, limit)
	sink := &appSink{}

	actions, err := m.Handle(EventEarlyData{Payload: []byte("abcd")})
	if err != nil {
		t.Fatalf("exact-limit record: %v", err)
	}
	sink.apply(actions)
	if m.State() != StateEarlyData {
		t.Fatalf("state = %s, want EarlyData", m.State())
	}
	if m.EarlyAccepted() != limit || m.EarlyWithheld() != 0 {
		t.Fatalf("accepted=%d withheld=%d", m.EarlyAccepted(), m.EarlyWithheld())
	}

	// One more byte now crosses the ceiling and must be wholly withheld.
	actions, err = m.Handle(EventEarlyData{Payload: []byte("X")})
	if !errors.Is(err, ErrEarlyDataLimitExceeded) {
		t.Fatalf("expected limit error, got %v", err)
	}
	sink.apply(actions)
	if got := string(sink.delivered()); got != "abcd" {
		t.Fatalf("delivered = %q, want abcd", got)
	}
	if m.EarlyWithheld() != 1 {
		t.Fatalf("withheld = %d, want 1", m.EarlyWithheld())
	}
}

func TestZeroLimitRejectsAllEarlyData(t *testing.T) {
	m := startEarly(t, 0)
	sink := &appSink{}

	actions, err := m.Handle(EventEarlyData{Payload: []byte("any")})
	if !errors.Is(err, ErrEarlyDataLimitExceeded) {
		t.Fatalf("expected limit error with zero limit, got %v", err)
	}
	sink.apply(actions)
	if sink.deliveredLen() != 0 {
		t.Fatalf("delivered %d bytes under zero limit", sink.deliveredLen())
	}
	if m.EarlyWithheld() != 3 || m.State() != StateFailed {
		t.Fatalf("withheld=%d state=%s", m.EarlyWithheld(), m.State())
	}
	// A zero-length record at the ceiling is harmless and carries no bytes.
	if len(actions) != 1 || actions[0].Kind != ActionFail {
		t.Fatalf("actions = %+v, want single fail action", actions)
	}
}

func TestRejectedEarlyDataIsDropped(t *testing.T) {
	m := startEarly(t, 100)
	sink := &appSink{}

	sink.apply(mustHandle(t, m, EventEarlyData{Payload: []byte("ignored")}))
	actions, err := m.Handle(EventRejectEarlyData{})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	sink.apply(actions)

	// Delivered-then-rejected 0-RTT bytes are reclassified as dropped.
	if m.EarlyDropped() != 7 || m.EarlyAccepted() != 0 {
		t.Fatalf("dropped=%d accepted=%d", m.EarlyDropped(), m.EarlyAccepted())
	}
	if m.State() != StateHandshake {
		t.Fatalf("state = %s, want Handshake", m.State())
	}

	// Early data is no longer accepted after the rejection.
	if _, err := m.Handle(EventEarlyData{Payload: []byte("x")}); !errors.Is(err, ErrNoEarlyData) {
		t.Fatalf("expected ErrNoEarlyData, got %v", err)
	}
}

func TestOneRTTDataFlowsAfterHandshake(t *testing.T) {
	m := startEarly(t, 4)
	sink := &appSink{}
	sink.apply(mustHandle(t, m, EventEarlyData{Payload: []byte("0rtt")}))
	mustHandle(t, m, EventEndOfEarlyData{})
	mustHandle(t, m, EventHandshakeComplete{})

	actions, err := m.Handle(EventApplicationData{Payload: []byte("post-handshake")})
	if err != nil {
		t.Fatalf("1-RTT data: %v", err)
	}
	sink.apply(actions)
	if got, want := string(sink.delivered()), "0rttpost-handshake"; got != want {
		t.Fatalf("delivered = %q, want %q", got, want)
	}
	if m.State() != StateConnected {
		t.Fatalf("state = %s, want Connected", m.State())
	}
}

func TestEarlyDataBeforeAcceptanceRejected(t *testing.T) {
	m := New()
	mustHandle(t, m, EventStart{MaxEarlyData: 10})

	// No EventBeginEarlyData: early data must be refused.
	if _, err := m.Handle(EventEarlyData{Payload: []byte("x")}); !errors.Is(err, ErrNoEarlyData) {
		t.Fatalf("expected ErrNoEarlyData, got %v", err)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want Failed", m.State())
	}
}

func TestUnexpectedEventsFailClosed(t *testing.T) {
	m := New()
	// BeginEarlyData before Start is invalid.
	if _, err := m.Handle(EventBeginEarlyData{}); !errors.Is(err, ErrUnexpectedEvent) {
		t.Fatalf("expected ErrUnexpectedEvent, got %v", err)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want Failed", m.State())
	}

	// A failed machine refuses everything except Close and keeps withholding.
	sink := &appSink{}
	actions, _ := m.Handle(EventEarlyData{Payload: []byte("leak")})
	sink.apply(actions)
	if sink.deliveredLen() != 0 {
		t.Fatal("failed machine delivered bytes")
	}
	if _, err := m.Handle(EventClose{}); err != nil {
		t.Fatalf("close after failure: %v", err)
	}
	if m.State() != StateClosed {
		t.Fatalf("state = %s, want Closed", m.State())
	}
}

func TestNilEventFails(t *testing.T) {
	m := New()
	if _, err := m.Handle(nil); !errors.Is(err, ErrNilEvent) {
		t.Fatalf("expected ErrNilEvent, got %v", err)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want Failed", m.State())
	}
}

func TestAlertAborts(t *testing.T) {
	m := startEarly(t, 10)
	_, err := m.Handle(EventAlert{AlertDescription: 10}) // unexpected_message
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("expected ErrConnectionFailed, got %v", err)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want Failed", m.State())
	}
}

func TestConcurrentHandleIsSafe(t *testing.T) {
	const limit = uint64(1000)
	m := startEarly(t, limit)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink := &appSink{}
			for j := 0; j < 50; j++ {
				actions, _ := m.Handle(EventEarlyData{Payload: []byte{1, 2, 3, 4}})
				sink.apply(actions)
			}
		}()
	}
	wg.Wait()

	// Accepted bytes can never pass the ceiling regardless of interleaving.
	if m.EarlyAccepted() > limit {
		t.Fatalf("accepted %d exceeds limit %d", m.EarlyAccepted(), limit)
	}
	if m.EarlyAccepted()+m.EarlyWithheld() != 8*50*4 {
		t.Fatalf("accounting gap: accepted=%d withheld=%d", m.EarlyAccepted(), m.EarlyWithheld())
	}
}

func mustHandle(t *testing.T, m *Machine, ev Event) []Action {
	t.Helper()
	actions, err := m.Handle(ev)
	if err != nil {
		t.Fatalf("unexpected error for %#v: %v", ev, err)
	}
	return actions
}
