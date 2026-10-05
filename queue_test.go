package mpqueue

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestFIFO(t *testing.T) {
	q := New[int](3)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		v, err := q.Dequeue(ctx)
		if err != nil || v != i {
			t.Fatalf("got %v, %v; want %d, nil", v, err, i)
		}
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestBlockingDequeueUnblockedByEnqueue(t *testing.T) {
	q := New[int](1)
	done := make(chan int, 1)
	go func() {
		v, err := q.Dequeue(context.Background())
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	time.Sleep(20 * time.Millisecond)
	if err := q.Enqueue(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-done:
		if v != 42 {
			t.Fatalf("got %d, want 42", v)
		}
	case <-time.After(time.Second):
		t.Fatal("dequeue did not unblock")
	}
}

func TestGracefulCloseDrains(t *testing.T) {
	q := New[int](4)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	if !q.Close() {
		t.Fatal("Close should win on an open queue")
	}
	if q.Close() {
		t.Fatal("second Close must lose")
	}
	if _, won := q.Abort(); won {
		t.Fatal("Abort after Close must lose")
	}
	if err := q.Enqueue(ctx, 99); !errors.Is(err, ErrClosed) {
		t.Fatalf("enqueue after close: got %v, want ErrClosed", err)
	}
	for i := 0; i < 3; i++ {
		v, err := q.Dequeue(ctx)
		if err != nil || v != i {
			t.Fatalf("drain: got %v, %v; want %d, nil", v, err, i)
		}
	}
	if _, err := q.Dequeue(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("dequeue on drained queue: got %v, want ErrClosed", err)
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	s, err := Verify(q.History(), q.Cap())
	if err != nil {
		t.Fatal(err)
	}
	if s.Winner != OpClose || s.Enqueued != 3 || s.Dequeued != 3 || s.Remaining != 0 {
		t.Fatalf("bad summary: %+v", s)
	}
}

func TestAbortStopsImmediately(t *testing.T) {
	q := New[int](4)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	// A blocked consumer must wake with ErrAborted.
	blocked := make(chan error, 1)
	go func() {
		for {
			_, err := q.Dequeue(ctx)
			if err != nil {
				blocked <- err
				return
			}
		}
	}()
	rest, won := q.Abort()
	if !won {
		t.Fatal("Abort should win on an open queue")
	}
	if q.Close() {
		t.Fatal("Close after Abort must lose")
	}
	if len(rest) != 3 {
		t.Fatalf("remainder: got %v, want 3 items", rest)
	}
	for i, v := range rest {
		if v != i {
			t.Fatalf("remainder not FIFO: %v", rest)
		}
	}
	if err := <-blocked; !errors.Is(err, ErrAborted) {
		t.Fatalf("blocked consumer: got %v, want ErrAborted", err)
	}
	if _, err := q.Dequeue(ctx); !errors.Is(err, ErrAborted) {
		t.Fatalf("dequeue after abort: got %v, want ErrAborted", err)
	}
	if err := q.Enqueue(ctx, 1); !errors.Is(err, ErrAborted) {
		t.Fatalf("enqueue after abort: got %v, want ErrAborted", err)
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	s, err := Verify(q.History(), q.Cap())
	if err != nil {
		t.Fatal(err)
	}
	if s.Winner != OpAbort || s.Released != 3 || s.Remaining != 0 {
		t.Fatalf("bad summary: %+v", s)
	}
	_, released, err := Replay(q.History(), q.Cap())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(released) != fmt.Sprint(rest) {
		t.Fatalf("replayed remainder %v != actual %v", released, rest)
	}
}

func TestFirstShutdownWins(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		q := New[int](8)
		ctx := context.Background()
		n := rand.Intn(8)
		for i := 0; i < n; i++ {
			if err := q.Enqueue(ctx, i); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		var closeWon, abortWon bool
		var rest []int
		wg.Add(2)
		go func() { defer wg.Done(); closeWon = q.Close() }()
		go func() { defer wg.Done(); rest, abortWon = q.Abort() }()
		wg.Wait()
		if closeWon == abortWon {
			t.Fatalf("trial %d: exactly one shutdown must win, closeWon=%v abortWon=%v", trial, closeWon, abortWon)
		}
		var drained []int
		if closeWon {
			if len(rest) != 0 {
				t.Fatalf("trial %d: losing Abort returned items", trial)
			}
			for {
				v, err := q.Dequeue(ctx)
				if errors.Is(err, ErrClosed) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				drained = append(drained, v)
			}
			if len(drained) != n {
				t.Fatalf("trial %d: drained %d, want %d", trial, len(drained), n)
			}
		} else {
			if len(rest) != n {
				t.Fatalf("trial %d: remainder %d, want %d", trial, len(rest), n)
			}
			if _, err := q.Dequeue(ctx); !errors.Is(err, ErrAborted) {
				t.Fatalf("trial %d: got %v, want ErrAborted", trial, err)
			}
		}
		if err := q.Check(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		s, err := Verify(q.History(), q.Cap())
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if s.Enqueued != s.Dequeued+s.Released+s.Remaining {
			t.Fatalf("trial %d: conservation broken: %+v", trial, s)
		}
	}
}

func TestCancelledDequeueDoesNotLoseItems(t *testing.T) {
	q := New[int](2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	// The cancelled dequeue must not have taken anything.
	if err := q.Enqueue(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	v, err := q.Dequeue(context.Background())
	if err != nil || v != 7 {
		t.Fatalf("got %v, %v; want 7, nil", v, err)
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledEnqueueKeepsOwnership(t *testing.T) {
	q := New[int](1)
	bg := context.Background()
	if err := q.Enqueue(bg, 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- q.Enqueue(ctx, 2) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	// Item 2 was never consumed by the queue: only item 1 is inside.
	v, err := q.Dequeue(bg)
	if err != nil || v != 1 {
		t.Fatalf("got %v, %v; want 1, nil", v, err)
	}
	if _, err := q.Dequeue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("queue should be empty now, got %v", err)
	}
	st := q.Stats()
	if st.Enqueued != 1 || st.Dequeued != 1 {
		t.Fatalf("bad stats: %+v", st)
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStressConservation(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		q := New[int](5)
		bg := context.Background()
		const producers = 4
		const perProducer = 50
		var produced, consumed sync.Map

		var pwg sync.WaitGroup
		for p := 0; p < producers; p++ {
			pwg.Add(1)
			go func(base int) {
				defer pwg.Done()
				n := 0
				for i := 0; i < perProducer; i++ {
					if err := q.Enqueue(bg, base+i); err != nil {
						break // rejected items stay with the producer
					}
					n++
				}
				produced.Store(base, n)
			}(p * perProducer)
		}

		var cwg sync.WaitGroup
		for c := 0; c < 3; c++ {
			cwg.Add(1)
			go func(id int) {
				defer cwg.Done()
				n := 0
				for {
					_, err := q.Dequeue(bg)
					if errors.Is(err, ErrClosed) || errors.Is(err, ErrAborted) {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					n++
				}
				consumed.Store(id, n)
			}(c)
		}

		pwg.Wait()
		// Randomly pick a shutdown mode; the race loser is irrelevant here.
		var rest []int
		if rand.Intn(2) == 0 {
			q.Close()
		} else {
			rest, _ = q.Abort()
		}
		// If close won, consumers drain; if abort won, they stop. Either way
		// a final Abort releases whatever is left (no-op after abort).
		rest2, _ := q.Abort()
		rest = append(rest, rest2...)
		cwg.Wait()

		totalProduced, totalConsumed := 0, 0
		produced.Range(func(_, v any) bool { totalProduced += v.(int); return true })
		consumed.Range(func(_, v any) bool { totalConsumed += v.(int); return true })
		if totalProduced != totalConsumed+len(rest) {
			t.Fatalf("trial %d: produced=%d consumed=%d remainder=%d", trial, totalProduced, totalConsumed, len(rest))
		}
		if err := q.Check(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		s, err := Verify(q.History(), q.Cap())
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if s.Enqueued != totalProduced || s.Dequeued != totalConsumed || s.Released != len(rest) {
			t.Fatalf("trial %d: summary %+v vs produced=%d consumed=%d remainder=%d",
				trial, s, totalProduced, totalConsumed, len(rest))
		}
		if _, _, err := Replay(q.History(), q.Cap()); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	good := []Event[int]{
		{Seq: 1, Op: OpEnqueue, Value: 1},
		{Seq: 2, Op: OpEnqueue, Value: 2},
		{Seq: 3, Op: OpDequeue, Value: 1},
		{Seq: 4, Op: OpAbort, Count: 1},
	}
	if _, err := Verify(good, 2); err != nil {
		t.Fatalf("good history rejected: %v", err)
	}
	bad := [][]Event[int]{
		// sequence gap
		{{Seq: 2, Op: OpEnqueue, Value: 1}},
		// capacity exceeded
		{{Seq: 1, Op: OpEnqueue, Value: 1}, {Seq: 2, Op: OpEnqueue, Value: 2}},
		// dequeue from empty
		{{Seq: 1, Op: OpDequeue, Value: 1}},
		// enqueue after close
		{{Seq: 1, Op: OpClose}, {Seq: 2, Op: OpEnqueue, Value: 1}},
		// dequeue after abort
		{{Seq: 1, Op: OpAbort, Count: 0}, {Seq: 2, Op: OpDequeue, Value: 1}},
		// two shutdowns
		{{Seq: 1, Op: OpClose}, {Seq: 2, Op: OpAbort, Count: 0}},
		// abort remainder mismatch
		{{Seq: 1, Op: OpEnqueue, Value: 1}, {Seq: 2, Op: OpAbort, Count: 0}},
	}
	for i, h := range bad {
		if _, err := Verify(h, 1); err == nil {
			t.Fatalf("bad history %d accepted", i)
		}
	}
	// FIFO violation caught by Replay.
	fifoBad := []Event[int]{
		{Seq: 1, Op: OpEnqueue, Value: 1},
		{Seq: 2, Op: OpEnqueue, Value: 2},
		{Seq: 3, Op: OpDequeue, Value: 2},
	}
	if _, _, err := Replay(fifoBad, 2); err == nil {
		t.Fatal("FIFO violation not detected")
	}
}
