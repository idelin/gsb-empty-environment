package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// controlledAuth is a scriptable Authenticator. It holds one key per
// generation, can be forced to reject calls, and records every generation it
// verifies so tests can prove forged packets never drive a key update.
type controlledAuth struct {
	mu       sync.Mutex
	keys     map[uint64][]byte
	failLeft int32
	calls    int32
	gens     []uint64
	gate     chan struct{}
}

func newControlledAuth(keys ...[]byte) *controlledAuth {
	c := &controlledAuth{keys: make(map[uint64][]byte)}
	for i, k := range keys {
		c.keys[uint64(i)] = k
	}
	return c
}

// failNext forces the next n Authenticate calls to report a bad tag.
func (c *controlledAuth) failNext(n int) { atomic.StoreInt32(&c.failLeft, int32(n)) }

func (c *controlledAuth) callCount() int { return int(atomic.LoadInt32(&c.calls)) }

func (c *controlledAuth) seenGens() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uint64, len(c.gens))
	copy(out, c.gens)
	return out
}

func (c *controlledAuth) tag(generation uint64, covered []byte) []byte {
	mac := hmac.New(sha256.New, c.keys[generation])
	mac.Write(covered)
	return mac.Sum(nil)
}

func (c *controlledAuth) Authenticate(generation uint64, payload []byte) error {
	if c.gate != nil {
		<-c.gate
	}
	atomic.AddInt32(&c.calls, 1)
	c.mu.Lock()
	c.gens = append(c.gens, generation)
	c.mu.Unlock()

	if atomic.AddInt32(&c.failLeft, -1) >= 0 {
		return errors.New("controlled auth: forced bad tag")
	}
	key, ok := c.keys[generation]
	if !ok {
		return fmt.Errorf("controlled auth: no key for generation %d", generation)
	}
	if len(payload) < sha256.Size {
		return errors.New("controlled auth: payload missing tag")
	}
	covered := payload[:len(payload)-sha256.Size]
	got := payload[len(payload)-sha256.Size:]
	mac := hmac.New(sha256.New, key)
	mac.Write(covered)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return errors.New("controlled auth: bad tag")
	}
	return nil
}

// frame builds a datagram carrying an authentic payload+tag for a full number.
func frame(t *testing.T, width byte, gen, number uint64, body string, auth *controlledAuth) []byte {
	t.Helper()
	covered := []byte(body)
	payload := append(append([]byte{}, covered...), auth.tag(gen, covered)...)
	phase := byte(0)
	if gen&1 == 1 {
		phase = WirePhaseBit
	}
	// The wire only carries the low width*8 bits; decodePacketNumber restores
	// the full number at the receiver.
	truncated := number & ((uint64(1) << (width * 8)) - 1)
	datagram, err := EncodeDatagram(width, phase, truncated, payload)
	if err != nil {
		t.Fatalf("EncodeDatagram: %v", err)
	}
	return datagram
}

func mustReceive(t *testing.T, r *Receiver, datagram []byte, wantNumber, wantGen uint64) {
	t.Helper()
	pkt, err := r.Receive(datagram)
	if err != nil {
		t.Fatalf("Receive unexpected error: %v", err)
	}
	if pkt.Number != wantNumber || pkt.Generation != wantGen {
		t.Fatalf("Receive = (n=%d gen=%d), want (n=%d gen=%d)",
			pkt.Number, pkt.Generation, wantNumber, wantGen)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	if _, err := New(5, Key("k0"), auth); !errors.Is(err, ErrBadWidth) {
		t.Fatalf("width 5: want ErrBadWidth, got %v", err)
	}
	if _, err := New(Width1, Key("k0"), auth, WithReplayWindow(0)); !errors.Is(err, ErrBadReplayWindow) {
		t.Fatalf("zero window: want ErrBadReplayWindow, got %v", err)
	}
	if _, err := New(Width1, Key("k0"), nil); err == nil {
		t.Fatal("nil authenticator must be rejected")
	}
}

func TestDecodeTieGoesToSmaller(t *testing.T) {
	// expected 128 puts truncated 0 exactly halfway between candidates 0 and
	// 256 (both distance 128); the smaller candidate 0 must win the tie.
	got, err := decodePacketNumber(0, Width1, 128)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != 0 {
		t.Fatalf("tie break = %d, want smaller candidate 0", got)
	}

	// Off the midpoint the closer candidate wins: truncated 0 against
	// expected 127 is closest to 0, against expected 129 closest to 256.
	if got, _ := decodePacketNumber(0, Width1, 127); got != 0 {
		t.Fatalf("expected 127: got %d, want 0", got)
	}
	if got, _ := decodePacketNumber(0, Width1, 129); got != 256 {
		t.Fatalf("expected 129: got %d, want 256", got)
	}
	got, _ = decodePacketNumber(5, Width1, 300)
	if got != 261 {
		t.Fatalf("nearest decode = %d, want 261", got)
	}
}

func TestOutOfOrderDelivery(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	r, err := New(Width2, Key("k0"), auth)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Deliver 10, then a delayed 8, then 11; all must reassemble and accept.
	mustReceive(t, r, frame(t, Width2, 0, 10, "p10", auth), 10, 0)
	mustReceive(t, r, frame(t, Width2, 0, 8, "p8", auth), 8, 0)
	mustReceive(t, r, frame(t, Width2, 0, 11, "p11", auth), 11, 0)

	highest, ok := r.HighestPacketNumber()
	if !ok || highest != 11 {
		t.Fatalf("highest = (%d,%v), want (11,true)", highest, ok)
	}
}

func TestReplayRejectedWithoutAuthenticating(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	r, _ := New(Width2, Key("k0"), auth)

	first := frame(t, Width2, 0, 7, "once", auth)
	mustReceive(t, r, first, 7, 0)
	if _, err := r.Receive(first); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate: want ErrReplay, got %v", err)
	}
	if auth.callCount() != 1 {
		t.Fatalf("authenticator called %d times, replay must not re-authenticate", auth.callCount())
	}
}

func TestBadTagDoesNotAdvanceState(t *testing.T) {
	auth := newControlledAuth([]byte("k0"), []byte("k1"), []byte("k2"))
	r, _ := New(Width2, Key("k0"), auth)
	mustReceive(t, r, frame(t, Width2, 0, 3, "ok", auth), 3, 0)

	// Forged new-phase packet whose tag fails: generation and highest stay put.
	r.RotateKey(Key("k1"))
	auth.failNext(1)
	forged := frame(t, Width2, 1, 4, "forged", auth)
	if _, err := r.Receive(forged); err == nil {
		t.Fatal("forged packet must fail authentication")
	}
	if r.Generation() != 0 {
		t.Fatalf("generation advanced to %d after bad tag", r.Generation())
	}
	if highest, _ := r.HighestPacketNumber(); highest != 3 {
		t.Fatalf("highest moved to %d after bad tag", highest)
	}

	// The failed attempt leaves the pending key in place; the same switch now
	// succeeds once with a valid tag.
	mustReceive(t, r, forged, 4, 1)
	if r.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", r.Generation())
	}

	// A second forged switch to generation 2 must likewise be blocked until its
	// tag is valid, proving bad tags never drive any phase advance.
	r.RotateKey(Key("k2"))
	auth.failNext(1)
	forged2 := frame(t, Width2, 2, 5, "forged2", auth)
	if _, err := r.Receive(forged2); err == nil {
		t.Fatal("second forged packet must fail authentication")
	}
	if r.Generation() != 1 {
		t.Fatalf("generation advanced to %d after second bad tag", r.Generation())
	}
	r.RotateKey(Key("k2"))
	mustReceive(t, r, forged2, 5, 2)
}

func TestWrapAroundOneByte(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	r, _ := New(Width1, Key("k0"), auth)

	// Walk 0..255, then wrap: truncated 0 decodes to full 256.
	for n := uint64(0); n < 256; n++ {
		mustReceive(t, r, frame(t, Width1, 0, n, fmt.Sprintf("n%d", n), auth), n, 0)
	}
	mustReceive(t, r, frame(t, Width1, 0, 256, "wrap", auth), 256, 0)
	mustReceive(t, r, frame(t, Width1, 0, 300, "later", auth), 300, 0)

	highest, _ := r.HighestPacketNumber()
	if highest != 300 {
		t.Fatalf("highest = %d, want 300", highest)
	}
}

func TestKeySwitchOldGenerationWindow(t *testing.T) {
	auth := newControlledAuth([]byte("k0"), []byte("k1"))
	r, _ := New(Width1, Key("k0"), auth, WithReplayWindow(256))

	// Generation 0 runs below 200, then a single authenticated switch makes
	// gen 1 current (phase bit 1) and retains gen 0 as the previous key.
	for n := uint64(0); n < 200; n++ {
		mustReceive(t, r, frame(t, Width1, 0, n, "g0", auth), n, 0)
	}
	r.RotateKey(Key("k1"))
	mustReceive(t, r, frame(t, Width1, 1, 200, "g1", auth), 200, 1)

	// A late gen-0 number inside the single window before boundary 200 is fine.
	mustReceive(t, r, frame(t, Width1, 0, 199, "late", auth), 199, 0)

	// Truncated 0 decodes to full 256 (past the boundary) against expected
	// 201 and was never authenticated, so only the boundary check rejects it.
	if _, err := r.Receive(frame(t, Width1, 0, 256, "after-boundary", auth)); !errors.Is(err, ErrBeforeSwitch) {
		t.Fatalf("boundary packet: want ErrBeforeSwitch, got %v", err)
	}
	// A number one full truncation window before the decodable range is also
	// outside the single allowed window and must be refused under the old key.
	if _, err := r.Receive(frame(t, Width1, 0, 0, "two-wrap", auth)); !errors.Is(err, ErrBeforeSwitch) {
		t.Fatalf("two-window old packet: want ErrBeforeSwitch, got %v", err)
	}
}

func TestSamePhaseTwoWrapsCannotMixOldKey(t *testing.T) {
	auth := newControlledAuth([]byte("k0"), []byte("k1"))
	r, _ := New(Width1, Key("k0"), auth, WithReplayWindow(256))

	// After the switch to gen 1 (phase bit 1), gen 0's retained key must only
	// cover the single truncation window before boundary 200. A packet whose
	// truncated number reappears a full window earlier cannot use that old key.
	for n := uint64(0); n < 200; n++ {
		mustReceive(t, r, frame(t, Width1, 0, n, "g0", auth), n, 0)
	}
	r.RotateKey(Key("k1"))
	mustReceive(t, r, frame(t, Width1, 1, 200, "switch", auth), 200, 1)
	replay := frame(t, Width1, 0, 0, "replay", auth)
	if _, err := r.Receive(replay); !errors.Is(err, ErrBeforeSwitch) {
		t.Fatalf("same-phase two-wrap replay: want ErrBeforeSwitch, got %v", err)
	}
}

func TestRetirePreviousRejectsOldPhase(t *testing.T) {
	auth := newControlledAuth([]byte("k0"), []byte("k1"))
	r, _ := New(Width1, Key("k0"), auth)
	for n := uint64(0); n < 10; n++ {
		mustReceive(t, r, frame(t, Width1, 0, n, "g0", auth), n, 0)
	}
	r.RotateKey(Key("k1"))
	mustReceive(t, r, frame(t, Width1, 1, 10, "switch", auth), 10, 1)

	r.RetirePrevious()
	// With the previous key gone and no pending next key, the old phase bit
	// resolves to no known generation.
	if _, err := r.Receive(frame(t, Width1, 0, 9, "late", auth)); !errors.Is(err, ErrUnknownPhase) {
		t.Fatalf("retired old phase: want ErrUnknownPhase, got %v", err)
	}
}

func TestConcurrentDuplicatesAcceptedOnce(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	gate := make(chan struct{})
	auth.gate = gate
	r, _ := New(Width2, Key("k0"), auth)

	pkt := frame(t, Width2, 0, 42, "dup", auth)
	const n = 32
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = r.Receive(pkt)
		}(i)
	}
	// Give every goroutine time to pile up on the receiver lock, then release.
	close(gate)
	wg.Wait()

	ok, replays := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrReplay):
			replays++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || replays != n-1 {
		t.Fatalf("concurrent duplicates: %d ok, %d replays, want 1 ok and %d replays",
			ok, replays, n-1)
	}
	if auth.callCount() != 1 {
		t.Fatalf("authenticator called %d times, want exactly 1", auth.callCount())
	}
}

func TestConcurrentReceiversNoDataRace(t *testing.T) {
	auth := newControlledAuth([]byte("k0"))
	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	// Each goroutine owns an independent receiver over a disjoint number band,
	// so the race detector exercises concurrent packet processing without any
	// cross-goroutine replay-window interaction.
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			r, err := New(Width4, Key("k0"), auth, WithReplayWindow(1024))
			if err != nil {
				t.Errorf("New: %v", err)
				return
			}
			for i := 0; i < perWorker; i++ {
				num := uint64(base*perWorker + i)
				if _, err := r.Receive(frame(t, Width4, 0, num, "x", auth)); err != nil {
					t.Errorf("number %d: %v", num, err)
				}
			}
		}(worker)
	}
	wg.Wait()
	if got := auth.callCount(); got != workers*perWorker {
		t.Fatalf("authenticator called %d times, want %d", got, workers*perWorker)
	}
}

func TestWireRoundTrip(t *testing.T) {
	for _, width := range []byte{Width1, Width2, Width3, Width4} {
		phase := WirePhaseBit
		max := (uint64(1) << (width * 8)) - 1
		header, err := BuildHeader(width, phase, max)
		if err != nil {
			t.Fatalf("BuildHeader width %d: %v", width, err)
		}
		gotPhase, gotNum := ParseHeader(header)
		if gotPhase != phase || gotNum != max {
			t.Fatalf("width %d round trip = (%d,%d), want (%d,%d)",
				width, gotPhase, gotNum, phase, max)
		}
	}
}
