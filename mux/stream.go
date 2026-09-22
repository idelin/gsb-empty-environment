package mux

import (
	"io"
	"sync"
)

type StreamState int

const (
	StateOpen StreamState = iota
	StateHalfClosedLocal
	StateHalfClosedRemote
	StateClosed
)

func (st StreamState) String() string {
	switch st {
	case StateOpen:
		return "open"
	case StateHalfClosedLocal:
		return "half-closed(local)"
	case StateHalfClosedRemote:
		return "half-closed(remote)"
	case StateClosed:
		return "closed"
	}
	return "unknown"
}

// Stream is one logical flow on a Conn. It implements io.ReadWriteCloser.
type Stream struct {
	id uint32
	c  *Conn

	state         StreamState
	sendWindow    int64
	recvWindow    int64
	consumed      uint64 // recv watermark advertised to the peer
	peerWatermark uint64 // last watermark applied from the peer

	sendBuf       []byte
	endStream     bool
	endStreamSent bool

	recvBuf []byte
	recvEnd bool
	rstErr  *StreamError

	cond sync.Cond // L == &c.mu
}

func (s *Stream) ID() uint32 { return s.id }

func (s *Stream) State() StreamState {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.state
}

// SendWindow reports the remaining send credit for this stream.
func (s *Stream) SendWindow() int64 {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.sendWindow
}

// Consumed reports the receive watermark advertised to the peer.
func (s *Stream) Consumed() uint64 {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.consumed
}

// Write queues p for transmission. It never blocks on flow control; the
// scheduler drains the queue as window credit allows.
func (s *Stream) Write(p []byte) (int, error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.rstErr != nil {
		return 0, s.rstErr
	}
	if s.endStream || s.state == StateHalfClosedLocal || s.state == StateClosed {
		return 0, ErrStreamClosed
	}
	s.sendBuf = append(s.sendBuf, p...)
	c.pokeLocked()
	return len(p), nil
}

// Read consumes received data. It blocks until data is available, the remote
// half-closes (io.EOF once drained), or the stream/connection fails.
// Consuming data advances the receive watermark and queues WINDOW_UPDATE
// frames for the stream and the connection.
func (s *Stream) Read(p []byte) (int, error) {
	c := s.c
	c.mu.Lock()
	for len(s.recvBuf) == 0 && !s.recvEnd && s.rstErr == nil && c.serveErr == nil {
		s.cond.Wait()
	}
	if len(s.recvBuf) == 0 {
		var err error
		switch {
		case s.rstErr != nil:
			err = s.rstErr
		case s.recvEnd:
			err = io.EOF
		default:
			err = c.serveErr
		}
		c.mu.Unlock()
		return 0, err
	}
	n := copy(p, s.recvBuf)
	s.recvBuf = s.recvBuf[n:]
	if len(s.recvBuf) == 0 {
		s.recvBuf = nil
	}
	s.consumed += uint64(n)
	s.recvWindow += int64(n)
	c.connConsumed += uint64(n)
	c.connRecvWindow += int64(n)
	c.queueCtrlLocked(&Frame{Type: FrameWindowUpdate, StreamID: s.id, Payload: WindowUpdatePayload(s.consumed)})
	c.queueCtrlLocked(&Frame{Type: FrameWindowUpdate, StreamID: 0, Payload: WindowUpdatePayload(c.connConsumed)})
	c.mu.Unlock()
	return n, nil
}

// CloseWrite half-closes the send side: a DATA frame with END_STREAM is sent
// once the queued bytes are flushed. The read side stays open.
func (s *Stream) CloseWrite() error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.rstErr != nil {
		return s.rstErr
	}
	if s.endStream {
		return nil
	}
	s.endStream = true
	c.pokeLocked()
	return nil
}

// Reset aborts the stream in both directions with the given error code.
func (s *Stream) Reset(code ErrCode) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.state == StateClosed {
		return nil
	}
	s.state = StateClosed
	s.rstErr = &StreamError{Code: code, Msg: "stream reset locally"}
	c.queueCtrlLocked(&Frame{Type: FrameRstStream, StreamID: s.id, Payload: rstPayload(code)})
	s.cond.Broadcast()
	return nil
}

// Close resets the stream with NO_ERROR.
func (s *Stream) Close() error { return s.Reset(ErrCodeNo) }
