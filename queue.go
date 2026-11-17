package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// msgGlobalRequest is the SSH message number for global requests (RFC 4254 §8).
const msgGlobalRequest = 80

// ErrQueueClosed is returned by Send after the queue has been shut down.
var ErrQueueClosed = errors.New("netsec: request queue closed")

// ErrPartialWrite is returned when a request message was only partially
// written to the transport. The connection is closed because the peer will
// parse a truncated message and every later reply would shift by one request.
var ErrPartialWrite = errors.New("netsec: partial write of global request")

// ErrUnsolicitedReply is returned by HandleReply when a reply arrives but no
// sent request is waiting for it. The connection is closed because the peer
// is out of sync with the queue.
var ErrUnsolicitedReply = errors.New("netsec: reply with no pending request")

// Transport is the underlying SSH connection used by Queue. Writes must be
// serialized by the caller; Queue guarantees this by holding its lock while
// writing.
type Transport interface {
	io.Writer
	io.Closer
}

// slot tracks one fully sent want-reply request until its reply is matched.
type slot struct {
	name      string
	done      chan struct{}
	ok        bool
	payload   []byte
	err       error
	cancelled bool // waiter gave up; the late reply is still consumed in order
}

// Queue matches SSH global-request replies to requests. Successful replies
// carry no request identifier, so they are matched strictly in the order the
// requests finished writing to the wire. Requests sent without wantReply
// never occupy a slot.
type Queue struct {
	mu      sync.Mutex
	tr      Transport
	pending []*slot
	closed  bool
	err     error
}

// NewQueue returns a Queue sending global requests over tr.
func NewQueue(tr Transport) *Queue {
	return &Queue{tr: tr}
}

// encodeGlobalRequest builds the wire form of SSH_MSG_GLOBAL_REQUEST:
// byte 80, string name, boolean want-reply, then the opaque payload.
func encodeGlobalRequest(name string, wantReply bool, payload []byte) []byte {
	msg := make([]byte, 0, 1+4+len(name)+1+len(payload))
	msg = append(msg, msgGlobalRequest)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(name)))
	msg = append(msg, lenBuf[:]...)
	msg = append(msg, name...)
	if wantReply {
		msg = append(msg, 1)
	} else {
		msg = append(msg, 0)
	}
	return append(msg, payload...)
}

// Send writes one global request and, when wantReply is true, waits for the
// reply matched to it. The returned ok/payload are meaningful only when
// wantReply is true. If ctx is cancelled while waiting, the request's slot
// stays queued as a tombstone so its late reply is consumed and discarded
// instead of being attributed to the next request.
func (q *Queue) Send(ctx context.Context, name string, wantReply bool, payload []byte) (bool, []byte, error) {
	msg := encodeGlobalRequest(name, wantReply, payload)

	q.mu.Lock()
	if q.closed {
		err := q.err
		q.mu.Unlock()
		return false, nil, err
	}
	n, err := q.tr.Write(msg)
	switch {
	case err == nil && n == len(msg):
		// Fully sent; enqueue below.
	case n > 0:
		// Partial write desynchronizes the peer: close rather than reorder.
		perr := fmt.Errorf("%w: wrote %d of %d bytes: %v", ErrPartialWrite, n, len(msg), err)
		q.closeLocked(perr)
		q.mu.Unlock()
		return false, nil, perr
	default:
		// Nothing reached the wire; the queue is still consistent.
		q.mu.Unlock()
		if err == nil {
			err = io.ErrShortWrite
		}
		return false, nil, err
	}

	if !wantReply {
		q.mu.Unlock()
		return false, nil, nil
	}
	s := &slot{name: name, done: make(chan struct{})}
	q.pending = append(q.pending, s)
	q.mu.Unlock()

	select {
	case <-s.done:
		q.mu.Lock()
		defer q.mu.Unlock()
		if s.err != nil {
			return false, nil, s.err
		}
		return s.ok, s.payload, nil
	case <-ctx.Done():
		q.mu.Lock()
		s.cancelled = true
		q.mu.Unlock()
		return false, nil, ctx.Err()
	}
}

// HandleReply delivers one reply (SSH_MSG_REQUEST_SUCCESS with ok=true, or
// SSH_MSG_REQUEST_FAILURE with ok=false) from the host read loop to the
// oldest sent request still waiting. A reply with no pending request is a
// protocol violation and closes the connection.
func (q *Queue) HandleReply(ok bool, payload []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return q.err
	}
	if len(q.pending) == 0 {
		q.closeLocked(ErrUnsolicitedReply)
		return ErrUnsolicitedReply
	}
	s := q.pending[0]
	q.pending[0] = nil
	q.pending = q.pending[1:]
	if s.cancelled {
		// Late reply for an abandoned request: consume it so it cannot be
		// attributed to the next pending request.
		return nil
	}
	s.ok = ok
	s.payload = append([]byte(nil), payload...)
	close(s.done)
	return nil
}

// Close shuts the queue, fails every pending waiter, and closes the transport.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeLocked(ErrQueueClosed)
	return nil
}

// closeLocked poisons the queue and closes the transport. Callers hold q.mu.
func (q *Queue) closeLocked(err error) {
	if q.closed {
		return
	}
	q.closed = true
	q.err = err
	for _, s := range q.pending {
		if !s.cancelled {
			s.err = err
			close(s.done)
		}
	}
	q.pending = nil
	q.tr.Close()
}
