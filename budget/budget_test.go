package budget

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcquireSpendAndRelease(t *testing.T) {
	b := New(2)

	t1, err := b.Acquire()
	if err != nil {
		t.Fatalf("acquire t1: %v", err)
	}
	t2, err := b.Acquire()
	if err != nil {
		t.Fatalf("acquire t2: %v", err)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
	if _, err := b.Acquire(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("third acquire err = %v, want ErrExhausted", err)
	}

	// Refunding an unspent token frees capacity again.
	t1.Release()
	if got := b.Remaining(); got != 1 {
		t.Fatalf("remaining after release = %d, want 1", got)
	}
	t3, err := b.Acquire()
	if err != nil {
		t.Fatalf("acquire t3 after refund: %v", err)
	}

	// Spending charges permanently; releasing afterwards must not refund.
	if !t2.Spend() {
		t.Fatal("spend t2 failed")
	}
	t2.Release()
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining after spend+release = %d, want 0", got)
	}

	// Double release refunds at most once.
	t3.Release()
	t3.Release()
	if got := b.Remaining(); got != 1 {
		t.Fatalf("remaining after double release = %d, want 1", got)
	}
}

func TestSpendAfterReleaseFails(t *testing.T) {
	b := New(1)
	tok, err := b.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	tok.Release()
	if tok.Spend() {
		t.Fatal("Spend on released token succeeded, want false")
	}
	if tok.Spent() {
		t.Fatal("token marked spent after release")
	}
}

func TestDeadlineRefusesAcquire(t *testing.T) {
	b := New(5).WithDeadline(time.Now().Add(-time.Second))
	if _, err := b.Acquire(); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("acquire err = %v, want ErrDeadlineExceeded", err)
	}
}

func TestContextDeadlinePropagation(t *testing.T) {
	budgetDeadline := time.Now().Add(time.Hour)
	b := New(5).WithDeadline(budgetDeadline)

	// Budget deadline earlier than parent's: budget wins.
	farParent, cancel := context.WithDeadline(context.Background(), time.Now().Add(24*time.Hour))
	defer cancel()
	ctx, stop := NewContext(farParent, b)
	defer stop()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(budgetDeadline) {
		t.Fatalf("deadline = %v, %v; want %v", got, ok, budgetDeadline)
	}

	// Parent deadline earlier than budget's: parent wins.
	near := time.Now().Add(time.Minute)
	nearParent, cancel2 := context.WithDeadline(context.Background(), near)
	defer cancel2()
	ctx2, stop2 := NewContext(nearParent, b)
	defer stop2()
	got2, _ := ctx2.Deadline()
	if !got2.Equal(near) {
		t.Fatalf("deadline = %v, want parent deadline %v", got2, near)
	}

	// Budget is retrievable from the derived context.
	if fb, ok := FromContext(ctx); !ok || fb != b {
		t.Fatal("FromContext did not return the attached budget")
	}
}

func TestConcurrentAcquireNeverOverCommits(t *testing.T) {
	const total = 50
	b := New(total)
	var wg sync.WaitGroup
	var mu sync.Mutex
	spent := 0
	for i := 0; i < 4*total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := b.Acquire()
			if err != nil {
				return
			}
			if tok.Spend() {
				mu.Lock()
				spent++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if spent != total {
		t.Fatalf("spent = %d, want exactly %d", spent, total)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
}
