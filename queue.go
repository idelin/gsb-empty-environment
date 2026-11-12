package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrQueueClosed is returned by Send and DeliverReply after the queue has
// been shut down, either because the connection died or Close was called.
var ErrQueueClosed = errors.New("netsec: request queue is closed")

// ErrUnsolicitedReply is returned when a success/failure reply arrives but no
// request expecting a reply is in flight. The queue shuts down in that case
// because reply/request ordering can no longer be trusted.
var ErrUnsolicitedReply = errors.New("netsec: reply received without a matching request")

// Transport is the write side of the SSH connection owned by the session
// host. The queue serializes all writes through this transport.
type Transport interface {
	Write(p []byte) (int, error)
	Close() error
}

// Request is an SSH global request (RFC 4254, section 4), for example a
// tcpip-forward request. Replies to such requests carry no request identifier,
// so they can only be matched in the order the requests finished sending.
type Request struct {
	Type      string
	WantReply bool
	Payload   []byte
}

type outcome struct {
	approved bool
	err      error
}

// waiter is one occupied reply slot. A slot exists only for requests that
// were fully written with WantReply set.
type waiter struct {
	req Request
	ch  chan outcome
	// detached means the caller stopped waiting (context cancellation) but the
	// slot is retained: the wire reply is still coming and must be consumed in
	// order instead of being handed to the next request.
	detached bool
}

// Queue serializes global requests on one SSH connection and matches
// SSH_MSG_REQUEST_SUCCESS / SSH_MSG_REQUEST_FAILURE replies to pending
// requests strictly in send-completion order.
type Queue struct {
	mu      sync.Mutex
	conn    Transport
	closed  bool
	cause   error
	waiters []*waiter
}

// NewQueue creates a request queue bound to the host's connection.
func NewQueue(conn Transport) *Queue {
	return &Queue{conn: conn}
}

// Send writes one global request. When req.WantReply is false it returns as
// soon as the write completes and occupies no reply slot. When true it blocks
// until the matching reply arrives: approved reflects SSH_MSG_REQUEST_SUCCESS.
//
// A partial or failed write is fatal: the connection is closed, every pending
// request fails with the write error, and the queue never reorders or retries.
func (q *Queue) Send(ctx context.Context, req Request) (approved bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	frame := MarshalGlobalRequest(req)

	q.mu.Lock()
	if q.closed {
		err := q.closedErr()
		q.mu.Unlock()
		return false, err
	}

	n, writeErr := q.conn.Write(frame)
	if writeErr != nil || n != len(frame) {
		writeErr = writeFailure(frame, n, writeErr)
		q.shutdownLocked(writeErr)
		q.mu.Unlock()
		return false, writeErr
	}

	if !req.WantReply {
		q.mu.Unlock()
		return false, nil
	}

	w := &waiter{req: req, ch: make(chan outcome, 1)}
	q.waiters = append(q.waiters, w)
	q.mu.Unlock()

	select {
	case res := <-w.ch:
		return res.approved, res.err
	case <-ctx.Done():
		// Keep the slot so the late reply is drained in order rather than
		// being associated with the next pending request.
		q.mu.Lock()
		w.detached = true
		q.mu.Unlock()
		return false, ctx.Err()
	}
}

// DeliverReply feeds one SSH_MSG_REQUEST_SUCCESS (approved == true) or
// SSH_MSG_REQUEST_FAILURE (approved == false) message from the host's read
// loop into the queue. The reply is always matched to the oldest unsent reply
// slot, i.e. the oldest request that fully completed sending with WantReply.
func (q *Queue) DeliverReply(approved bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return q.closedErr()
	}
	if len(q.waiters) == 0 {
		// Desynchronized stream: failing closed is the only safe option.
		q.shutdownLocked(ErrUnsolicitedReply)
		return ErrUnsolicitedReply
	}

	w := q.waiters[0]
	q.waiters = q.waiters[1:]
	if w.detached {
		// Late reply for a canceled request; discard it to preserve the
		// one-to-one FIFO mapping between requests and replies.
		return nil
	}
	w.ch <- outcome{approved: approved}
	return nil
}

// Close shuts the queue down, failing every pending request with cause. It is
// safe to call from the read loop on EOF or a decode error and is idempotent.
func (q *Queue) Close(cause error) {
	if cause == nil {
		cause = ErrQueueClosed
	}
	q.mu.Lock()
	q.shutdownLocked(cause)
	q.mu.Unlock()
}

// Cause returns the error that closed the queue, or nil while it is open.
func (q *Queue) Cause() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cause
}

// shutdownLocked marks the queue dead, fails all live waiters and closes the
// underlying connection. Detached waiters are dropped silently.
func (q *Queue) shutdownLocked(cause error) {
	if q.closed {
		return
	}
	q.closed = true
	q.cause = cause
	closedErr := closedWithCause(cause)
	for _, w := range q.waiters {
		if !w.detached {
			w.ch <- outcome{err: closedErr}
		}
	}
	q.waiters = nil
	_ = q.conn.Close()
}

func (q *Queue) closedErr() error {
	return closedWithCause(q.cause)
}

func closedWithCause(cause error) error {
	if cause == nil {
		return ErrQueueClosed
	}
	return fmt.Errorf("%w: %w", ErrQueueClosed, cause)
}

func writeFailure(frame []byte, n int, err error) error {
	if err == nil {
		err = io.ErrShortWrite
	}
	return fmt.Errorf("global request write stopped after %d of %d bytes: %w", n, len(frame), err)
}
