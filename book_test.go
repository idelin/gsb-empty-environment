package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newReq(id, owner string, side Side, price, qty int64) OrderRequest {
	return OrderRequest{
		Symbol:  "BTC",
		Owner:   owner,
		OrderID: id,
		Side:    side,
		Price:   price,
		Qty:     qty,
	}
}

func seqIDs(orders []*Order) []string {
	ids := make([]string, len(orders))
	for i, order := range orders {
		ids[i] = order.OrderID()
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A forged client sequence or timestamp must never reorder the price-level
// queue: engine sequence alone decides time priority.
func TestSequenceForgeryCannotReorder(t *testing.T) {
	book := NewOrderBook("BTC")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := newReq("o1", "alice", SideBuy, 100, 1)
	first.ClientSeq = 1
	first.ClientTime = base
	if _, err := book.Submit(first); err != nil {
		t.Fatalf("submit o1: %v", err)
	}

	forged := newReq("o2", "mallory", SideBuy, 100, 1)
	forged.ClientSeq = 0
	forged.ClientTime = base.Add(-time.Hour)
	order, err := book.Submit(forged)
	if err != nil {
		t.Fatalf("submit o2: %v", err)
	}
	if order.Sequence != 2 {
		t.Fatalf("engine sequence = %d, want 2", order.Sequence)
	}

	future := newReq("o3", "mallory", SideBuy, 100, 1)
	future.ClientSeq = 999999
	future.ClientTime = base.Add(24 * time.Hour)
	if _, err := book.Submit(future); err != nil {
		t.Fatalf("submit o3: %v", err)
	}

	got := seqIDs(book.Queue(SideBuy, 100))
	want := []string{"o1", "o2", "o3"}
	if !equalStrings(got, want) {
		t.Fatalf("queue order = %v, want %v", got, want)
	}

	var popped []string
	for book.Len(SideBuy) > 0 {
		popped = append(popped, book.Pop(SideBuy).OrderID())
	}
	if !equalStrings(popped, want) {
		t.Fatalf("pop order = %v, want %v", popped, want)
	}
}

// Cancelling and resubmitting allocates a fresh sequence, so the order can
// only re-enter at the tail of its price level.
func TestCancelResubmitGoesToTail(t *testing.T) {
	book := NewOrderBook("BTC")

	for _, id := range []string{"a", "b", "c"} {
		if _, err := book.Submit(newReq(id, "acct", SideBuy, 100, 1)); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
	}

	cancelled, err := book.Cancel("a")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %v, want cancelled", cancelled.Status)
	}
	if got := book.Queue(SideBuy, 100); len(got) != 2 || got[0].OrderID() != "b" {
		t.Fatalf("queue after cancel = %v", seqIDs(got))
	}
	if _, err := book.Cancel("a"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("double cancel err = %v, want ErrOrderNotFound", err)
	}

	resent, err := book.Submit(newReq("a", "acct", SideBuy, 100, 1))
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if resent.Sequence != 4 {
		t.Fatalf("resubmit sequence = %d, want 4", resent.Sequence)
	}

	got := seqIDs(book.Queue(SideBuy, 100))
	want := []string{"b", "c", "a"}
	if !equalStrings(got, want) {
		t.Fatalf("queue after resubmit = %v, want %v", got, want)
	}
}

// Legitimate orders must obey strict price-time priority on both sides.
func TestPriceTimePriority(t *testing.T) {
	book := NewOrderBook("BTC")

	orders := []struct {
		id    string
		side  Side
		price int64
	}{
		{"b1", SideBuy, 100},
		{"b2", SideBuy, 102},
		{"b3", SideBuy, 102},
		{"b4", SideBuy, 101},
		{"s1", SideSell, 110},
		{"s2", SideSell, 108},
		{"s3", SideSell, 108},
	}
	for _, item := range orders {
		if _, err := book.Submit(newReq(item.id, "acct", item.side, item.price, 1)); err != nil {
			t.Fatalf("submit %s: %v", item.id, err)
		}
	}

	wantBuys := []string{"b2", "b3", "b4", "b1"}
	if got := seqIDs(book.Snapshot(SideBuy)); !equalStrings(got, wantBuys) {
		t.Fatalf("buy snapshot = %v, want %v", got, wantBuys)
	}

	wantSells := []string{"s2", "s3", "s1"}
	if got := seqIDs(book.Snapshot(SideSell)); !equalStrings(got, wantSells) {
		t.Fatalf("sell snapshot = %v, want %v", got, wantSells)
	}

	if best, ok := book.BestPrice(SideBuy); !ok || best != 102 {
		t.Fatalf("best bid = %d (%v), want 102", best, ok)
	}
	if best, ok := book.BestPrice(SideSell); !ok || best != 108 {
		t.Fatalf("best ask = %d (%v), want 108", best, ok)
	}

	var popped []string
	for {
		order := book.Pop(SideBuy)
		if order == nil {
			break
		}
		popped = append(popped, order.OrderID())
	}
	if !equalStrings(popped, wantBuys) {
		t.Fatalf("buy pop order = %v, want %v", popped, wantBuys)
	}
}

// Sequence allocation stays unique and monotonic under concurrent load, and
// each price level remains FIFO by assigned sequence.
func TestConcurrentSequencesMonotonic(t *testing.T) {
	book := NewOrderBook("BTC")
	const goroutines = 16
	const perGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				_, err := book.Submit(newReq(id, "acct", SideBuy, 100, 1))
				if err != nil {
					t.Errorf("concurrent submit: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	queue := book.Queue(SideBuy, 100)
	if len(queue) != goroutines*perGoroutine {
		t.Fatalf("queue len = %d, want %d", len(queue), goroutines*perGoroutine)
	}
	seen := make(map[uint64]bool, len(queue))
	var last uint64
	for _, order := range queue {
		if seen[order.Sequence] {
			t.Fatalf("duplicate sequence %d", order.Sequence)
		}
		seen[order.Sequence] = true
		if order.Sequence < last {
			t.Fatalf("queue not sequence-ordered: %d after %d", order.Sequence, last)
		}
		last = order.Sequence
	}
}

// Invalid requests are rejected before consuming any sequence.
func TestValidation(t *testing.T) {
	book := NewOrderBook("BTC")
	valid := newReq("dup", "alice", SideBuy, 100, 1)
	if _, err := book.Submit(valid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name string
		req  OrderRequest
		want error
	}{
		{"missing id", OrderRequest{Owner: "a", Side: SideBuy, Price: 1, Qty: 1}, ErrEmptyOrderID},
		{"missing owner", OrderRequest{OrderID: "x", Side: SideBuy, Price: 1, Qty: 1}, ErrEmptyOwner},
		{"bad side", OrderRequest{OrderID: "x", Owner: "a", Side: SideUnset, Price: 1, Qty: 1}, ErrInvalidSide},
		{"bad price", OrderRequest{OrderID: "x", Owner: "a", Side: SideBuy, Price: 0, Qty: 1}, ErrInvalidPrice},
		{"bad qty", OrderRequest{OrderID: "x", Owner: "a", Side: SideBuy, Price: 1, Qty: -1}, ErrInvalidQty},
		{"duplicate", newReq("dup", "alice", SideBuy, 100, 1), ErrDuplicateOrderID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := book.Submit(tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if seq := book.NextSequence(); seq != 2 {
		t.Fatalf("next sequence = %d, want 2 (failed validation consumes none)", seq)
	}
}
