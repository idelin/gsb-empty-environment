package antireplay

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// referenceSelect is an intentionally slow, independent implementation of the
// full-sequence choice. It enumerates candidates over a range that is wide
// enough to contain every candidate that can be nearest to highest+1.
func referenceSelect(highest, truncated uint64, fullBits, truncBits uint) (uint64, error) {
	if fullBits >= 63 {
		panic("referenceSelect is only for small exhaustive tests")
	}
	modulus := int64(uint64(1) << truncBits)
	maxFull := int64((uint64(1) << fullBits) - 1)
	target := int64(highest) + 1 // fullBits is small in these tests
	low := int64(truncated) & (modulus - 1)

	var best int64
	bestDistance := int64(math.MaxInt64)
	found := false
	// Candidate values are low+k*modulus. The useful k values surround target.
	for k := int64(-4); k <= int64(maxFull/modulus)+5; k++ {
		candidate := low + k*modulus
		distance := candidate - target
		if distance < 0 {
			distance = -distance
		}
		if !found || distance < bestDistance || (distance == bestDistance && candidate < best) {
			best = candidate
			bestDistance = distance
			found = true
		}
	}
	if best < 0 {
		return 0, ErrUnderflow
	}
	if best > maxFull {
		return 0, ErrOverflow
	}
	return uint64(best), nil
}

func TestReconstructExhaustiveSmallBits(t *testing.T) {
	for _, fullBits := range []uint{5, 6, 7, 8} {
		maxFull := (uint64(1) << fullBits) - 1
		for _, truncBits := range []uint{2, 3, 4} {
			if truncBits > fullBits {
				continue
			}
			modulus := uint64(1) << truncBits
			for highest := uint64(0); highest <= maxFull; highest++ {
				w, err := newWindow(fullBits, truncBits, 1, highest, false)
				if err != nil {
					t.Fatal(err)
				}
				for truncated := uint64(0); truncated < modulus; truncated++ {
					got, gotErr := w.reconstructLocked(truncated)
					want, wantErr := referenceSelect(highest, truncated, fullBits, truncBits)
					if (gotErr == nil) != (wantErr == nil) || got != want {
						t.Fatalf("full=%d trunc=%d highest=%d low=%d: got (%d,%v), want (%d,%v)",
							fullBits, truncBits, highest, truncated, got, gotErr, want, wantErr)
					}
					if gotErr != nil && !errors.Is(gotErr, wantErr) {
						t.Fatalf("full=%d trunc=%d highest=%d low=%d: got error %v, want %v",
							fullBits, truncBits, highest, truncated, gotErr, wantErr)
					}
				}
			}
		}
	}
}

type referenceWindow struct {
	fullBits  uint
	truncBits uint
	width     uint
	maxFull   uint64
	highest   uint64
	seen      map[uint64]bool
}

func newReferenceWindow(fullBits, truncBits, width uint) *referenceWindow {
	maxFull := ^uint64(0)
	if fullBits < 64 {
		maxFull = (uint64(1) << fullBits) - 1
	}
	return &referenceWindow{
		fullBits:  fullBits,
		truncBits: truncBits,
		width:     width,
		maxFull:   maxFull,
		seen:      make(map[uint64]bool),
	}
}

func (r *referenceWindow) accept(truncated uint64) (uint64, error) {
	seq, err := referenceSelect(r.highest, truncated, r.fullBits, r.truncBits)
	if err != nil {
		return 0, err
	}
	if seq > r.highest {
		r.highest = seq
		for seen := range r.seen {
			if seen <= r.highest && r.highest-seen >= uint64(r.width) {
				delete(r.seen, seen)
			}
		}
	}
	if seq <= r.highest && r.highest-seq >= uint64(r.width) {
		return 0, ErrTooOld
	}
	if r.seen[seq] {
		return 0, ErrReplay
	}
	if seq > r.maxFull {
		return 0, ErrOverflow
	}
	r.seen[seq] = true
	return seq, nil
}

func TestWindowBehaviourAgainstExhaustiveReference(t *testing.T) {
	const (
		fullBits  = 6
		truncBits = 2
		length    = 5
	)
	modulus := 1 << truncBits
	for _, width := range []uint{1, 2, 3} {
		total := 1
		for i := 0; i < length; i++ {
			total *= modulus
		}
		for encoded := 0; encoded < total; encoded++ {
			sequence := make([]uint64, length)
			n := encoded
			for i := 0; i < length; i++ {
				sequence[i] = uint64(n % modulus)
				n /= modulus
			}

			w, err := newWindow(fullBits, truncBits, int(width), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			ref := newReferenceWindow(fullBits, truncBits, width)

			for step, truncated := range sequence {
				got, gotErr := w.accept(truncated, func(uint64) error { return nil })
				want, wantErr := ref.accept(truncated)
				if got != want || (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("width=%d seq=%v step=%d low=%d: got (%d,%v), want (%d,%v)",
						width, sequence, step, truncated, got, gotErr, want, wantErr)
				}
				if gotErr != nil && !errors.Is(gotErr, wantErr) {
					t.Fatalf("width=%d seq=%v step=%d low=%d: got error %v, want %v",
						width, sequence, step, truncated, gotErr, wantErr)
				}
				if w.Highest() != ref.highest {
					t.Fatalf("width=%d seq=%v step=%d low=%d: highest=%d, want %d",
						width, sequence, step, truncated, w.Highest(), ref.highest)
				}
			}
		}
	}
}

func TestWrapAroundHighBits(t *testing.T) {
	w, err := NewWithHighest(64, math.MaxUint32-1)
	if err != nil {
		t.Fatal(err)
	}

	seq, err := w.Accept(math.MaxUint32, func(uint64) error { return nil })
	if err != nil || seq != math.MaxUint32 {
		t.Fatalf("first packet: got (%d,%v), want (%d,nil)", seq, err, uint64(math.MaxUint32))
	}

	// The low 32 bits wrap to zero while the full sequence grows into bit 32.
	seq, err = w.Accept(0, func(uint64) error { return nil })
	if err != nil || seq != math.MaxUint32+1 {
		t.Fatalf("wrapped packet: got (%d,%v), want (%d,nil)", seq, err, uint64(math.MaxUint32)+1)
	}
	if got := w.Highest(); got != math.MaxUint32+1 {
		t.Fatalf("highest=%d, want %d", got, uint64(math.MaxUint32)+1)
	}
}

func TestTieChoosesSmallerCandidate(t *testing.T) {
	// target is 0x1_0000_0001 and the received low bits are exactly half a
	// truncated range above its low half. The two candidates are equidistant.
	w, err := newWindow(64, 32, 16, uint64(1)<<32, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.reconstructLocked(uint64(1)<<31 + 1)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(1)<<31 + 1
	if got != want {
		t.Fatalf("tie picked %#x, want smaller candidate %#x", got, want)
	}
}

func TestWindowLeftEdgeAndVeryOldPacket(t *testing.T) {
	const width = 8
	w, err := NewWithHighest(width, 100)
	if err != nil {
		t.Fatal(err)
	}

	// NewWithHighest marks 100 as seen. The window is [93, 100].
	seq, err := w.Accept(93, func(seq uint64) error {
		if seq != 93 {
			return fmt.Errorf("verifier saw %d", seq)
		}
		return nil
	})
	if err != nil || seq != 93 {
		t.Fatalf("left edge: got (%d,%v), want (93,nil)", seq, err)
	}
	if _, err := w.Accept(93, func(uint64) error { return nil }); !errors.Is(err, ErrReplay) {
		t.Fatalf("left-edge replay: got %v, want ErrReplay", err)
	}
	if _, err := w.Accept(92, func(uint64) error { return nil }); !errors.Is(err, ErrTooOld) {
		t.Fatalf("left edge minus one: got %v, want ErrTooOld", err)
	}

	// A large forward jump clears stale ring slots. The new left edge must be
	// usable, while both the old baseline and one-before-left must not be.
	if seq, err := w.Accept(200, func(uint64) error { return nil }); err != nil || seq != 200 {
		t.Fatalf("forward jump: got (%d,%v), want (200,nil)", seq, err)
	}
	if seq, err := w.Accept(193, func(uint64) error { return nil }); err != nil || seq != 193 {
		t.Fatalf("new left edge: got (%d,%v), want (193,nil)", seq, err)
	}
	for _, old := range []uint64{192, 100} {
		if _, err := w.Accept(uint32(old), func(uint64) error { return nil }); !errors.Is(err, ErrTooOld) {
			t.Fatalf("old sequence %d: got %v, want ErrTooOld", old, err)
		}
	}

	veryOld, err := NewWithHighest(64, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := veryOld.Accept(uint32(500_000), func(uint64) error { return nil }); !errors.Is(err, ErrTooOld) {
		t.Fatalf("very old packet: got %v, want ErrTooOld", err)
	}
	if got := veryOld.Highest(); got != 1_000_000 {
		t.Fatalf("old packet moved highest to %d", got)
	}
}

func TestVerificationFailureDoesNotMoveWindow(t *testing.T) {
	verifyErr := errors.New("bad signature")
	w, err := NewWithHighest(64, 1000)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Accept(2000, func(uint64) error { return verifyErr }); !errors.Is(err, ErrVerification) || !errors.Is(err, verifyErr) {
		t.Fatalf("got %v, want verification error wrapping %v", err, verifyErr)
	}
	if got := w.Highest(); got != 1000 {
		t.Fatalf("failed verification moved highest to %d", got)
	}

	// A failed attempt must not reserve or mark the sequence number either.
	seq, err := w.Accept(2000, func(uint64) error { return nil })
	if err != nil || seq != 2000 {
		t.Fatalf("retry after failure: got (%d,%v), want (2000,nil)", seq, err)
	}
	if got := w.Highest(); got != 2000 {
		t.Fatalf("highest=%d, want 2000", got)
	}
}

func TestConcurrentVerificationDeliversSameSequenceOnce(t *testing.T) {
	const (
		highest  = 1000
		low      = 1050
		workers  = 32
		expected = 1050
	)
	w, err := NewWithHighest(128, highest)
	if err != nil {
		t.Fatal(err)
	}

	var started sync.WaitGroup
	started.Add(workers)
	release := make(chan struct{})

	type result struct {
		seq uint64
		err error
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func() {
			seq, err := w.Accept(low, func(seq uint64) error {
				if seq != expected {
					return fmt.Errorf("verifier saw %d", seq)
				}
				started.Done()
				<-release
				return nil
			})
			results <- result{seq: seq, err: err}
		}()
	}
	started.Wait()
	close(release)

	var accepted int
	for i := 0; i < workers; i++ {
		res := <-results
		if res.err == nil {
			accepted++
			if res.seq != expected {
				t.Fatalf("accepted seq=%d, want %d", res.seq, expected)
			}
		} else if !errors.Is(res.err, ErrReplay) {
			t.Fatalf("unexpected error: %v", res.err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d times, want exactly 1", accepted)
	}
	if got := w.Highest(); got != expected {
		t.Fatalf("highest=%d, want %d", got, expected)
	}

	verifyCalled := false
	if _, err := w.Accept(low, func(uint64) error {
		verifyCalled = true
		return nil
	}); !errors.Is(err, ErrReplay) {
		t.Fatalf("sequential replay: got %v, want ErrReplay", err)
	}
	if verifyCalled {
		t.Fatal("verifier was called for an already accepted sequence")
	}
}

func TestTwoCandidatesCompleteSimultaneously(t *testing.T) {
	const highest = 1000
	w, err := NewWithHighest(64, highest)
	if err != nil {
		t.Fatal(err)
	}

	var started sync.WaitGroup
	started.Add(2)
	release := make(chan struct{})
	type result struct {
		seq uint64
		err error
	}
	results := make(chan result, 2)

	for _, seq := range []uint64{1005, 1010} {
		seq := seq
		go func() {
			got, err := w.Accept(uint32(seq), func(full uint64) error {
				if full != seq {
					return fmt.Errorf("verifier saw %d, want %d", full, seq)
				}
				started.Done()
				<-release
				return nil
			})
			results <- result{seq: got, err: err}
		}()
	}
	started.Wait()
	close(release)

	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent packet rejected: %v", res.err)
		}
	}
	if got := w.Highest(); got != 1010 {
		t.Fatalf("highest=%d, want 1010", got)
	}
	if _, err := w.Accept(1005, func(uint64) error { return nil }); !errors.Is(err, ErrReplay) {
		t.Fatalf("1005 replay: got %v, want ErrReplay", err)
	}
	if _, err := w.Accept(1010, func(uint64) error { return nil }); !errors.Is(err, ErrReplay) {
		t.Fatalf("1010 replay: got %v, want ErrReplay", err)
	}
}

func TestCommitRechecksAfterConcurrentAdvance(t *testing.T) {
	w, err := NewWithHighest(64, 1000)
	if err != nil {
		t.Fatal(err)
	}

	verifyStarted := make(chan struct{})
	verifyRelease := make(chan struct{})
	lowResult := make(chan error, 1)
	go func() {
		_, err := w.Accept(1005, func(uint64) error {
			close(verifyStarted)
			<-verifyRelease
			return nil
		})
		lowResult <- err
	}()
	<-verifyStarted

	// Advance the window far beyond the packet being verified.
	if _, err := w.Accept(2000, func(uint64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	close(verifyRelease)
	if err := <-lowResult; !errors.Is(err, ErrTooOld) {
		t.Fatalf("late commit: got %v, want ErrTooOld", err)
	}
	if got := w.Highest(); got != 2000 {
		t.Fatalf("highest=%d, want 2000", got)
	}
}

func TestOverflowAndUnderflow(t *testing.T) {
	w, err := NewWithHighest(64, math.MaxUint64-2)
	if err != nil {
		t.Fatal(err)
	}
	maxU := uint64(math.MaxUint64)
	if seq, err := w.Accept(uint32(maxU-1), func(uint64) error { return nil }); err != nil || seq != maxU-1 {
		t.Fatalf("max-1: got (%d,%v)", seq, err)
	}
	if seq, err := w.Accept(uint32(maxU), func(uint64) error { return nil }); err != nil || seq != maxU {
		t.Fatalf("max: got (%d,%v)", seq, err)
	}
	if _, err := w.Accept(0, func(uint64) error { return nil }); !errors.Is(err, ErrOverflow) {
		t.Fatalf("past max: got %v, want ErrOverflow", err)
	}
	if got := w.Highest(); got != math.MaxUint64 {
		t.Fatalf("overflow moved highest to %d", got)
	}

	// With highest zero, target is one. The half-tie candidate below zero is
	// smaller than the equidistant positive candidate, so it underflows.
	fresh, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Accept(1<<31+1, func(uint64) error { return nil }); !errors.Is(err, ErrUnderflow) {
		t.Fatalf("underflow tie: got %v, want ErrUnderflow", err)
	}
}

func TestConstructorValidation(t *testing.T) {
	for _, width := range []int{-1, 0, 1 << 31} {
		if _, err := New(width); !errors.Is(err, ErrInvalidWindow) {
			t.Fatalf("New(%d): got %v, want ErrInvalidWindow", width, err)
		}
	}
	if _, err := newWindow(8, 4, 8, 256, false); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("out-of-range highest: got %v, want ErrInvalidWindow", err)
	}
	if _, err := newWindow(4, 8, 8, 0, false); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("truncated width larger than full width: got %v, want ErrInvalidWindow", err)
	}

	w, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Accept(1, nil); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("nil verifier: got %v, want ErrNilVerifier", err)
	}

	var zero Window
	if _, err := zero.Accept(1, func(uint64) error { return nil }); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("zero-value window: got %v, want ErrInvalidWindow", err)
	}
}
