package netsec

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// MaxWindow is the largest legal flow-control window. Window counters are
// uint32, so an update that would push a window past this value overflows.
const MaxWindow = uint64(^uint32(0))

var (
	// ErrWindowOverflow is reported for a WINDOW_ADJUST that would make a
	// channel window exceed the uint32 range. The offending channel is
	// terminated; other channels on the same connection keep running.
	ErrWindowOverflow = errors.New("netsec: window update exceeds uint32 range")
	// ErrRecvWindowOverflow is reported when inbound DATA exceeds the
	// receiver window. The offending channel alone is terminated.
	ErrRecvWindowOverflow = errors.New("netsec: inbound data exceeds receive window")
	// ErrChannelClosed is reported after a channel has been fully closed.
	ErrChannelClosed = errors.New("netsec: channel closed")
	// ErrDataAfterEOF is reported when DATA arrives after an EOF.
	ErrDataAfterEOF = errors.New("netsec: data received after EOF")
	// ErrUnknownChannel is reported for frames referencing an unknown id.
	ErrUnknownChannel = errors.New("netsec: unknown channel")
)

// FrameType identifies the virtual frame kinds produced by the flow layer.
type FrameType uint8

const (
	FrameData FrameType = iota + 1
	FrameEOF
	FrameClose
	FrameWindowAdjust
)

// Frame is one unit handed to the transport host. For FrameData, Payload is
// the on-wire data and N is its length. For FrameWindowAdjust, N carries the
// uint32 adjustment amount. EOF and CLOSE frames carry no payload.
type Frame struct {
	ChannelID uint32
	Type      FrameType
	Payload   []byte
	N         uint32
}

// FrameWriter is the virtual transport sink. WriteFrame returns the number of
// payload bytes actually delivered: n equals len(f.Payload) for a complete
// write, or a smaller value for a partial write. A non-nil err with n below
// the payload length terminates the emitting channel after accounting for the
// delivered prefix only.
//
// Implementations must not re-enter the channel while WriteFrame is running;
// the channel lock is held for the duration of the call.
type FrameWriter interface {
	WriteFrame(f Frame) (n int, err error)
}

// FlowConn is a multiplexed transport connection that owns many independent
// channels. A misbehaving channel can only terminate itself; its window
// accounting never touches another channel's state.
type FlowConn struct {
	mu       sync.Mutex
	channels map[uint32]*Channel
	writer   FrameWriter
}

// NewFlowConn creates a connection bound to the given frame sink.
func NewFlowConn(w FrameWriter) *FlowConn {
	return &FlowConn{channels: make(map[uint32]*Channel), writer: w}
}

// OpenChannel registers a fresh channel. sendWindow/recvWindow are the
// initial credit in bytes and maxPacket caps every individual DATA frame.
func (c *FlowConn) OpenChannel(id, sendWindow, recvWindow, maxPacket uint32) (*Channel, error) {
	if maxPacket == 0 {
		return nil, errors.New("netsec: max packet size must be non-zero")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.channels[id]; ok {
		return nil, errors.New("netsec: duplicate channel id")
	}
	ch := &Channel{
		conn:       c,
		id:         id,
		maxPacket:  maxPacket,
		sendWindow: uint64(sendWindow),
		recvWindow: uint64(recvWindow),
		recvInit:   uint64(recvWindow),
		cond:       sync.NewCond(&c.mu),
	}
	c.channels[id] = ch
	return ch, nil
}

// Channel returns an open channel by id, or nil if none exists.
func (c *FlowConn) Channel(id uint32) *Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channels[id]
}

// Channel is a single byte stream with per-channel flow control.
//
// The zero value is not usable; obtain instances from OpenChannel.
type Channel struct {
	conn *FlowConn

	id        uint32
	maxPacket uint32

	// send-side state, guarded by conn.mu
	sendWindow  uint64 // remote credit currently available
	sendBuf     bytes.Buffer
	sendErr     error
	localClosed bool
	localEOF    bool
	pendingEOF  bool

	// receive-side state, guarded by conn.mu
	recvWindow  uint64 // remaining inbound credit
	recvInit    uint64
	recvPending uint64 // consumed by Read, not yet returned to the sender
	recvBuf     bytes.Buffer
	eofRecv     bool
	closeRecv   bool
	recvErr     error

	cond *sync.Cond
}

// ID returns the channel identifier.
func (ch *Channel) ID() uint32 { return ch.id }

// failLocked marks the channel dead and releases blocked readers/writers. The
// caller holds conn.mu.
func (ch *Channel) failLocked(err error) {
	if ch.sendErr == nil {
		ch.sendErr = err
	}
	if ch.recvErr == nil {
		ch.recvErr = err
	}
	ch.eofRecv = true
	ch.cond.Broadcast()
}

// Write sends p over the channel, respecting remote window credit and the max
// packet size. It blocks until all bytes are handed to the frame writer, the
// channel fails, or the channel is closed. On success len(p), nil is returned;
// a transport failure returns the number of bytes acknowledged before it.
func (ch *Channel) Write(p []byte) (int, error) {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.sendErr != nil {
		return 0, ch.sendErr
	}
	if ch.localClosed {
		return 0, ErrChannelClosed
	}

	ch.sendBuf.Write(p)
	total := len(p)

	if err := ch.pumpLocked(); err != nil {
		acked := total - ch.sendBuf.Len()
		if acked < 0 {
			acked = 0
		}
		return acked, err
	}
	return total, nil
}

// pumpLocked drains queued data (and a pending EOF) while credit permits. The
// caller holds conn.mu; the condition variable is released while blocked.
func (ch *Channel) pumpLocked() error {
	for {
		if ch.sendErr != nil {
			return ch.sendErr
		}
		if ch.localClosed {
			return ErrChannelClosed
		}

		if ch.sendBuf.Len() == 0 {
			if ch.pendingEOF {
				if _, err := ch.emitLocked(Frame{ChannelID: ch.id, Type: FrameEOF}); err != nil {
					return err
				}
				ch.pendingEOF = false
				ch.localEOF = true
			}
			return nil
		}

		// Wait until the remote has granted at least one byte of credit.
		if ch.sendWindow == 0 {
			ch.cond.Wait()
			continue
		}

		// A reservation is bounded by both available credit and the
		// maximum frame size.
		limit := min(ch.sendWindow, uint64(ch.maxPacket))
		pending := ch.sendBuf.Bytes()
		n := min(limit, uint64(len(pending)))
		fragment := append([]byte(nil), pending[:n]...)

		delivered, err := ch.emitLocked(Frame{
			ChannelID: ch.id,
			Type:      FrameData,
			Payload:   fragment,
		})

		// Every reserved byte is accounted: delivered bytes consume
		// remote credit, undelivered bytes stay queued and retain their
		// local reservation.
		ch.sendWindow -= uint64(delivered)
		ch.sendBuf.Next(delivered)
		if err != nil {
			ch.failLocked(err)
			return err
		}
	}
}

// emitLocked invokes the host frame writer. For DATA frames a partial write
// reports only the delivered prefix; other frame kinds are all-or-nothing.
func (ch *Channel) emitLocked(f Frame) (int, error) {
	n, err := ch.conn.writer.WriteFrame(f)
	if f.Type == FrameData {
		if n > len(f.Payload) {
			n = len(f.Payload)
		}
		if n < 0 {
			n = 0
		}
	} else if err == nil {
		n = 0
	}
	return n, err
}

// HandleWindowAdjust applies an inbound WINDOW_ADJUST. If the adjustment
// overflows the uint32 window the channel is terminated and
// ErrWindowOverflow is returned; sibling channels are untouched.
func (ch *Channel) HandleWindowAdjust(inc uint32) error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.sendErr != nil {
		return ch.sendErr
	}
	next := ch.sendWindow + uint64(inc)
	if next > MaxWindow || next < ch.sendWindow {
		ch.failLocked(ErrWindowOverflow)
		return ErrWindowOverflow
	}
	ch.sendWindow = next
	ch.cond.Broadcast()
	return nil
}

// CancelPending discards data that was queued but never sent. Local
// reservations for the discarded bytes are returned to the send window, but no
// WINDOW_ADJUST is emitted: the remote never granted extra credit, so remote
// accounting must not be inflated.
func (ch *Channel) CancelPending() int {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	n := ch.sendBuf.Len()
	if n == 0 {
		return 0
	}
	ch.sendWindow += uint64(n)
	ch.sendBuf.Reset()
	ch.pendingEOF = false
	ch.cond.Broadcast()
	return n
}

// SendEOF flushes queued data, then emits a half-close (EOF) frame. It blocks
// until the queue drains or the channel fails.
func (ch *Channel) SendEOF() error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.sendErr != nil {
		return ch.sendErr
	}
	if ch.localClosed || ch.localEOF {
		return ErrChannelClosed
	}
	ch.pendingEOF = true
	return ch.pumpLocked()
}

// Close terminates the channel: unsent queued data is discarded with its local
// reservation returned, a single CLOSE frame is emitted, and the channel is
// removed from the connection. Close is idempotent.
func (ch *Channel) Close() error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.localClosed {
		return nil
	}

	// Discard unsent data and return the local reservation without
	// touching remote credit.
	if ch.sendBuf.Len() > 0 {
		ch.sendWindow += uint64(ch.sendBuf.Len())
		ch.sendBuf.Reset()
	}
	ch.pendingEOF = false

	var closeErr error
	if _, err := ch.emitLocked(Frame{ChannelID: ch.id, Type: FrameClose}); err != nil {
		closeErr = err
	}

	ch.localClosed = true
	ch.localEOF = true
	ch.failLocked(ErrChannelClosed)
	delete(ch.conn.channels, ch.id)
	return closeErr
}

// HandleData feeds an inbound DATA frame into the channel. Data past the
// receive window terminates only this channel.
func (ch *Channel) HandleData(p []byte) error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.recvErr != nil {
		return ch.recvErr
	}
	if ch.closeRecv {
		return ErrChannelClosed
	}
	if ch.eofRecv {
		ch.failLocked(ErrDataAfterEOF)
		return ErrDataAfterEOF
	}
	if uint64(len(p)) > ch.recvWindow {
		ch.failLocked(ErrRecvWindowOverflow)
		return ErrRecvWindowOverflow
	}
	ch.recvWindow -= uint64(len(p))
	ch.recvBuf.Write(p)
	ch.cond.Broadcast()
	return nil
}

// HandleEOF marks the remote send side as finished. Buffered data remains
// readable until drained, after which Read returns io.EOF.
func (ch *Channel) HandleEOF() error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.closeRecv {
		return ErrChannelClosed
	}
	ch.eofRecv = true
	ch.cond.Broadcast()
	return nil
}

// HandleClose tears down the receive side and fails any blocked writer.
func (ch *Channel) HandleClose() error {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	if ch.closeRecv {
		return nil
	}
	ch.closeRecv = true
	ch.eofRecv = true
	ch.cond.Broadcast()
	return nil
}

// Read consumes received bytes into p. When the buffer is empty after EOF it
// returns io.EOF; a closed or failed channel reports its terminal error.
// Consumed receive credit is returned to the sender with WINDOW_ADJUST.
func (ch *Channel) Read(p []byte) (int, error) {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()

	for ch.recvBuf.Len() == 0 {
		if ch.recvErr != nil {
			return 0, ch.recvErr
		}
		if ch.closeRecv || ch.eofRecv {
			return 0, io.EOF
		}
		ch.cond.Wait()
	}

	n, _ := ch.recvBuf.Read(p)

	// Return credit only for bytes the reader actually consumed, once at
	// least half the initial window is pending, mirroring RFC 4254's
	// threshold behavior. Buffered-but-unread bytes keep their credit.
	ch.recvPending += uint64(n)
	threshold := ch.recvInit / 2
	for ch.recvPending > 0 && ch.recvPending >= threshold {
		adjust := ch.recvPending
		if adjust > MaxWindow {
			adjust = MaxWindow
		}
		if _, err := ch.emitLocked(Frame{
			ChannelID: ch.id,
			Type:      FrameWindowAdjust,
			N:         uint32(adjust),
		}); err != nil {
			ch.failLocked(err)
			return n, err
		}
		ch.recvWindow += adjust
		ch.recvPending -= adjust
	}
	return n, nil
}

// Stats is a point-in-time snapshot of a channel's flow-control counters.
type Stats struct {
	// SendWindow is the remote credit currently held (not yet consumed).
	SendWindow uint32
	// QueuedSend is bytes buffered locally awaiting credit.
	QueuedSend int
	// RecvWindow is the inbound credit still available.
	RecvWindow uint32
	// BufferedRecv is inbound bytes not yet consumed by Read.
	BufferedRecv int
	// Closed reports whether Close has fully torn the channel down.
	Closed bool
}

// Snapshot returns the current flow-control counters for the channel.
func (ch *Channel) Snapshot() Stats {
	ch.conn.mu.Lock()
	defer ch.conn.mu.Unlock()
	return Stats{
		SendWindow:   uint32(ch.sendWindow),
		QueuedSend:   ch.sendBuf.Len(),
		RecvWindow:   uint32(ch.recvWindow),
		BufferedRecv: ch.recvBuf.Len(),
		Closed:       ch.localClosed,
	}
}
