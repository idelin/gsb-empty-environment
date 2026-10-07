package ringloan

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

// patternAt is the deterministic byte sequence used as the model:
// the i-th byte ever written by the producer.
func patternAt(i int) byte { return byte(i*31 + i>>3) }

func pattern(off, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = patternAt(off + i)
	}
	return p
}

func join(first, second []byte) []byte {
	out := make([]byte, 0, len(first)+len(second))
	return append(append(out, first...), second...)
}

// TestWrapAroundLoan verifies that a loan crossing the ring tail is
// exposed as two segments whose concatenation matches the byte
// sequence model.
func TestWrapAroundLoan(t *testing.T) {
	b := New(16)

	// Write 12 bytes, lend and release 8 so head moves to offset 8.
	if !b.TryWrite(pattern(0, 12)) {
		t.Fatal("initial write failed")
	}
	l, ok := b.Lend(8)
	if !ok {
		t.Fatal("lend failed")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}

	// Write 10 more: tail wraps (offsets 12..15 then 0..5).
	if !b.TryWrite(pattern(12, 10)) {
		t.Fatal("wrap write failed")
	}

	// Lend all 14 readable bytes: [8..16) + [0..6), crosses the tail.
	l, ok = b.Lend(14)
	if !ok {
		t.Fatal("wrap lend failed")
	}
	first, second := l.Segments()
	if len(second) == 0 {
		t.Fatal("expected a two-segment loan across the ring tail")
	}
	if len(first) != 8 || len(second) != 6 {
		t.Fatalf("segment split = %d+%d, want 8+6", len(first), len(second))
	}
	want := pattern(8, 14)
	got := join(first, second)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d", i, got[i], want[i])
		}
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 || b.Free() != 16 {
		t.Fatalf("after release: Len=%d Free=%d, want 0/16", b.Len(), b.Free())
	}
}

// TestOutOfOrderRelease verifies that releases may happen in any
// order but capacity is only returned as a contiguous released prefix.
func TestOutOfOrderRelease(t *testing.T) {
	b := New(16)
	if !b.TryWrite(pattern(0, 12)) {
		t.Fatal("write failed")
	}
	a, _ := b.Lend(4)
	c, _ := b.Lend(4)
	d, _ := b.Lend(4)

	// Release the middle loan first: nothing is reclaimed.
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != 4 {
		t.Fatalf("Free after middle release = %d, want 4", got)
	}
	if b.TryWrite(pattern(12, 8)) {
		t.Fatal("producer overwrote bytes still pinned by older loans")
	}

	// Release the newest loan: still no contiguous released prefix.
	if err := d.Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != 4 {
		t.Fatalf("Free after tail release = %d, want 4", got)
	}

	// Release the oldest: the whole prefix [a,c,d] is reclaimed at once.
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != 16 {
		t.Fatalf("Free after prefix release = %d, want 16", got)
	}
	if !b.TryWrite(pattern(12, 8)) {
		t.Fatal("write after prefix reclaim failed")
	}
}

// TestDoubleRelease verifies that releasing a loan twice is an error.
func TestDoubleRelease(t *testing.T) {
	b := New(8)
	if !b.TryWrite(pattern(0, 4)) {
		t.Fatal("write failed")
	}
	l, _ := b.Lend(4)
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != ErrDoubleRelease {
		t.Fatalf("second release = %v, want ErrDoubleRelease", err)
	}
}

// TestCancelWaitingWrite verifies that a producer blocked for space
// can be cancelled via its context.
func TestCancelWaitingWrite(t *testing.T) {
	b := New(8)
	if !b.TryWrite(pattern(0, 8)) {
		t.Fatal("fill failed")
	}
	// Pin 4 bytes with an unreleased loan so nothing can be reclaimed.
	l, _ := b.Lend(4)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.Write(ctx, pattern(8, 4))
		done <- err
	}()

	// Give the writer a moment to block, then cancel the wait.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancelled write = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write did not observe cancellation")
	}

	// A blocked write completes once loans are released.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	go func() {
		time.Sleep(20 * time.Millisecond)
		l.Release()
	}()
	if _, err := b.Write(ctx2, pattern(8, 4)); err != nil {
		t.Fatalf("write after release failed: %v", err)
	}
	if got := b.Len(); got != 8 {
		t.Fatalf("Len = %d, want 8", got)
	}
}

// TestByteSequenceModel drives the buffer with random operations and
// checks every lent byte against a queue model of the byte sequence,
// including wrap-around, out-of-order releases and prefix reclamation.
func TestByteSequenceModel(t *testing.T) {
	const capacity = 32
	rng := rand.New(rand.NewSource(1))
	b := New(capacity)

	var model []byte // bytes currently inside the buffer (head..tail)
	written := 0     // total bytes ever produced

	type openLoan struct {
		loan     *Loan
		len      int
		released bool
	}
	var loans []*openLoan // outstanding, oldest first
	lentBytes := 0

	reclaim := func() {
		for len(loans) > 0 && loans[0].released {
			model = model[loans[0].len:]
			lentBytes -= loans[0].len
			loans = loans[1:]
		}
	}

	for step := 0; step < 5000; step++ {
		switch rng.Intn(3) {
		case 0: // produce
			n := 1 + rng.Intn(12)
			if b.Free() >= n {
				if !b.TryWrite(pattern(written, n)) {
					t.Fatalf("step %d: TryWrite failed with %d free", step, b.Free())
				}
				model = append(model, pattern(written, n)...)
				written += n
			}
		case 1: // lend
			avail := len(model) - lentBytes
			if avail > 0 {
				n := 1 + rng.Intn(avail)
				l, ok := b.Lend(n)
				if !ok {
					t.Fatalf("step %d: Lend(%d) failed with %d available", step, n, avail)
				}
				first, second := l.Segments()
				got := join(first, second)
				for i := 0; i < n; i++ {
					if got[i] != model[lentBytes+i] {
						t.Fatalf("step %d: lent byte %d = %d, model has %d",
							step, i, got[i], model[lentBytes+i])
					}
				}
				loans = append(loans, &openLoan{loan: l, len: n})
				lentBytes += n
			}
		case 2: // release a random outstanding loan (out of order)
			var pending []int
			for i, ol := range loans {
				if !ol.released {
					pending = append(pending, i)
				}
			}
			if len(pending) > 0 {
				i := pending[rng.Intn(len(pending))]
				if err := loans[i].loan.Release(); err != nil {
					t.Fatalf("step %d: release failed: %v", step, err)
				}
				loans[i].released = true
				reclaim()
			}
		}
		if got := b.Len(); got != len(model) {
			t.Fatalf("step %d: Len = %d, model = %d", step, got, len(model))
		}
		if got := b.Free(); got != capacity-len(model) {
			t.Fatalf("step %d: Free = %d, model = %d", step, got, capacity-len(model))
		}
	}
}

// TestProducerConsumer streams a byte sequence through the buffer with
// a blocking producer and a consumer lending in irregular chunk sizes,
// verifying the received sequence end to end.
func TestProducerConsumer(t *testing.T) {
	const (
		capacity = 64
		total    = 10000
	)
	b := New(capacity)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prodDone := make(chan error, 1)
	go func() {
		written := 0
		for written < total {
			n := 1 + (written % 17)
			if n > total-written {
				n = total - written
			}
			if _, err := b.Write(ctx, pattern(written, n)); err != nil {
				prodDone <- err
				return
			}
			written += n
		}
		prodDone <- nil
	}()

	consDone := make(chan error, 1)
	go func() {
		received := 0
		rng := rand.New(rand.NewSource(2))
		var pending []*Loan
		for received < total {
			l, ok := b.Lend(1 + rng.Intn(13))
			if !ok {
				// Drain older loans out of order to free capacity.
				if len(pending) > 0 {
					l := pending[len(pending)-1]
					pending = pending[:len(pending)-1]
					if err := l.Release(); err != nil {
						consDone <- err
						return
					}
				}
				continue
			}
			first, second := l.Segments()
			for _, by := range join(first, second) {
				if by != patternAt(received) {
					consDone <- &seqError{at: received}
					return
				}
				received++
			}
			pending = append(pending, l)
			if len(pending) == 3 {
				if err := pending[0].Release(); err != nil {
					consDone <- err
					return
				}
				pending = pending[1:]
			}
		}
		for _, l := range pending {
			l.Release()
		}
		consDone <- nil
	}()

	if err := <-prodDone; err != nil {
		t.Fatalf("producer: %v", err)
	}
	if err := <-consDone; err != nil {
		t.Fatalf("consumer: %v", err)
	}
}

type seqError struct{ at int }

func (e *seqError) Error() string { return "byte sequence mismatch" }
