package txnsched

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type sentRequest struct {
	txn     int
	payload []byte
}

type fakeTransport struct {
	mu    sync.Mutex
	sends []sentRequest
	err   error
}

func (f *fakeTransport) Send(txn int, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sends = append(f.sends, sentRequest{txn: txn, payload: append([]byte(nil), payload...)})
	return nil
}

func (f *fakeTransport) sentTxns() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, s := range f.sends {
		out = append(out, s.txn)
	}
	return out
}

func (f *fakeTransport) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

type harness struct {
	sched *Scheduler
	clock *VirtualClock
	tr    *fakeTransport
}

func newHarness(cfg Config) *harness {
	clock := NewVirtualClock(time.Unix(1700000000, 0))
	tr := &fakeTransport{}
	sched := NewScheduler(cfg, clock, tr)
	return &harness{sched: sched, clock: clock, tr: tr}
}

func (h *harness) submit(payload string) *Future {
	fut, err := h.sched.Submit(context.Background(), []byte(payload))
	if err != nil {
		panic(err)
	}
	return fut
}

func awaitResult(t *testing.T, fut *Future) Result {
	t.Helper()
	select {
	case r := <-fut.Done():
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for result")
		return Result{}
	}
}

func expectPending(t *testing.T, fut *Future, what string) {
	t.Helper()
	select {
	case r := <-fut.Done():
		t.Fatalf("%s: expected pending, got %+v", what, r)
	default:
	}
}

func waitClosed(t *testing.T, s *Scheduler) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not drain")
	}
}

// Replay: in-flight request times out; the next request must use a
// fresh transaction number because the old one sits in isolation.
func TestTimeoutPutsTxnIntoIsolation(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	a := h.submit("a")
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("sends = %v", got)
	}

	h.clock.Advance(10 * time.Second) // timeout fires
	h.sched.sync()
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: got %v", r.Err)
	}

	b := h.submit("b")
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("sends = %v, want txn 0 quarantined", got)
	}

	h.sched.HandleResponse(1, []byte("ok-b"))
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != nil || string(r.Resp.Payload) != "ok-b" {
		t.Fatalf("b: got %+v", r)
	}
	if st := h.sched.Stats(); st.Timeouts != 1 || st.Completed != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// Replay: with a tiny number space every txn is isolated, so dispatch
// stalls until the earliest quarantine expires; numbers then wrap.
func TestWraparoundWaitsForIsolation(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 2, Timeout: 10 * time.Second, Isolation: 100 * time.Second})

	a := h.submit("a")
	h.sched.sync()
	h.clock.Advance(10 * time.Second) // t=10: a times out, txn0 isolated until t=110
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: %v", r.Err)
	}

	b := h.submit("b")
	h.sched.sync()
	h.clock.Advance(10 * time.Second) // t=20: b times out, txn1 isolated until t=120
	if r := awaitResult(t, b); r.Err != ErrTimeout {
		t.Fatalf("b: %v", r.Err)
	}

	c := h.submit("c")
	h.sched.sync()
	if n := h.tr.sentCount(); n != 2 {
		t.Fatalf("c dispatched while every txn isolated, sends = %d", n)
	}

	// A late answer for txn0 arrives while c is still queued: dropped.
	h.sched.HandleResponse(0, []byte("late-a"))
	h.sched.sync()
	expectPending(t, c, "c")
	if st := h.sched.Stats(); st.LateResponses != 1 {
		t.Fatalf("late = %d", st.LateResponses)
	}

	h.clock.Advance(90 * time.Second) // t=110: txn0 leaves isolation
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0, 1, 0}) {
		t.Fatalf("sends = %v, want wraparound [0 1 0]", got)
	}

	h.sched.HandleResponse(0, []byte("ok-c"))
	h.sched.sync()
	if r := awaitResult(t, c); r.Err != nil || string(r.Resp.Payload) != "ok-c" {
		t.Fatalf("c: got %+v", r)
	}
}

// Replay: a response that arrives after its exchange timed out must not
// complete the request now occupying the line.
func TestLateResponseDoesNotCompleteNewRequest(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: 10 * time.Minute})

	a := h.submit("a")
	h.sched.sync()
	h.clock.Advance(10 * time.Second) // a times out
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: %v", r.Err)
	}

	b := h.submit("b") // dispatched with txn1
	h.sched.sync()

	// The device finally answers the old exchange (txn0) while b is
	// in flight. It must be dropped, not credited to b.
	h.sched.HandleResponse(0, []byte("late-a"))
	h.sched.sync()
	expectPending(t, b, "b")
	if st := h.sched.Stats(); st.LateResponses != 1 || st.Completed != 0 {
		t.Fatalf("stats = %+v", st)
	}

	h.sched.HandleResponse(1, []byte("ok-b"))
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != nil || string(r.Resp.Payload) != "ok-b" {
		t.Fatalf("b: got %+v", r)
	}

	// A duplicate of the same answer is dropped too.
	h.sched.HandleResponse(1, []byte("dup-b"))
	h.sched.sync()
	if st := h.sched.Stats(); st.LateResponses != 2 || st.Completed != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// Replay: a single-number space cannot reuse its only txn until the
// isolation window passes, which is what keeps a late answer from the
// previous exchange from colliding with the next one.
func TestSingleTxnSpaceSerializesByIsolation(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 1, Timeout: 5 * time.Second, Isolation: 30 * time.Second})

	a := h.submit("a")
	h.sched.sync()
	h.clock.Advance(5 * time.Second) // t=5: a times out, txn0 isolated until t=35
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: %v", r.Err)
	}

	b := h.submit("b")
	h.sched.sync()
	if n := h.tr.sentCount(); n != 1 {
		t.Fatalf("b sent while txn0 isolated, sends = %d", n)
	}

	h.clock.Advance(29 * time.Second) // t=34: still isolated
	h.sched.sync()
	expectPending(t, b, "b")
	if n := h.tr.sentCount(); n != 1 {
		t.Fatalf("b sent early, sends = %d", n)
	}

	h.clock.Advance(time.Second) // t=35: isolation over
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0, 0}) {
		t.Fatalf("sends = %v", got)
	}
	h.sched.HandleResponse(0, []byte("ok-b"))
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != nil {
		t.Fatalf("b: %v", r.Err)
	}
}

// Replay: Close drains the queue with ErrShutdown, rejects new
// submissions, lets the in-flight exchange finish, then exits.
func TestShutdownDrainsQueueKeepsInflight(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	a := h.submit("a") // in flight, txn0
	b := h.submit("b") // queued
	h.sched.sync()
	if n := h.tr.sentCount(); n != 1 {
		t.Fatalf("sends = %d", n)
	}

	h.sched.Close()
	h.sched.sync()

	if r := awaitResult(t, b); r.Err != ErrShutdown {
		t.Fatalf("b: %v", r.Err)
	}
	expectPending(t, a, "a") // in flight is not touched by the drain

	if _, err := h.sched.Submit(context.Background(), []byte("c")); err != ErrClosed {
		t.Fatalf("submit after close: %v", err)
	}

	h.sched.HandleResponse(0, []byte("ok-a"))
	if r := awaitResult(t, a); r.Err != nil || string(r.Resp.Payload) != "ok-a" {
		t.Fatalf("a: got %+v", r)
	}
	waitClosed(t, h.sched)
}

// Replay: shutdown while in flight, device never answers — the exchange
// still ends by timeout and the loop exits.
func TestShutdownDrainByTimeout(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	a := h.submit("a")
	h.sched.sync()
	h.sched.Close()
	h.sched.sync()

	h.clock.Advance(10 * time.Second)
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: %v", r.Err)
	}
	waitClosed(t, h.sched)
}

// Replay: cancelling a queued request removes it without disturbing the
// in-flight exchange.
func TestQueueCancelDoesNotAffectInflight(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	a := h.submit("a") // in flight, txn0
	ctxB, cancelB := context.WithCancel(context.Background())
	b, err := h.sched.Submit(ctxB, []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	h.sched.sync()

	cancelB()
	if r := awaitResult(t, b); r.Err != context.Canceled {
		t.Fatalf("b: %v", r.Err)
	}
	h.sched.sync()
	expectPending(t, a, "a")
	if n := h.tr.sentCount(); n != 1 {
		t.Fatalf("cancel dispatched queued work, sends = %d", n)
	}

	h.sched.HandleResponse(0, []byte("ok-a"))
	h.sched.sync()
	if r := awaitResult(t, a); r.Err != nil {
		t.Fatalf("a: %v", r.Err)
	}
	if st := h.sched.Stats(); st.Cancelled != 1 || st.Completed != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// Replay: cancelling the caller of the in-flight request unblocks that
// caller, but the device exchange itself runs on to its response.
func TestInflightCancelKeepsExchange(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	ctxA, cancelA := context.WithCancel(context.Background())
	a, err := h.sched.Submit(ctxA, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	h.sched.sync()

	cancelA()
	if r := awaitResult(t, a); r.Err != context.Canceled {
		t.Fatalf("a: %v", r.Err)
	}
	h.sched.sync()

	// The exchange is still on the line: the device answer is consumed
	// (not reported as late) and frees the slot for the next request.
	h.sched.HandleResponse(0, []byte("ok-a"))
	h.sched.sync()
	if st := h.sched.Stats(); st.LateResponses != 0 {
		t.Fatalf("late = %d", st.LateResponses)
	}

	b := h.submit("b")
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("sends = %v", got)
	}
	h.sched.HandleResponse(1, []byte("ok-b"))
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != nil {
		t.Fatalf("b: %v", r.Err)
	}
}

// Replay: completing the in-flight request immediately dispatches the
// next queued one, keeping exactly one request on the line.
func TestCompletionDispatchesNext(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})

	a := h.submit("a")
	b := h.submit("b")
	h.sched.sync()
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("sends = %v", got)
	}

	h.sched.HandleResponse(0, []byte("ok-a"))
	h.sched.sync()
	if r := awaitResult(t, a); r.Err != nil {
		t.Fatalf("a: %v", r.Err)
	}
	if got := h.tr.sentTxns(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("sends = %v", got)
	}
	expectPending(t, b, "b")

	h.sched.HandleResponse(1, []byte("ok-b"))
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != nil {
		t.Fatalf("b: %v", r.Err)
	}
}

// Replay: transport failure fails the request and moves on to the next.
func TestTransportErrorSkipsToNext(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: 10 * time.Second, Isolation: time.Minute})
	boom := errors.New("link down")

	a := h.submit("a")
	h.tr.mu.Lock()
	h.tr.err = boom
	h.tr.mu.Unlock()
	h.sched.sync()

	// a was already sent; time it out so the queue head retries into
	// the failing transport.
	h.clock.Advance(10 * time.Second)
	if r := awaitResult(t, a); r.Err != ErrTimeout {
		t.Fatalf("a: %v", r.Err)
	}

	b := h.submit("b")
	h.sched.sync()
	if r := awaitResult(t, b); r.Err != boom {
		t.Fatalf("b: %v", r.Err)
	}

	h.tr.mu.Lock()
	h.tr.err = nil
	h.tr.mu.Unlock()
	c := h.submit("c")
	h.sched.sync()
	h.sched.HandleResponse(2, []byte("ok-c"))
	h.sched.sync()
	if r := awaitResult(t, c); r.Err != nil {
		t.Fatalf("c: %v", r.Err)
	}
}

func TestSubmitAfterClose(t *testing.T) {
	h := newHarness(Config{TxnSpaceSize: 4, Timeout: time.Second, Isolation: time.Minute})
	h.sched.Close()
	waitClosed(t, h.sched)
	if _, err := h.sched.Submit(context.Background(), []byte("x")); err != ErrClosed {
		t.Fatalf("got %v", err)
	}
}
