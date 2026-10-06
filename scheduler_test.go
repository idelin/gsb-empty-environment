package devsched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type sentRequest struct {
	txnID   uint32
	payload []byte
}

type fakeTransport struct {
	mu   sync.Mutex
	sent []sentRequest
	err  error
}

func (f *fakeTransport) Send(txnID uint32, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentRequest{txnID: txnID, payload: payload})
	return nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeTransport) at(i int) sentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[i]
}

type asyncResult struct {
	resp []byte
	err  error
}

func submitAsync(s *Scheduler, ctx context.Context, payload string) chan asyncResult {
	ch := make(chan asyncResult, 1)
	go func() {
		resp, err := s.Submit(ctx, []byte(payload))
		ch <- asyncResult{resp: resp, err: err}
	}()
	return ch
}

func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func recvResult(t *testing.T, ch chan asyncResult) asyncResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request result")
		return asyncResult{}
	}
}

func assertNoResult(t *testing.T, ch chan asyncResult) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("unexpected result: %+v", r)
	default:
	}
}

func newTestScheduler(tr *fakeTransport, clock *VirtualClock, timeout, isolation time.Duration, idSpace int) *Scheduler {
	return New(tr,
		WithClock(clock),
		WithTimeout(timeout),
		WithIsolation(isolation),
		WithTxnIDSpace(idSpace),
	)
}

// Replay: the device never answers and the request times out.
func TestTimeout(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, time.Second, 10*time.Second, 4)

	res := submitAsync(s, context.Background(), "a")
	waitFor(t, "request sent", func() bool { return tr.count() == 1 })
	if got := tr.at(0).txnID; got != 0 {
		t.Fatalf("first txn ID = %d, want 0", got)
	}

	clock.Advance(time.Second)
	r := recvResult(t, res)
	if !errors.Is(r.err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", r.err)
	}
	if got := s.Stats().Timeouts; got != 1 {
		t.Fatalf("timeouts = %d, want 1", got)
	}
	if s.InFlight() {
		t.Fatal("slot still in flight after timeout")
	}
}

// Replay: a response arriving after the timeout is stale, is dropped, and
// must not complete a later request — even while the old ID sits in
// quarantine and a different request is in flight.
func TestLateResponseDropped(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, time.Second, 10*time.Second, 4)

	resA := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })
	clock.Advance(2 * time.Second) // a times out at +1s
	if r := recvResult(t, resA); !errors.Is(r.err, ErrTimeout) {
		t.Fatalf("a: err = %v, want ErrTimeout", r.err)
	}

	// Late answer for the timed-out request arrives while idle: dropped.
	if s.HandleResponse(0, []byte("late")) {
		t.Fatal("accepted late response while idle")
	}

	// Next request must not reuse ID 0: it is still in isolation.
	resB := submitAsync(s, context.Background(), "b")
	waitFor(t, "b sent", func() bool { return tr.count() == 2 })
	idB := tr.at(1).txnID
	if idB == 0 {
		t.Fatal("reused quarantined txn ID 0")
	}

	// The stale answer must not complete the new in-flight request.
	if s.HandleResponse(0, []byte("late")) {
		t.Fatal("late response completed a new request")
	}
	assertNoResult(t, resB)

	// The matching answer completes it.
	if !s.HandleResponse(idB, []byte("ok-b")) {
		t.Fatal("rejected the in-flight response")
	}
	rB := recvResult(t, resB)
	if rB.err != nil || string(rB.resp) != "ok-b" {
		t.Fatalf("b: got %q, %v", rB.resp, rB.err)
	}

	stats := s.Stats()
	if stats.StaleResponses != 2 || stats.Timeouts != 1 || stats.Completed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

// Replay: with a tiny ID space every ID is quarantined, so dispatch stalls
// until the isolation period of the earliest ID expires.
func TestIsolationDelaysReuse(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, 30*time.Second, 10*time.Second, 2)

	resA := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })
	s.HandleResponse(0, []byte("ok-a"))
	recvResult(t, resA)

	resB := submitAsync(s, context.Background(), "b")
	waitFor(t, "b sent", func() bool { return tr.count() == 2 })
	s.HandleResponse(1, []byte("ok-b"))
	recvResult(t, resB)

	// Both IDs quarantined until t0+10s: c must wait.
	resC := submitAsync(s, context.Background(), "c")
	waitFor(t, "c queued", func() bool { return s.Queued() == 1 })
	time.Sleep(5 * time.Millisecond)
	if tr.count() != 2 {
		t.Fatalf("c dispatched while all IDs quarantined: sent = %d", tr.count())
	}

	clock.Advance(9 * time.Second) // t0+9s: still quarantined
	time.Sleep(5 * time.Millisecond)
	if tr.count() != 2 {
		t.Fatalf("c dispatched before isolation expired: sent = %d", tr.count())
	}
	assertNoResult(t, resC)

	clock.Advance(time.Second) // t0+10s: ID 0 reusable again
	waitFor(t, "c sent", func() bool { return tr.count() == 3 })
	if got := tr.at(2).txnID; got != 0 {
		t.Fatalf("c txn ID = %d, want 0 (earliest released)", got)
	}
	s.HandleResponse(0, []byte("ok-c"))
	if r := recvResult(t, resC); r.err != nil {
		t.Fatalf("c: err = %v", r.err)
	}
}

// Replay: the ID ring wraps around; reuse follows ring order and never
// violates the isolation period.
func TestWraparound(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, 30*time.Second, 2*time.Second, 3)

	// Three requests at t0 consume IDs 0,1,2 in order.
	for i, want := range []uint32{0, 1, 2} {
		res := submitAsync(s, context.Background(), "r")
		waitFor(t, "request sent", func() bool { return tr.count() == i+1 })
		if got := tr.at(i).txnID; got != want {
			t.Fatalf("request %d: txn ID = %d, want %d", i, got, want)
		}
		s.HandleResponse(want, []byte("ok"))
		recvResult(t, res)
	}

	// Fourth request at t0: all IDs quarantined until t0+2s, so it waits.
	res4 := submitAsync(s, context.Background(), "r")
	waitFor(t, "fourth queued", func() bool { return s.Queued() == 1 })
	time.Sleep(5 * time.Millisecond)
	if tr.count() != 3 {
		t.Fatalf("wraparound reused an ID inside isolation: sent = %d", tr.count())
	}

	clock.Advance(2 * time.Second) // isolation over: ring wraps to ID 0
	waitFor(t, "fourth sent", func() bool { return tr.count() == 4 })
	if got := tr.at(3).txnID; got != 0 {
		t.Fatalf("after wraparound txn ID = %d, want 0", got)
	}
	s.HandleResponse(0, []byte("ok"))
	recvResult(t, res4)
}

// Replay: cancelling a queued request leaves the in-flight request and the
// rest of the queue untouched.
func TestQueueCancelLeavesInflight(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, 30*time.Second, time.Second, 4)

	resA := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })

	ctxB, cancelB := context.WithCancel(context.Background())
	resB := submitAsync(s, ctxB, "b")
	waitFor(t, "b queued", func() bool { return s.Queued() == 1 })
	resC := submitAsync(s, context.Background(), "c")
	waitFor(t, "c queued", func() bool { return s.Queued() == 2 })

	cancelB()
	if r := recvResult(t, resB); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("b: err = %v, want context.Canceled", r.err)
	}

	// In-flight request is unaffected by the queue cancellation.
	assertNoResult(t, resA)
	if !s.InFlight() {
		t.Fatal("in-flight request disturbed by queue cancellation")
	}
	if !s.HandleResponse(0, []byte("ok-a")) {
		t.Fatal("in-flight response rejected after queue cancellation")
	}
	if r := recvResult(t, resA); r.err != nil || string(r.resp) != "ok-a" {
		t.Fatalf("a: got %q, %v", r.resp, r.err)
	}

	// c is dispatched next; b was never sent to the device.
	waitFor(t, "c sent", func() bool { return tr.count() == 2 })
	if got := string(tr.at(1).payload); got != "c" {
		t.Fatalf("second sent payload = %q, want %q", got, "c")
	}
	s.HandleResponse(tr.at(1).txnID, []byte("ok-c"))
	if r := recvResult(t, resC); r.err != nil {
		t.Fatalf("c: err = %v", r.err)
	}
	if got := s.Stats().Cancelled; got != 1 {
		t.Fatalf("cancelled = %d, want 1", got)
	}
}

// Replay: shutdown fails queued requests immediately and drains the
// in-flight request when its response arrives.
func TestShutdownDrainOnResponse(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, 30*time.Second, time.Second, 4)

	resA := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })
	resB := submitAsync(s, context.Background(), "b")
	waitFor(t, "b queued", func() bool { return s.Queued() == 1 })
	resC := submitAsync(s, context.Background(), "c")
	waitFor(t, "c queued", func() bool { return s.Queued() == 2 })

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()

	for _, res := range []chan asyncResult{resB, resC} {
		if r := recvResult(t, res); !errors.Is(r.err, ErrClosed) {
			t.Fatalf("queued request: err = %v, want ErrClosed", r.err)
		}
	}

	// Close keeps waiting while the in-flight request is outstanding.
	select {
	case <-closed:
		t.Fatal("Close returned before the in-flight request drained")
	default:
	}
	assertNoResult(t, resA)

	// The device finally answers: in-flight completes, Close returns.
	if !s.HandleResponse(0, []byte("ok-a")) {
		t.Fatal("in-flight response rejected during shutdown drain")
	}
	if r := recvResult(t, resA); r.err != nil || string(r.resp) != "ok-a" {
		t.Fatalf("a: got %q, %v", r.resp, r.err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after drain")
	}

	if _, err := s.Submit(context.Background(), []byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close: err = %v, want ErrClosed", err)
	}
}

// Replay: shutdown where the in-flight request drains by timing out.
func TestShutdownDrainOnTimeout(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, 5*time.Second, time.Second, 4)

	resA := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })
	resB := submitAsync(s, context.Background(), "b")
	waitFor(t, "b queued", func() bool { return s.Queued() == 1 })

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()

	if r := recvResult(t, resB); !errors.Is(r.err, ErrClosed) {
		t.Fatalf("b: err = %v, want ErrClosed", r.err)
	}
	assertNoResult(t, resA)

	clock.Advance(5 * time.Second) // in-flight times out; drain completes
	if r := recvResult(t, resA); !errors.Is(r.err, ErrTimeout) {
		t.Fatalf("a: err = %v, want ErrTimeout", r.err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after timeout drain")
	}
}

// Replay: a response with no matching in-flight request is stale.
func TestStaleResponseWhenIdle(t *testing.T) {
	clock := NewVirtualClock(time.Unix(1000, 0))
	tr := &fakeTransport{}
	s := newTestScheduler(tr, clock, time.Second, time.Second, 4)

	if s.HandleResponse(3, []byte("stray")) {
		t.Fatal("accepted response with nothing in flight")
	}
	if got := s.Stats().StaleResponses; got != 1 {
		t.Fatalf("stale = %d, want 1", got)
	}
}

// Smoke test against the real clock: response path and timeout path.
func TestRealClock(t *testing.T) {
	tr := &fakeTransport{}
	s := New(tr, WithTimeout(50*time.Millisecond), WithIsolation(10*time.Millisecond))

	res := submitAsync(s, context.Background(), "a")
	waitFor(t, "a sent", func() bool { return tr.count() == 1 })
	s.HandleResponse(tr.at(0).txnID, []byte("ok-a"))
	if r := recvResult(t, res); r.err != nil || string(r.resp) != "ok-a" {
		t.Fatalf("a: got %q, %v", r.resp, r.err)
	}

	res2 := submitAsync(s, context.Background(), "b")
	waitFor(t, "b sent", func() bool { return tr.count() == 2 })
	if r := recvResult(t, res2); !errors.Is(r.err, ErrTimeout) {
		t.Fatalf("b: err = %v, want ErrTimeout", r.err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: err = %v", err)
	}
}
