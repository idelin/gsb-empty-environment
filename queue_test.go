package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// memTransport records fully or partially written frames and can be told to
// fail a write after failAfter bytes.
type memTransport struct {
	mu        sync.Mutex
	frames    [][]byte
	closed    bool
	failAfter int
	failErr   error
}

func newMemTransport() *memTransport {
	return &memTransport{failAfter: -1}
}

func (m *memTransport) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAfter >= 0 && len(p) > m.failAfter {
		n := m.failAfter
		if n > len(p) {
			n = len(p)
		}
		if n > 0 {
			m.frames = append(m.frames, append([]byte(nil), p[:n]...))
		}
		return n, m.failErr
	}
	m.frames = append(m.frames, append([]byte(nil), p...))
	return len(p), nil
}

func (m *memTransport) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func (m *memTransport) snapshot() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.frames))
	for i, f := range m.frames {
		out[i] = append([]byte(nil), f...)
	}
	return out
}

func (m *memTransport) setFail(failAfter int, err error) {
	m.mu.Lock()
	m.failAfter = failAfter
	m.failErr = err
	m.mu.Unlock()
}

func waitersLen(q *Queue) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiters)
}

func waitForWaiters(t *testing.T, q *Queue, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if waitersLen(q) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d pending requests, got %d", want, waitersLen(q))
}

func portPayload(port uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, port)
	return b
}

type sendResult struct {
	approved bool
	err      error
}

// Replies carry no request id: concurrent requests must be matched strictly in
// the order their frames finished sending, never by goroutine launch order.
func TestQueueMatchesRepliesInSendCompletionOrder(t *testing.T) {
	tr := newMemTransport()
	q := NewQueue(tr)

	const n = 3
	results := make(map[uint32]sendResult)
	var resMu sync.Mutex
	var wg sync.WaitGroup
	ready := make(chan struct{}, n)

	for i := 0; i < n; i++ {
		port := uint32(1000 + i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			approved, err := q.Send(context.Background(), Request{
				Type:      "tcpip-forward",
				WantReply: true,
				Payload:   portPayload(port),
			})
			resMu.Lock()
			results[port] = sendResult{approved, err}
			resMu.Unlock()
		}()
	}
	for i := 0; i < n; i++ {
		<-ready
	}
	waitForWaiters(t, q, n)

	frames := tr.snapshot()
	if len(frames) != n {
		t.Fatalf("expected %d written frames, got %d", n, len(frames))
	}

	// Derive the real send order from the wire and assign mixed verdicts in
	// that order: approve, deny, approve.
	order := make([]uint32, n)
	verdicts := map[uint32]bool{}
	pattern := []bool{true, false, true}
	for i, frame := range frames {
		req, err := ParseGlobalRequest(frame)
		if err != nil {
			t.Fatalf("frame %d parse: %v", i, err)
		}
		if !req.WantReply || req.Type != "tcpip-forward" {
			t.Fatalf("frame %d unexpected request %+v", i, req)
		}
		port := binary.BigEndian.Uint32(req.Payload)
		order[i] = port
		verdicts[port] = pattern[i]
	}

	for _, approved := range pattern {
		if err := q.DeliverReply(approved); err != nil {
			t.Fatalf("DeliverReply: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("only %d of %d requests completed", len(results), n)
	}
	for port, want := range verdicts {
		got := results[port]
		if got.err != nil {
			t.Fatalf("port %d unexpected error: %v", port, got.err)
		}
		if got.approved != want {
			t.Fatalf("port %d approval = %v, want %v (cross-request association)", port, got.approved, want)
		}
	}
	if len(order) != n {
		t.Fatalf("send order length mismatch")
	}
}

func TestNoReplyRequestsDoNotOccupySlots(t *testing.T) {
	tr := newMemTransport()
	q := NewQueue(tr)

	noReplyDone := make(chan error, 1)
	go func() {
		_, err := q.Send(context.Background(), Request{Type: "keepalive@example.com", WantReply: false})
		noReplyDone <- err
	}()
	select {
	case err := <-noReplyDone:
		if err != nil {
			t.Fatalf("no-reply send: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no-reply Send blocked")
	}
	if got := waitersLen(q); got != 0 {
		t.Fatalf("no-reply request occupied %d reply slots", got)
	}

	// A want-reply request still gets its own slot and verdict.
	approvedCh := make(chan sendResult, 1)
	go func() {
		approved, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true})
		approvedCh <- sendResult{approved, err}
	}()
	waitForWaiters(t, q, 1)

	// Another no-reply request must not shift the pending slot.
	go func() {
		_, err := q.Send(context.Background(), Request{Type: "no-hostkey@example.com", WantReply: false})
		noReplyDone <- err
	}()
	select {
	case err := <-noReplyDone:
		if err != nil {
			t.Fatalf("second no-reply send: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second no-reply Send blocked")
	}
	if got := waitersLen(q); got != 1 {
		t.Fatalf("expected 1 pending request, got %d", got)
	}
	if err := q.DeliverReply(false); err != nil {
		t.Fatalf("DeliverReply: %v", err)
	}
	select {
	case r := <-approvedCh:
		if r.err != nil || r.approved {
			t.Fatalf("expected clean denial, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("want-reply Send never returned")
	}

	// Nothing is waiting: an unsolicited reply proves desync and must fail closed.
	err := q.DeliverReply(true)
	if !errors.Is(err, ErrUnsolicitedReply) {
		t.Fatalf("expected ErrUnsolicitedReply, got %v", err)
	}
	if !errors.Is(q.Cause(), ErrUnsolicitedReply) || !tr.closed {
		t.Fatalf("unsolicited reply did not close queue/connection: cause=%v connClosed=%v", q.Cause(), tr.closed)
	}
}

// Canceling the head request must not free its slot: the late wire reply is
// consumed and discarded so it can never authorize the next request.
func TestCanceledHeadAbsorbsLateSuccess(t *testing.T) {
	tr := newMemTransport()
	q := NewQueue(tr)

	headCtx, cancelHead := context.WithCancel(context.Background())
	headErr := make(chan error, 1)
	go func() {
		_, err := q.Send(headCtx, Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(1)})
		headErr <- err
	}()
	waitForWaiters(t, q, 1)

	tailRes := make(chan sendResult, 1)
	go func() {
		approved, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(2)})
		tailRes <- sendResult{approved, err}
	}()
	waitForWaiters(t, q, 2)

	cancelHead()
	select {
	case err := <-headErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("head err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled head did not return")
	}
	if got := waitersLen(q); got != 2 {
		t.Fatalf("cancellation freed a slot: waiters = %d, want 2", got)
	}

	// Late SUCCESS belongs to the canceled head and must be swallowed.
	if err := q.DeliverReply(true); err != nil {
		t.Fatalf("late reply: %v", err)
	}
	if got := waitersLen(q); got != 1 {
		t.Fatalf("after late reply waiters = %d, want 1", got)
	}

	// The tail receives its own denial.
	if err := q.DeliverReply(false); err != nil {
		t.Fatalf("tail reply: %v", err)
	}
	select {
	case r := <-tailRes:
		if r.err != nil || r.approved {
			t.Fatalf("tail must be denied without borrowing the head approval, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail Send never returned")
	}
	if got := waitersLen(q); got != 0 {
		t.Fatalf("expected no waiters left, got %d", got)
	}
}

// Canceling a non-head request leaves earlier requests' matching untouched.
func TestCanceledTailDoesNotDisturbHead(t *testing.T) {
	tr := newMemTransport()
	q := NewQueue(tr)

	headRes := make(chan sendResult, 1)
	go func() {
		approved, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(1)})
		headRes <- sendResult{approved, err}
	}()
	waitForWaiters(t, q, 1)

	tailCtx, cancelTail := context.WithCancel(context.Background())
	tailErr := make(chan error, 1)
	go func() {
		_, err := q.Send(tailCtx, Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(2)})
		tailErr <- err
	}()
	waitForWaiters(t, q, 2)

	cancelTail()
	select {
	case err := <-tailErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tail err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled tail did not return")
	}

	// Head's own success still maps to the head.
	if err := q.DeliverReply(true); err != nil {
		t.Fatalf("head reply: %v", err)
	}
	select {
	case r := <-headRes:
		if r.err != nil || !r.approved {
			t.Fatalf("head should be approved, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("head Send never returned")
	}

	// The detached tail slot still consumes its own reply, leaving an empty queue.
	if err := q.DeliverReply(false); err != nil {
		t.Fatalf("detached tail reply: %v", err)
	}
	if got := waitersLen(q); got != 0 {
		t.Fatalf("expected empty queue, got %d waiters", got)
	}
}

var errBrokenPipe = errors.New("broken pipe")

func TestPartialWriteClosesConnectionAndFailsPending(t *testing.T) {
	tr := newMemTransport()
	q := NewQueue(tr)

	pending := make(chan sendResult, 1)
	go func() {
		approved, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(1)})
		pending <- sendResult{approved, err}
	}()
	waitForWaiters(t, q, 1)

	tr.setFail(2, errBrokenPipe)
	_, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(2)})
	if err == nil || !errors.Is(err, errBrokenPipe) {
		t.Fatalf("partial-write Send err = %v, want broken pipe", err)
	}
	if !tr.closed {
		t.Fatal("partial write did not close the connection")
	}
	if q.Cause() == nil {
		t.Fatal("queue recorded no failure cause")
	}

	select {
	case r := <-pending:
		if r.approved || r.err == nil || !errors.Is(r.err, ErrQueueClosed) {
			t.Fatalf("pending request should fail closed, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request was not failed")
	}

	if _, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true}); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("Send after failure = %v, want ErrQueueClosed", err)
	}
	if err := q.DeliverReply(true); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("DeliverReply after failure = %v, want ErrQueueClosed", err)
	}

	// Idempotent: a later Close must not overwrite the original cause.
	q.Close(nil)
	if !errors.Is(q.Cause(), errBrokenPipe) {
		t.Fatalf("cause overwritten: %v", q.Cause())
	}
}

// A short write with a nil error is still fatal and must not be retried.
func TestShortWriteWithNilErrorIsFatal(t *testing.T) {
	tr := newMemTransport()
	tr.setFail(1, nil)
	q := NewQueue(tr)

	_, err := q.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err = %v, want io.ErrShortWrite", err)
	}
	if !tr.closed {
		t.Fatal("short write did not close the connection")
	}
	if frames := tr.snapshot(); len(frames) != 1 || len(frames[0]) != 1 {
		t.Fatalf("expected exactly one truncated frame, got %d", len(frames))
	}
	// The failed frame must not have been retried or requeued.
	if got := waitersLen(q); got != 0 {
		t.Fatalf("failed request should occupy no slot, got %d", got)
	}
}

// framedConn turns a net.Conn into length-prefixed SSH packets so the queue
// can be exercised over a real, half-closable connection.
type framedConn struct {
	conn net.Conn
}

func (f *framedConn) Write(p []byte) (int, error) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(p)))
	if _, err := f.conn.Write(lenBuf[:]); err != nil {
		return 0, err
	}
	n, err := f.conn.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return len(p), nil
}

func (f *framedConn) Close() error { return f.conn.Close() }

func readFrame(conn net.Conn) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(conn net.Conn, payload []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

// End-to-end half-close: when the peer shuts down its write side the host
// read loop hits EOF and must fail pending requests instead of leaving them
// able to inherit another request's approval.
func TestHalfCloseFailsPendingWithoutApprovalBleed(t *testing.T) {
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()

	queue := NewQueue(&framedConn{conn: host})

	// Peer: approve the first request, then half-close its write side after
	// the second request frame arrives.
	peerDone := make(chan error, 2)
	go func() {
		frame, err := readFrame(peer)
		if err != nil {
			peerDone <- err
			return
		}
		req, err := ParseGlobalRequest(frame)
		if err != nil {
			peerDone <- err
			return
		}
		if req.Type != "tcpip-forward" {
			peerDone <- errors.New("unexpected first request: " + req.Type)
			return
		}
		if err := writeFrame(peer, MarshalReply(true)); err != nil {
			peerDone <- err
			return
		}
		if _, err := readFrame(peer); err != nil {
			peerDone <- err
			return
		}
		if c, ok := peer.(interface{ CloseWrite() error }); ok {
			peerDone <- c.CloseWrite()
		} else {
			peerDone <- peer.Close()
		}
	}()

	// Host read loop: feed replies until the half-close surfaces as EOF.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			msg, err := readFrame(host)
			if err != nil {
				queue.Close(err)
				return
			}
			approved, ok := ParseReply(msg)
			if !ok {
				queue.Close(errors.New("host: unknown reply message"))
				return
			}
			if err := queue.DeliverReply(approved); err != nil {
				queue.Close(err)
				return
			}
		}
	}()

	// First request is genuinely approved by the peer.
	first := make(chan sendResult, 1)
	go func() {
		approved, err := queue.Send(context.Background(), Request{
			Type: "tcpip-forward", WantReply: true, Payload: portPayload(6000),
		})
		first <- sendResult{approved, err}
	}()
	select {
	case r := <-first:
		if r.err != nil || !r.approved {
			t.Fatalf("first request should be approved, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request never resolved")
	}

	// Second request is in flight when the peer half-closes: it must fail,
	// never be confused with the first approval.
	second := make(chan sendResult, 1)
	go func() {
		approved, err := queue.Send(context.Background(), Request{
			Type: "tcpip-forward", WantReply: true, Payload: portPayload(7000),
		})
		second <- sendResult{approved, err}
	}()

	select {
	case r := <-second:
		if r.approved {
			t.Fatalf("half-closed request must not be approved, got %+v", r)
		}
		if r.err == nil {
			t.Fatal("half-closed request must return an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request did not fail after half-close")
	}

	select {
	case err := <-peerDone:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("peer error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer goroutine stalled")
	}

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("host read loop did not stop on half-close")
	}

	if queue.Cause() == nil {
		t.Fatal("queue should record the half-close cause")
	}
	if _, err := queue.Send(context.Background(), Request{Type: "tcpip-forward", WantReply: true}); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("Send after half-close = %v, want ErrQueueClosed", err)
	}
}

func TestWireRoundTrip(t *testing.T) {
	req := Request{Type: "tcpip-forward", WantReply: true, Payload: portPayload(8080)}
	frame := MarshalGlobalRequest(req)
	parsed, err := ParseGlobalRequest(frame)
	if err != nil {
		t.Fatalf("ParseGlobalRequest: %v", err)
	}
	if parsed.Type != req.Type || parsed.WantReply != req.WantReply || len(parsed.Payload) != len(req.Payload) {
		t.Fatalf("round trip mismatch: %+v", parsed)
	}
	if binary.BigEndian.Uint32(parsed.Payload) != 8080 {
		t.Fatal("payload not preserved")
	}
	if approved, ok := ParseReply(MarshalReply(true)); !ok || !approved {
		t.Fatal("success reply round trip failed")
	}
	if approved, ok := ParseReply(MarshalReply(false)); !ok || approved {
		t.Fatal("failure reply round trip failed")
	}
	if _, ok := ParseReply([]byte{SSH_MSG_GLOBAL_REQUEST}); ok {
		t.Fatal("non-reply message accepted")
	}
}
