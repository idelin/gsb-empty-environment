package netsec

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustPIV(t *testing.T, n uint64) []byte {
	t.Helper()
	b, err := EncodePIV(n)
	if err != nil {
		t.Fatalf("EncodePIV(%d): %v", n, err)
	}
	return b
}

func establishWindow(t *testing.T, g *Guard, seq uint64) []byte {
	t.Helper()
	piv := mustPIV(t, seq)
	first := g.Check(piv, nil)
	if first.Status != StatusEchoRequired {
		t.Fatalf("first check for PIV %d = %v, want Echo required", seq, first.Status)
	}
	if len(first.Echo) == 0 || len(first.Echo) > MaxEchoBytes {
		t.Fatalf("challenge length = %d, want 1..%d", len(first.Echo), MaxEchoBytes)
	}
	second := g.Check(piv, first.Echo)
	if second.Status != StatusAccepted {
		t.Fatalf("Echo-authenticated check for PIV %d = %v (%v), want accepted", seq, second.Status, second.Err)
	}
	return first.Echo
}

func check(t *testing.T, g *Guard, seq uint64) Decision {
	t.Helper()
	return g.Check(mustPIV(t, seq), nil)
}

func TestPIVEncoding(t *testing.T) {
	tests := []struct {
		name string
		n    uint64
		raw  []byte
	}{
		{"zero", 0, []byte{0x00}},
		{"one byte", 0x2a, []byte{0x2a}},
		{"three bytes", 0x010203, []byte{0x01, 0x02, 0x03}},
		{"maximum", MaxPIVNumber, []byte{0xff, 0xff, 0xff, 0xff, 0xff}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := EncodePIV(tc.n)
			if err != nil {
				t.Fatalf("EncodePIV: %v", err)
			}
			if !bytes.Equal(encoded, tc.raw) {
				t.Fatalf("EncodePIV(%d) = %x, want %x", tc.n, encoded, tc.raw)
			}
			decoded, err := DecodePIV(tc.raw)
			if err != nil {
				t.Fatalf("DecodePIV: %v", err)
			}
			if decoded != tc.n {
				t.Fatalf("DecodePIV(%x) = %d, want %d", tc.raw, decoded, tc.n)
			}
		})
	}

	if _, err := DecodePIV(nil); !errors.Is(err, ErrPIVMissing) {
		t.Fatalf("DecodePIV(empty) error = %v, want ErrPIVMissing", err)
	}
	if _, err := DecodePIV(make([]byte, 6)); !errors.Is(err, ErrPIVTooLong) {
		t.Fatalf("DecodePIV(6 bytes) error = %v, want ErrPIVTooLong", err)
	}
	if _, err := EncodePIV(MaxPIVNumber + 1); !errors.Is(err, ErrPIVOutOfRange) {
		t.Fatalf("EncodePIV(2^40) error = %v, want ErrPIVOutOfRange", err)
	}
}

func TestReplayRejected(t *testing.T) {
	g := NewGuard()
	establishWindow(t, g, 10)

	if d := check(t, g, 10); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay of PIV 10 = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}

	if d := check(t, g, 12); d.Status != StatusAccepted {
		t.Fatalf("PIV 12 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 11); d.Status != StatusAccepted {
		t.Fatalf("PIV 11 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 12); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay of PIV 12 = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}
	if d := check(t, g, 11); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay of PIV 11 = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}
}

func TestWindowBoundaries(t *testing.T) {
	g := NewGuard()
	establishWindow(t, g, 0)
	// Jumping to 100 establishes the ordinary sliding window [69, 100].
	if d := check(t, g, 100); d.Status != StatusAccepted {
		t.Fatalf("PIV 100 = %v (%v), want accepted", d.Status, d.Err)
	}

	// PIV 69 is the last acceptable older value; PIV 68 has fallen off the
	// window.
	if d := check(t, g, 69); d.Status != StatusAccepted {
		t.Fatalf("PIV 100-31 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 68); d.Status != StatusRejected || !errors.Is(d.Err, ErrBelowWindow) {
		t.Fatalf("PIV 100-32 = %v (%v), want rejected/ErrBelowWindow", d.Status, d.Err)
	}

	// An in-window value that was already seen is a replay.
	if d := check(t, g, 100); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay of highest PIV 100 = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}

	// Jumping ahead moves the window. 100 is now exactly 32 below the new
	// highest and must be rejected even though it was recorded previously.
	if d := check(t, g, 132); d.Status != StatusAccepted {
		t.Fatalf("PIV 132 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 100); d.Status != StatusRejected || !errors.Is(d.Err, ErrBelowWindow) {
		t.Fatalf("PIV 132-32 = %v (%v), want rejected/ErrBelowWindow", d.Status, d.Err)
	}
	if d := check(t, g, 101); d.Status != StatusAccepted {
		t.Fatalf("PIV 132-31 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 101); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay of PIV 101 = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}
}

func TestWindowJumpClearsOldBits(t *testing.T) {
	g := NewGuard()
	establishWindow(t, g, 5)
	for _, seq := range []uint64{6, 7, 8} {
		if d := check(t, g, seq); d.Status != StatusAccepted {
			t.Fatalf("PIV %d = %v (%v), want accepted", seq, d.Status, d.Err)
		}
	}

	// A jump of at least the window width discards all previous entries.
	if d := check(t, g, 40); d.Status != StatusAccepted {
		t.Fatalf("PIV 40 = %v (%v), want accepted", d.Status, d.Err)
	}
	if d := check(t, g, 8); d.Status != StatusRejected || !errors.Is(d.Err, ErrBelowWindow) {
		t.Fatalf("old PIV 8 after jump = %v (%v), want rejected/ErrBelowWindow", d.Status, d.Err)
	}
	if d := check(t, g, 39); d.Status != StatusAccepted {
		t.Fatalf("PIV 39 inside new window = %v (%v), want accepted", d.Status, d.Err)
	}
}

func TestRestartRequiresEchoAndRejectsReplay(t *testing.T) {
	before := NewGuard()
	staleEcho := establishWindow(t, before, 100)
	if d := check(t, before, 101); d.Status != StatusAccepted {
		t.Fatalf("pre-restart PIV 101 = %v (%v), want accepted", d.Status, d.Err)
	}

	// A new guard models a restarted server whose replay window was lost.
	after := NewGuard()
	replayed := mustPIV(t, 100)

	noEcho := after.Check(replayed, nil)
	if noEcho.Status != StatusEchoRequired || !errors.Is(noEcho.Err, ErrEchoRequired) {
		t.Fatalf("post-restart replay without Echo = %v (%v), want Echo required", noEcho.Status, noEcho.Err)
	}
	if !after.WindowUnknown() {
		t.Fatal("window became known without an Echo proof")
	}

	// Reusing an Echo value captured before the restart must not work.
	stale := after.Check(replayed, staleEcho)
	if stale.Status != StatusEchoRequired || !errors.Is(stale.Err, ErrEchoInvalid) {
		t.Fatalf("post-restart replay with stale Echo = %v (%v), want Echo required/invalid", stale.Status, stale.Err)
	}
	if !bytes.Equal(stale.Echo, noEcho.Echo) {
		t.Fatal("a failed Echo check changed the current challenge")
	}
	if !after.WindowUnknown() {
		t.Fatal("window became known after an invalid Echo proof")
	}

	// A client that returns the current protected challenge re-establishes
	// freshness; the captured request without it would never reach this point.
	fresh := after.Check(replayed, noEcho.Echo)
	if fresh.Status != StatusAccepted {
		t.Fatalf("post-restart replay with fresh Echo = %v (%v), want accepted", fresh.Status, fresh.Err)
	}
	if after.WindowUnknown() {
		t.Fatal("window remains unknown after a valid Echo proof")
	}
	if low, ok := after.LowerLimit(); !ok || low != 100 {
		t.Fatalf("post-restart lower limit = %d (known=%v), want 100", low, ok)
	}
	if d := after.Check(replayed, noEcho.Echo); d.Status != StatusRejected || !errors.Is(d.Err, ErrReplay) {
		t.Fatalf("replay after re-established window = %v (%v), want rejected/ErrReplay", d.Status, d.Err)
	}
	// PIV 99 is numerically inside a 32-wide window, but it is older than the
	// fresh Echo request and may have been processed before the restart.
	if d := check(t, after, 99); d.Status != StatusRejected || !errors.Is(d.Err, ErrBelowWindow) {
		t.Fatalf("pre-restart PIV 99 after restart baseline = %v (%v), want rejected/ErrBelowWindow", d.Status, d.Err)
	}
	if d := check(t, after, 68); d.Status != StatusRejected || !errors.Is(d.Err, ErrBelowWindow) {
		t.Fatalf("older PIV after restart baseline = %v (%v), want rejected/ErrBelowWindow", d.Status, d.Err)
	}
	if d := check(t, after, 101); d.Status != StatusAccepted {
		t.Fatalf("fresh PIV above restart baseline = %v (%v), want accepted", d.Status, d.Err)
	}
}

func TestEchoChallengeIsStableAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	g := NewGuard(
		WithClock(func() time.Time { return now }),
		WithEchoTTL(10*time.Second),
		WithEchoReader(bytes.NewReader([]byte{
			0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
			16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
		})),
	)
	piv := mustPIV(t, 7)
	first := g.Check(piv, nil)
	if first.Status != StatusEchoRequired {
		t.Fatalf("first check = %v, want Echo required", first.Status)
	}
	second := g.Check(piv, nil)
	if !bytes.Equal(first.Echo, second.Echo) {
		t.Fatal("challenge changed before it expired or was used")
	}

	now = now.Add(11 * time.Second)
	expired := g.Check(piv, first.Echo)
	if expired.Status != StatusEchoRequired || !errors.Is(expired.Err, ErrEchoInvalid) {
		t.Fatalf("expired Echo = %v (%v), want Echo required/invalid", expired.Status, expired.Err)
	}
	if bytes.Equal(expired.Echo, first.Echo) {
		t.Fatal("expired challenge was reused")
	}
	if d := g.Check(piv, expired.Echo); d.Status != StatusAccepted {
		t.Fatalf("new Echo = %v (%v), want accepted", d.Status, d.Err)
	}
}

func TestSequenceExhaustion(t *testing.T) {
	g := NewGuard()
	last := MaxPIVNumber - 1
	establishWindow(t, g, last)

	// The final PIV is still processed, then the context is disabled.
	final := check(t, g, MaxPIVNumber)
	if final.Status != StatusAccepted {
		t.Fatalf("final PIV = %v (%v), want accepted", final.Status, final.Err)
	}
	if !g.Exhausted() {
		t.Fatal("guard not exhausted after consuming the final PIV")
	}

	if d := check(t, g, MaxPIVNumber); d.Status != StatusRejected || !errors.Is(d.Err, ErrSequenceExhausted) {
		t.Fatalf("replay of final PIV = %v (%v), want rejected/ErrSequenceExhausted", d.Status, d.Err)
	}
	// Even an unseen value inside the old window is refused once the context
	// is disabled.
	if d := check(t, g, last-1); d.Status != StatusRejected || !errors.Is(d.Err, ErrSequenceExhausted) {
		t.Fatalf("in-window PIV after exhaustion = %v (%v), want rejected/ErrSequenceExhausted", d.Status, d.Err)
	}

	// A valid Echo proof cannot revive an exhausted context.
	challenge := g.Check(mustPIV(t, MaxPIVNumber), nil)
	if challenge.Status != StatusRejected || !errors.Is(challenge.Err, ErrSequenceExhausted) {
		t.Fatalf("exhausted check = %v (%v), want rejected/ErrSequenceExhausted", challenge.Status, challenge.Err)
	}
}

func TestConfiguredMaximumExhaustion(t *testing.T) {
	g := NewGuard(WithMaxPIV(5))
	establishWindow(t, g, 4)

	if d := check(t, g, 6); d.Status != StatusRejected || !errors.Is(d.Err, ErrSequenceExhausted) {
		t.Fatalf("PIV 6 above configured max = %v (%v), want rejected/ErrSequenceExhausted", d.Status, d.Err)
	}
	if !g.Exhausted() {
		t.Fatal("guard not exhausted after out-of-range PIV")
	}
	if d := check(t, g, 5); d.Status != StatusRejected || !errors.Is(d.Err, ErrSequenceExhausted) {
		t.Fatalf("PIV 5 after disabled = %v (%v), want rejected/ErrSequenceExhausted", d.Status, d.Err)
	}
}

func TestInvalidInputRejectedWithoutEcho(t *testing.T) {
	g := NewGuard()
	tooLong := []byte{1, 2, 3, 4, 5, 6}
	if d := g.Check(tooLong, nil); d.Status != StatusRejected || !errors.Is(d.Err, ErrPIVTooLong) {
		t.Fatalf("6-byte PIV = %v (%v), want rejected/ErrPIVTooLong", d.Status, d.Err)
	}
	if !g.WindowUnknown() {
		t.Fatal("invalid PIV established a window")
	}

	piv := mustPIV(t, 1)
	if d := g.Check(piv, make([]byte, MaxEchoBytes+1)); d.Status != StatusEchoRequired {
		t.Fatalf("oversized Echo = %v (%v), want Echo required", d.Status, d.Err)
	}
}

func TestConcurrentDuplicateIsAcceptedOnce(t *testing.T) {
	g := NewGuard()
	establishWindow(t, g, 0)

	const workers = 64
	piv := mustPIV(t, 1)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if g.Check(piv, nil).Accepted() {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted %d concurrent copies of one PIV, want 1", got)
	}
}

func TestForgetWindowRequiresEchoAgain(t *testing.T) {
	g := NewGuard()
	establishWindow(t, g, 50)
	if d := check(t, g, 51); d.Status != StatusAccepted {
		t.Fatalf("PIV 51 = %v (%v), want accepted", d.Status, d.Err)
	}
	g.ForgetWindow()

	if !g.WindowUnknown() {
		t.Fatal("ForgetWindow did not clear the replay window")
	}
	if d := check(t, g, 52); d.Status != StatusEchoRequired {
		t.Fatalf("PIV 52 after ForgetWindow = %v (%v), want Echo required", d.Status, d.Err)
	}
}
