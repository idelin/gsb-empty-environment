package ringloan

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// seqByte is the byte-sequence model: the producer's stream is defined as
// stream[i] = seqByte(i). The modulus 251 is prime and shares no factor
// with any test capacity, so an offset error in the ring mapping almost
// surely produces a mismatch.
func seqByte(pos uint64) byte { return byte(pos % 251) }

func seqChunk(dst []byte, start uint64) {
	for i := range dst {
		dst[i] = seqByte(start + uint64(i))
	}
}

// checkLoan verifies a loan's (possibly two-segment) payload against the
// model stream at absolute position start.
func checkLoan(t *testing.T, l *Loan, start uint64) {
	t.Helper()
	if len(l.First)+len(l.Second) != l.Len() {
		t.Fatalf("loan segments %d+%d != Len() %d", len(l.First), len(l.Second), l.Len())
	}
	pos := start
	for _, seg := range [][]byte{l.First, l.Second} {
		for i, got := range seg {
			if want := seqByte(pos + uint64(i)); got != want {
				t.Fatalf("stream mismatch at absolute pos %d: got %d, want %d",
					pos+uint64(i), got, want)
			}
		}
		pos += uint64(len(seg))
	}
}

// TestWrapAroundLoan forces a loan to straddle the end of the ring and
// verifies both segments against the byte-sequence model.
func TestWrapAroundLoan(t *testing.T) {
	const cap = 16
	b := New(cap)

	w := make([]byte, 12)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l1, err := b.Loan(12)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l1, 0)
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}

	// Positions 12..19 wrap the ring: offsets 12..15 then 0..3.
	w = make([]byte, 8)
	seqChunk(w, 12)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l2, err := b.Loan(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(l2.First) != 4 || len(l2.Second) != 4 {
		t.Fatalf("expected two 4-byte segments across the ring tail, got %d and %d",
			len(l2.First), len(l2.Second))
	}
	checkLoan(t, l2, 12)
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != cap {
		t.Fatalf("Free() = %d, want %d", got, cap)
	}
}

// TestOutOfOrderRelease releases loans out of order and checks that
// capacity returns only as the contiguous released prefix advances.
func TestOutOfOrderRelease(t *testing.T) {
	const cap = 32
	b := New(cap)

	w := make([]byte, 24)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	loans := make([]*Loan, 3)
	for i := range loans {
		l, err := b.Loan(8)
		if err != nil {
			t.Fatal(err)
		}
		checkLoan(t, l, uint64(8*i))
		loans[i] = l
	}
	if got := b.Free(); got != cap-24 {
		t.Fatalf("Free() = %d, want %d", got, cap-24)
	}

	// Release the newest loan first: it is not the prefix, so no capacity
	// is returned.
	if err := loans[2].Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != cap-24 {
		t.Fatalf("after out-of-order release Free() = %d, want %d", got, cap-24)
	}
	// Release the oldest: it is itself the released prefix, so its 8 bytes
	// return; the middle loan still pins everything behind it.
	if err := loans[0].Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != cap-16 {
		t.Fatalf("with middle loan outstanding Free() = %d, want %d", got, cap-16)
	}

	// A 24-byte write must not fit while the middle loan pins the suffix.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	big := make([]byte, 24)
	seqChunk(big, 24)
	if err := b.WriteContext(ctx, big); err != context.DeadlineExceeded {
		t.Fatalf("write while prefix pinned: err = %v, want DeadlineExceeded", err)
	}

	// Releasing the middle loan completes the prefix through the
	// already-released newest loan; all remaining capacity returns at once.
	if err := loans[1].Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != cap {
		t.Fatalf("after prefix completed Free() = %d, want %d", got, cap)
	}
	if err := b.Write(big); err != nil {
		t.Fatal(err)
	}
	l, err := b.Loan(24)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l, 24)
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestDoubleRelease verifies the second release of a loan is reported.
func TestDoubleRelease(t *testing.T) {
	b := New(16)
	w := make([]byte, 8)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l, err := b.Loan(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != ErrDoubleRelease {
		t.Fatalf("second release: err = %v, want ErrDoubleRelease", err)
	}
	// A double release must not corrupt accounting for other loans.
	w2 := make([]byte, 8)
	seqChunk(w2, 8)
	if err := b.Write(w2); err != nil {
		t.Fatal(err)
	}
	l2, err := b.Loan(8)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l2, 8)
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
	if got := b.Free(); got != 16 {
		t.Fatalf("Free() = %d, want 16", got)
	}
}

// TestCancelProducerWait cancels a producer blocked on unreleased loans.
func TestCancelProducerWait(t *testing.T) {
	b := New(16)
	w := make([]byte, 16)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l, err := b.Loan(8) // pins the whole ring
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		more := make([]byte, 4)
		seqChunk(more, 16)
		done <- b.WriteContext(ctx, more)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancelled write: err = %v, want context.Canceled", err)
	}
	// The cancelled write must have written nothing.
	if got := b.Available(); got != 8 {
		t.Fatalf("Available() = %d, want 8", got)
	}
	// Releasing the loan restores writability.
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	more := make([]byte, 8)
	seqChunk(more, 16)
	if err := b.Write(more); err != nil {
		t.Fatal(err)
	}
	l2, err := b.Loan(16)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l2, 8)
	l2.Release()
}

// TestCancelConsumerWait cancels a consumer blocked on missing data.
func TestCancelConsumerWait(t *testing.T) {
	b := New(16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.LoanContext(ctx, 4)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancelled loan: err = %v, want context.Canceled", err)
	}
	// The ring is still usable.
	w := make([]byte, 4)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l, err := b.Loan(4)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l, 0)
	l.Release()
}

// TestWaitUnblockedByRelease checks a blocked producer wakes when a
// consumer releases the pinning loan.
func TestWaitUnblockedByRelease(t *testing.T) {
	b := New(16)
	w := make([]byte, 16)
	seqChunk(w, 0)
	if err := b.Write(w); err != nil {
		t.Fatal(err)
	}
	l, err := b.Loan(8)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		more := make([]byte, 8)
		seqChunk(more, 16)
		done <- b.Write(more)
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("write completed while loan pinned the ring")
	default:
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	l2, err := b.Loan(16)
	if err != nil {
		t.Fatal(err)
	}
	checkLoan(t, l2, 8)
	l2.Release()
}

// TestRandomizedModel interleaves writes, loans and out-of-order releases
// in a single goroutine and checks every observable against a model: loan
// payloads against the byte sequence, and Free() against the contiguous
// released prefix rule.
func TestRandomizedModel(t *testing.T) {
	const cap = 32
	b := New(cap)
	rng := rand.New(rand.NewSource(1))

	var written, loaned, modelBase uint64
	type modelLoan struct {
		l        *Loan
		start    uint64
		n        uint64
		released bool
	}
	var outstanding []*modelLoan

	modelFree := func() int {
		base := modelBase
		for _, m := range outstanding {
			if !m.released {
				break
			}
			base += m.n
		}
		return cap - int(written-base)
	}

	for step := 0; step < 3000; step++ {
		free := modelFree()
		avail := int(written - loaned)
		var pending []*modelLoan
		for _, m := range outstanding {
			if !m.released {
				pending = append(pending, m)
			}
		}
		switch {
		case free > 0 && (avail == 0 || rng.Intn(2) == 0):
			n := 1 + rng.Intn(free)
			w := make([]byte, n)
			seqChunk(w, written)
			if err := b.Write(w); err != nil {
				t.Fatalf("step %d: write: %v", step, err)
			}
			written += uint64(n)
		case avail > 0 && (len(pending) == 0 || rng.Intn(2) == 0):
			n := 1 + rng.Intn(avail)
			l, err := b.Loan(n)
			if err != nil {
				t.Fatalf("step %d: loan: %v", step, err)
			}
			checkLoan(t, l, loaned)
			outstanding = append(outstanding, &modelLoan{l: l, start: loaned, n: uint64(n)})
			loaned += uint64(n)
		default:
			m := pending[rng.Intn(len(pending))]
			if err := m.l.Release(); err != nil {
				t.Fatalf("step %d: release: %v", step, err)
			}
			m.released = true
			// Held loans must be intact regardless of release order.
			checkLoan(t, m.l, m.start)
			// Drop released prefix entries from the model queue.
			for len(outstanding) > 0 && outstanding[0].released {
				modelBase += outstanding[0].n
				outstanding = outstanding[1:]
			}
		}
		if got, want := b.Free(), modelFree(); got != want {
			t.Fatalf("step %d: Free() = %d, model wants %d", step, got, want)
		}
	}
}

// TestConcurrentByteSequence runs a producer and a consumer concurrently.
// The consumer releases loans in random order and verifies, at release
// time, that unreleased loans were never overwritten by the producer.
// Pinned bytes (everything since the oldest unreleased loan) stay below 24
// and chunks below 8, so the 64-byte ring always leaves the producer room:
// out-of-order releases behind an unreleased loan return no capacity, so
// under pressure the consumer must release the oldest loan.
func TestConcurrentByteSequence(t *testing.T) {
	const (
		cap      = 64
		total    = 20000
		maxChunk = 8
		maxHeld  = 24
	)
	b := New(cap)
	rng := rand.New(rand.NewSource(2))
	producerRng := rand.New(rand.NewSource(3))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var pos uint64
		chunk := make([]byte, maxChunk)
		for pos < total {
			n := 1 + producerRng.Intn(maxChunk)
			if rem := total - int(pos); n > rem {
				n = rem
			}
			seqChunk(chunk[:n], pos)
			if err := b.Write(chunk[:n]); err != nil {
				t.Errorf("write at %d: %v", pos, err)
				return
			}
			pos += uint64(n)
		}
	}()

	type held struct {
		l     *Loan
		start uint64
	}
	var pending []held
	var consumed uint64
	// pinnedBytes models the capacity the ring cannot reclaim: everything
	// from the oldest unreleased loan onward.
	pinnedBytes := func() uint64 {
		if len(pending) == 0 {
			return 0
		}
		return consumed - pending[0].start
	}
	releaseAt := func(i int) {
		checkLoan(t, pending[i].l, pending[i].start)
		if err := pending[i].l.Release(); err != nil {
			t.Fatal(err)
		}
		pending = append(pending[:i], pending[i+1:]...)
	}
	for consumed < total {
		n := 1 + rng.Intn(maxChunk)
		if rem := total - int(consumed); n > rem {
			n = rem
		}
		l, err := b.Loan(n)
		if err != nil {
			t.Fatalf("loan at %d: %v", consumed, err)
		}
		pending = append(pending, held{l: l, start: consumed})
		consumed += uint64(n)
		// Randomly release held loans out of order.
		for len(pending) > 1 && rng.Intn(2) == 0 {
			releaseAt(1 + rng.Intn(len(pending)-1))
		}
		// Under pin pressure, release the oldest loans in order: only the
		// contiguous released prefix returns capacity to the producer.
		for len(pending) > 0 && pinnedBytes() > maxHeld {
			releaseAt(0)
		}
	}
	for len(pending) > 0 {
		releaseAt(rng.Intn(len(pending)))
	}
	wg.Wait()
	if got := b.Free(); got != cap {
		t.Fatalf("Free() = %d, want %d", got, cap)
	}
}

// TestOversizedRequests rejects writes and loans beyond capacity.
func TestOversizedRequests(t *testing.T) {
	b := New(8)
	if err := b.Write(make([]byte, 9)); err != ErrTooLarge {
		t.Fatalf("oversized write: err = %v, want ErrTooLarge", err)
	}
	if _, err := b.Loan(9); err != ErrTooLarge {
		t.Fatalf("oversized loan: err = %v, want ErrTooLarge", err)
	}
	if _, err := b.Loan(0); err != ErrNotPositive {
		t.Fatalf("zero loan: err = %v, want ErrNotPositive", err)
	}
}
