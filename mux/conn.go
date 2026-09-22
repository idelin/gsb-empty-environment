package mux

import (
	"encoding/binary"
	"io"
	"sync"
)

const (
	defaultInitialWindow = 65535
	defaultConnWindow    = 1 << 20
	defaultWriteBudget   = 1 << 16
)

// Config configures a Conn. Both peers are assumed to use the same
// InitialWindow/ConnWindow (there is no settings negotiation).
type Config struct {
	// Server selects the stream-ID parity: clients use odd IDs, servers even.
	Server bool
	// InitialWindow is the per-stream receive window advertised to the peer.
	InitialWindow int64
	// ConnWindow is the connection-level receive window.
	ConnWindow int64
	// MaxFramePayload is the receive-side frame size quota.
	MaxFramePayload int
	// WriteBudget caps the total bytes (control + data) emitted per
	// scheduling cycle. Control frames are drained first within the budget.
	WriteBudget int

	// OnNewStream is called (in a new goroutine) for each peer-opened stream.
	OnNewStream func(s *Stream)
	// OnStreamError is called when a stream is torn down by a protocol
	// violation such as data received after close.
	OnStreamError func(s *Stream, err *StreamError)
	// OnLateData is called when data arrives for a fully closed stream; the
	// peer is answered with RST_STREAM(STREAM_CLOSED) per protocol.
	OnLateData func(streamID uint32, n int)
}

type Conn struct {
	r io.Reader
	w io.Writer

	cfg Config

	mu             sync.Mutex
	streams        map[uint32]*Stream
	order          []uint32 // stream IDs in creation order, for round-robin
	nextID         uint32
	ctrl           [][]byte // high-priority queue of encoded control frames
	connSendWindow int64
	connRecvWindow int64
	connConsumed   uint64
	connPeerWM     uint64
	closed         bool
	serveErr       error

	notify    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// NewConn creates a connection and starts its background writer goroutine.
// Serve must be called to drive the read side.
func NewConn(r io.Reader, w io.Writer, cfg Config) *Conn {
	c := newConn(cfg)
	c.r = r
	c.w = w
	go c.writerLoop()
	return c
}

// newConn builds a Conn without I/O; used by NewConn and by unit tests that
// drive collectLocked directly.
func newConn(cfg Config) *Conn {
	if cfg.InitialWindow <= 0 {
		cfg.InitialWindow = defaultInitialWindow
	}
	if cfg.ConnWindow <= 0 {
		cfg.ConnWindow = defaultConnWindow
	}
	if cfg.MaxFramePayload <= 0 {
		cfg.MaxFramePayload = defaultMaxPayload
	}
	if cfg.WriteBudget <= 0 {
		cfg.WriteBudget = defaultWriteBudget
	}
	c := &Conn{
		cfg:            cfg,
		streams:        make(map[uint32]*Stream),
		connSendWindow: cfg.ConnWindow,
		connRecvWindow: cfg.ConnWindow,
		notify:         make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	if cfg.Server {
		c.nextID = 2
	} else {
		c.nextID = 1
	}
	return c
}

// OpenStream creates a locally-initiated stream.
func (c *Conn) OpenStream() (*Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrConnClosed
	}
	id := c.nextID
	c.nextID += 2
	return c.newStreamLocked(id), nil
}

func (c *Conn) newStreamLocked(id uint32) *Stream {
	s := &Stream{
		id:         id,
		c:          c,
		sendWindow: c.cfg.InitialWindow,
		recvWindow: c.cfg.InitialWindow,
	}
	s.cond.L = &c.mu
	c.streams[id] = s
	c.order = append(c.order, id)
	return s
}

func (c *Conn) validPeerStreamID(id uint32) bool {
	if id == 0 {
		return false
	}
	if c.cfg.Server {
		return id%2 == 1
	}
	return id%2 == 0
}

// Serve reads frames from the connection until a protocol error or EOF.
func (c *Conn) Serve() error {
	p := NewParser(c.cfg.MaxFramePayload)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := c.r.Read(buf)
		if n > 0 {
			p.Feed(buf[:n])
			for {
				f, err := p.Next()
				if err == ErrNeedMore {
					break
				}
				if err != nil {
					c.fail(err)
					return err
				}
				if err := c.handleFrame(f); err != nil {
					c.fail(err)
					return err
				}
			}
		}
		if rerr != nil {
			c.fail(rerr)
			return rerr
		}
	}
}

// Close shuts the connection down.
func (c *Conn) Close() error {
	c.fail(ErrConnClosed)
	return nil
}

func (c *Conn) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.serveErr = err
		c.closed = true
		for _, s := range c.streams {
			s.cond.Broadcast()
		}
		c.mu.Unlock()
		close(c.done)
		if cl, ok := c.w.(io.Closer); ok {
			cl.Close()
		}
	})
}

// SendRawFrame queues a raw frame on the high-priority control queue,
// bypassing stream state and flow control. It exists so tests and demos can
// exercise protocol edge cases (duplicate WINDOW_UPDATE, data after close).
func (c *Conn) SendRawFrame(f *Frame) {
	c.mu.Lock()
	c.queueCtrlLocked(f)
	c.mu.Unlock()
}

type callbacks struct {
	newStream *Stream
	errStream *Stream
	err       *StreamError
	late      bool
	lateID    uint32
	lateN     int
}

func (c *Conn) handleFrame(f *Frame) error {
	var cb callbacks
	c.mu.Lock()
	var connErr error
	switch f.Type {
	case FrameData:
		connErr = c.handleDataLocked(f, &cb)
	case FrameWindowUpdate:
		c.handleWindowUpdateLocked(f)
	case FrameRstStream:
		c.handleRstLocked(f, &cb)
	}
	c.mu.Unlock()
	if cb.newStream != nil && c.cfg.OnNewStream != nil {
		go c.cfg.OnNewStream(cb.newStream)
	}
	if cb.errStream != nil && c.cfg.OnStreamError != nil {
		c.cfg.OnStreamError(cb.errStream, cb.err)
	}
	if cb.late && c.cfg.OnLateData != nil {
		c.cfg.OnLateData(cb.lateID, cb.lateN)
	}
	return connErr
}

func (c *Conn) handleDataLocked(f *Frame, cb *callbacks) error {
	sid := f.StreamID
	s := c.streams[sid]
	if s == nil {
		if !c.validPeerStreamID(sid) {
			return &ProtocolError{Code: ErrCodeProtocol, Msg: "DATA on unknown stream"}
		}
		s = c.newStreamLocked(sid)
		cb.newStream = s
	}
	switch s.state {
	case StateHalfClosedRemote:
		// Data after the peer half-closed: stream error per protocol.
		se := &StreamError{Code: ErrCodeStreamClosed, Msg: "data received after remote close"}
		s.rstErr = se
		s.state = StateClosed
		c.queueCtrlLocked(&Frame{Type: FrameRstStream, StreamID: sid, Payload: rstPayload(ErrCodeStreamClosed)})
		s.cond.Broadcast()
		cb.errStream = s
		cb.err = se
		return nil
	case StateClosed:
		// Late data for a fully closed stream: reject per protocol.
		c.queueCtrlLocked(&Frame{Type: FrameRstStream, StreamID: sid, Payload: rstPayload(ErrCodeStreamClosed)})
		cb.late = true
		cb.lateID = sid
		cb.lateN = len(f.Payload)
		return nil
	}
	n := int64(len(f.Payload))
	if n > s.recvWindow || n > c.connRecvWindow {
		return &ProtocolError{Code: ErrCodeFlowControl, Msg: "receive window exceeded"}
	}
	s.recvWindow -= n
	c.connRecvWindow -= n
	if n > 0 {
		s.recvBuf = append(s.recvBuf, f.Payload...)
	}
	if f.Flags&FlagEndStream != 0 {
		s.recvEnd = true
		switch s.state {
		case StateOpen:
			s.state = StateHalfClosedRemote
		case StateHalfClosedLocal:
			s.state = StateClosed
		}
	}
	s.cond.Broadcast()
	return nil
}

func (c *Conn) handleWindowUpdateLocked(f *Frame) {
	wm := binary.BigEndian.Uint64(f.Payload)
	if f.StreamID == 0 {
		if wm <= c.connPeerWM {
			return // duplicate or stale: must not extend the window twice
		}
		c.connSendWindow += int64(wm - c.connPeerWM)
		c.connPeerWM = wm
	} else {
		s := c.streams[f.StreamID]
		if s == nil {
			return
		}
		if wm <= s.peerWatermark {
			return // duplicate or stale
		}
		s.sendWindow += int64(wm - s.peerWatermark)
		s.peerWatermark = wm
	}
	c.pokeLocked()
}

func (c *Conn) handleRstLocked(f *Frame, cb *callbacks) {
	s := c.streams[f.StreamID]
	if s == nil || s.state == StateClosed {
		return
	}
	code := ErrCode(binary.BigEndian.Uint32(f.Payload))
	s.rstErr = &StreamError{Code: code, Msg: "reset by peer"}
	s.state = StateClosed
	s.cond.Broadcast()
	cb.errStream = s
	cb.err = s.rstErr
}

func (c *Conn) pokeLocked() {
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *Conn) queueCtrlLocked(f *Frame) {
	c.ctrl = append(c.ctrl, encodeFrame(f))
	c.pokeLocked()
}

// collectLocked assembles up to WriteBudget bytes of output. Control frames
// go first (still bounded by the budget), then DATA frames round-robin over
// streams that have both stream-level and connection-level credit, then
// deferred END_STREAM markers.
func (c *Conn) collectLocked() []byte {
	budget := c.cfg.WriteBudget
	var out []byte

	for len(c.ctrl) > 0 {
		next := c.ctrl[0]
		if len(out)+len(next) > budget && len(out) > 0 {
			break
		}
		out = append(out, next...)
		c.ctrl = c.ctrl[1:]
	}

dataLoop:
	for {
		progressed := false
		for _, id := range c.order {
			s := c.streams[id]
			if s == nil || len(s.sendBuf) == 0 || s.sendWindow <= 0 || c.connSendWindow <= 0 {
				continue
			}
			avail := budget - len(out) - frameHeaderLen
			if avail <= 0 {
				if len(out) == 0 {
					avail = 1 // always make progress, even if budget is tiny
				} else {
					break dataLoop
				}
			}
			n := min64(int64(len(s.sendBuf)), s.sendWindow, c.connSendWindow,
				int64(c.cfg.MaxFramePayload), int64(avail))
			payload := s.sendBuf[:n]
			out = append(out, encodeFrame(&Frame{Type: FrameData, StreamID: id, Payload: payload})...)
			s.sendBuf = s.sendBuf[n:]
			if len(s.sendBuf) == 0 {
				s.sendBuf = nil
			}
			s.sendWindow -= n
			c.connSendWindow -= n
			progressed = true
		}
		if !progressed {
			break
		}
	}

	for _, id := range c.order {
		s := c.streams[id]
		if s == nil || !s.endStream || s.endStreamSent || len(s.sendBuf) > 0 {
			continue
		}
		if len(out)+frameHeaderLen > budget && len(out) > 0 {
			continue // next cycle
		}
		out = append(out, encodeFrame(&Frame{Type: FrameData, Flags: FlagEndStream, StreamID: id})...)
		s.endStreamSent = true
		switch s.state {
		case StateOpen:
			s.state = StateHalfClosedLocal
		case StateHalfClosedRemote:
			s.state = StateClosed
		}
		s.cond.Broadcast()
	}
	return out
}

func (c *Conn) writerLoop() {
	for {
		c.mu.Lock()
		chunk := c.collectLocked()
		closed := c.closed
		c.mu.Unlock()
		if len(chunk) > 0 {
			if _, err := c.w.Write(chunk); err != nil {
				c.fail(err)
				return
			}
			continue
		}
		if closed {
			return
		}
		select {
		case <-c.notify:
		case <-c.done:
		}
	}
}

func min64(v int64, rest ...int64) int64 {
	for _, x := range rest {
		if x < v {
			v = x
		}
	}
	return v
}
