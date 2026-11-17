package netsec

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTransport records writes and lets tests script write failures.
type fakeTransport struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	closed    bool
	writeFunc func(p []byte) (int, error)
}

func (f *fakeTransport) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeFunc != nil {
		return f.writeFunc(p)
	}
	return f.buf.Write(p)
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeTransport) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeTransport) written() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Len()
}

// waitForBytes blocks until n bytes have been written to the transport,
// meaning the corresponding requests are fully sent and queued.
func waitForBytes(t *testing.T, f *fakeTransport, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.written() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d written bytes, got %d", n, f.written())
		}
		time.Sleep(time.Millisecond)
	}
}

func sendAsync(q *Queue, ctx context.Context, name string, wantReply bool, payload []byte) <-chan sendResult {
	ch := make(chan sendResult, 1)
	go func() {
		ok, p, err := q.Send(ctx, name, wantReply, payload)
		ch <- sendResult{ok: ok, payload: p, err: err}
	}()
	return ch
}

type sendResult struct {
	ok      bool
	payload []byte
	err     error
}

func waitResult(t *testing.T, ch <-chan sendResult) sendResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Send to return")
		return sendResult{}
	}
}

func TestWireEncoding(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)
	defer q.Close()

	if _, _, err := q.Send(context.Background(), "tcpip-forward", false, []byte{0xde, 0xad}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := []byte{80, 0, 0, 0, 13}
	want = append(want, "tcpip-forward"...)
	want = append(want, 0, 0xde, 0xad)
	if !bytes.Equal(tr.buf.Bytes(), want) {
		t.Fatalf("wire bytes = %x, want %x", tr.buf.Bytes(), want)
	}
}

func TestRepliesMatchedInSendOrder(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)
	defer q.Close()

	first := sendAsync(q, context.Background(), "forward-a", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("forward-a", true, nil)))
	second := sendAsync(q, context.Background(), "forward-b", true, nil)

	// Both requests are fully written before any reply arrives.
	waitForBytes(t, tr, len(encodeGlobalRequest("forward-a", true, nil))+
		len(encodeGlobalRequest("forward-b", true, nil)))
	if err := q.HandleReply(true, []byte("grant-a")); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	if err := q.HandleReply(false, nil); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}

	r1 := waitResult(t, first)
	if !r1.ok || string(r1.payload) != "grant-a" || r1.err != nil {
		t.Fatalf("first request got %+v, want approval grant-a", r1)
	}
	r2 := waitResult(t, second)
	if r2.ok || r2.err != nil {
		t.Fatalf("second request got %+v, want failure", r2)
	}
}

func TestNoReplyRequestsDoNotOccupySlot(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)
	defer q.Close()

	// Fire-and-forget request must not consume the next reply.
	if _, _, err := q.Send(context.Background(), "keepalive", false, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waiting := sendAsync(q, context.Background(), "tcpip-forward", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("keepalive", false, nil))+
		len(encodeGlobalRequest("tcpip-forward", true, nil)))

	if err := q.HandleReply(true, []byte("bound")); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	r := waitResult(t, waiting)
	if !r.ok || string(r.payload) != "bound" {
		t.Fatalf("want-reply request got %+v, want approval bound", r)
	}
}

func TestCancelledRequestLateReplyNotReattributed(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)
	defer q.Close()

	ctx, cancel := context.WithCancel(context.Background())
	abandoned := sendAsync(q, ctx, "cancel-tcpip-forward", true, nil)
	// Wait until the first request is on the wire, then abandon it.
	waitForBytes(t, tr, len(encodeGlobalRequest("cancel-tcpip-forward", true, nil)))
	cancel()
	if r := waitResult(t, abandoned); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("abandoned Send returned %+v, want context.Canceled", r)
	}

	victim := sendAsync(q, context.Background(), "tcpip-forward", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("cancel-tcpip-forward", true, nil))+
		len(encodeGlobalRequest("tcpip-forward", true, nil)))

	// The late approval for the cancelled request must be consumed by its
	// tombstone, not handed to the next request.
	if err := q.HandleReply(true, []byte("stale-approval")); err != nil {
		t.Fatalf("HandleReply for late reply: %v", err)
	}
	if err := q.HandleReply(true, []byte("fresh-approval")); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	r := waitResult(t, victim)
	if !r.ok || string(r.payload) != "fresh-approval" {
		t.Fatalf("second request got %+v, want fresh-approval only", r)
	}
}

func TestPartialWriteClosesConnection(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)

	// One request is already waiting for its reply.
	blocked := sendAsync(q, context.Background(), "forward-a", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("forward-a", true, nil)))

	// The next request is only half written before failing.
	tr.mu.Lock()
	full := len(encodeGlobalRequest("forward-b", true, nil))
	tr.writeFunc = func(p []byte) (int, error) {
		return full / 2, errors.New("connection reset mid-write")
	}
	tr.mu.Unlock()

	_, _, err := q.Send(context.Background(), "forward-b", true, nil)
	if !errors.Is(err, ErrPartialWrite) {
		t.Fatalf("Send err = %v, want ErrPartialWrite", err)
	}
	if !tr.isClosed() {
		t.Fatal("transport not closed after partial write")
	}

	// The already-pending waiter is failed, not left hanging or reordered.
	if r := waitResult(t, blocked); !errors.Is(r.err, ErrPartialWrite) {
		t.Fatalf("pending waiter got %+v, want ErrPartialWrite", r)
	}
	// The queue stays poisoned; no reordered retry is possible.
	if _, _, err := q.Send(context.Background(), "forward-c", true, nil); !errors.Is(err, ErrPartialWrite) {
		t.Fatalf("Send after partial write err = %v, want ErrPartialWrite", err)
	}
}

func TestZeroByteWriteFailureKeepsQueue(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)
	defer q.Close()

	boom := errors.New("write blocked")
	tr.writeFunc = func(p []byte) (int, error) { return 0, boom }
	if _, _, err := q.Send(context.Background(), "tcpip-forward", true, nil); !errors.Is(err, boom) {
		t.Fatalf("Send err = %v, want %v", err, boom)
	}
	if tr.isClosed() {
		t.Fatal("transport closed although nothing was written")
	}

	// Nothing was sent, so the queue is still consistent and usable.
	tr.writeFunc = nil
	waiting := sendAsync(q, context.Background(), "tcpip-forward", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("tcpip-forward", true, nil)))
	if err := q.HandleReply(true, []byte("ok")); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	if r := waitResult(t, waiting); !r.ok {
		t.Fatalf("request after zero-byte failure got %+v, want approval", r)
	}
}

func TestHalfCloseFailsWaitersAndRejectsLateReply(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)

	// Peer half-closes while a request is still awaiting its reply.
	waiting := sendAsync(q, context.Background(), "tcpip-forward", true, nil)
	waitForBytes(t, tr, len(encodeGlobalRequest("tcpip-forward", true, nil)))
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if r := waitResult(t, waiting); !errors.Is(r.err, ErrQueueClosed) {
		t.Fatalf("waiter after half-close got %+v, want ErrQueueClosed", r)
	}
	// A reply dribbling in after the close must not be delivered anywhere.
	if err := q.HandleReply(true, []byte("too-late")); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("HandleReply after close = %v, want ErrQueueClosed", err)
	}
	if _, _, err := q.Send(context.Background(), "tcpip-forward", true, nil); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("Send after close err = %v, want ErrQueueClosed", err)
	}
}

func TestUnsolicitedReplyClosesConnection(t *testing.T) {
	tr := &fakeTransport{}
	q := NewQueue(tr)

	if err := q.HandleReply(true, []byte("surprise")); !errors.Is(err, ErrUnsolicitedReply) {
		t.Fatalf("HandleReply = %v, want ErrUnsolicitedReply", err)
	}
	if !tr.isClosed() {
		t.Fatal("transport not closed after unsolicited reply")
	}
}
