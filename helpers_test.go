package netsec

import (
	"net/netip"
	"sync"
	"testing"
	"time"
)

// probe records one PATH_CHALLENGE transmission as observed by the send
// callback, including the actual local/peer addresses used.
type probe struct {
	path  Path
	nonce Nonce
}

// recorder is a thread-safe fake SendFunc. It captures every probe with the
// exact addresses handed to the callback so tests can assert the datagram
// actually leaves on the candidate's own path.
type recorder struct {
	mu     sync.Mutex
	probes []probe
	err    error
}

func (r *recorder) send(path Path, nonce Nonce) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = append(r.probes, probe{path: path, nonce: nonce})
	return r.err
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.probes)
}

func (r *recorder) snapshot() []probe {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]probe, len(r.probes))
	copy(out, r.probes)
	return out
}

func (r *recorder) last() (probe, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.probes) == 0 {
		return probe{}, false
	}
	return r.probes[len(r.probes)-1], true
}

// eventLog collects lifecycle events for assertions.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) record(ev Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *eventLog) kinds() []EventKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]EventKind, len(l.events))
	for i, ev := range l.events {
		out[i] = ev.Kind
	}
	return out
}

func (l *eventLog) all() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// counterNonce returns deterministic, collision-free nonces for tests.
func counterNonce(t *testing.T) NonceSource {
	t.Helper()
	var n uint64
	var mu sync.Mutex
	return func() (Nonce, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		var out Nonce
		for i := 0; i < 8; i++ {
			out[7-i] = byte(n >> (8 * i))
		}
		return out, nil
	}
}

// harness bundles the common test fixtures.
type harness struct {
	clock    *FakeClock
	rec      *recorder
	events   *eventLog
	v        *Validator
	nonces   NonceSource
	base     time.Time
	interval time.Duration
}

func newHarness(t *testing.T, conn ConnID, interval time.Duration) *harness {
	t.Helper()
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(base)
	rec := &recorder{}
	ev := &eventLog{}
	nonces := counterNonce(t)
	v, err := NewValidator(Config{
		Conn:          conn,
		Clock:         clock,
		RetryInterval: interval,
		NonceSource:   nonces,
		Send:          rec.send,
		OnEvent:       ev.record,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return &harness{
		clock:    clock,
		rec:      rec,
		events:   ev,
		v:        v,
		nonces:   nonces,
		base:     base,
		interval: interval,
	}
}

func mustAddr(t *testing.T, s string) netip.AddrPort {
	t.Helper()
	a, err := netip.ParseAddrPort(s)
	if err != nil {
		t.Fatalf("ParseAddrPort(%q): %v", s, err)
	}
	return a
}

// pathFor builds a candidate path for a connection instance.
func pathFor(t *testing.T, conn ConnID, local, peer string) Path {
	t.Helper()
	return Path{Conn: conn, Local: mustAddr(t, local), Peer: mustAddr(t, peer)}
}

func assertErrIs(t *testing.T, got, want error) {
	t.Helper()
	if got == nil {
		t.Fatalf("want error %v, got nil", want)
	}
	if got != want {
		t.Fatalf("want error %v, got %v", want, got)
	}
}

func assertActive(t *testing.T, v *Validator, want Path) {
	t.Helper()
	got, ok := v.Active()
	if !ok {
		t.Fatalf("want active path %v, got none", want)
	}
	if got != want {
		t.Fatalf("active path = %v, want %v", got, want)
	}
}

func assertNoActive(t *testing.T, v *Validator) {
	t.Helper()
	if got, ok := v.Active(); ok {
		t.Fatalf("want no active path, got %v", got)
	}
}
