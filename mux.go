package sshchannel

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// Sentinal errors reported by the multiplexer and its channels.
var (
	ErrSessionClosed = errors.New("sshchannel: session closed")
	ErrChannelInUse  = errors.New("sshchannel: channel id already in use")
	ErrChannelClosed = errors.New("sshchannel: channel closed")
)

type channelState int

const (
	chanOpening channelState = iota
	chanOpen
	chanHalf
	chanClosed
)

// Config wires a Mux to its session transport, policy and dialer.
type Config struct {
	// Session is the reliable, ordered byte stream to the peer (typically an
	// SSH channel).
	Session io.ReadWriter
	// Policy authorizes every open request and re-checks grants on admission.
	Policy Policy
	// Dialer reaches the pinned target for each authorized channel.
	Dialer Dialer
}

// Mux is the session host side of the multiplexer. One Mux serves one
// session; every logical channel gets its own authorization grant and its
// own dial.
type Mux struct {
	reader *frameReader
	writer *frameWriter
	closer io.Closer
	policy Policy
	dialer Dialer

	mu       sync.Mutex
	channels map[uint32]*channel
	epochs   map[uint32]uint64
	closed   bool

	wg        sync.WaitGroup // dial/admit goroutines
	relayWG   sync.WaitGroup // target -> peer relay goroutines
	closeOnce sync.Once
	closeErr  error
}

type channel struct {
	id     uint32
	epoch  uint64
	state  channelState
	grant  *Grant
	target Target

	dialCtx    context.Context
	dialCancel context.CancelFunc

	conn      net.Conn
	relayDone chan struct{}

	// peerHalf records a half-close that arrived while the dial was still
	// in flight, so it can be applied once the connection is admitted.
	peerHalf bool
}

// NewMux constructs a session host.
func NewMux(cfg Config) *Mux {
	m := &Mux{
		reader:   newFrameReader(cfg.Session),
		writer:   newFrameWriter(cfg.Session),
		policy:   cfg.Policy,
		dialer:   cfg.Dialer,
		channels: make(map[uint32]*channel),
		epochs:   make(map[uint32]uint64),
	}
	if c, ok := cfg.Session.(io.Closer); ok {
		m.closer = c
	}
	return m
}

// Serve runs the read loop until the session ends. It always drains in-flight
// dials and relays before returning.
func (m *Mux) Serve() error {
	var serveErr error
	for {
		typ, payload, err := m.reader.readFrame()
		if err != nil {
			serveErr = err
			break
		}
		switch typ {
		case frameOpen:
			err = m.handleOpen(payload)
		case frameData:
			err = m.handleData(payload)
		case frameClose:
			m.handleClose(payload)
		case frameHalf:
			m.handleHalf(payload)
		default:
			err = fmt.Errorf("sshchannel: unexpected frame type %d", typ)
		}
		if err != nil {
			serveErr = err
			break
		}
	}
	m.drain()
	return serveErr
}

// Close shuts the session down: cancels in-flight dials, closes every
// upstream connection and closes the session transport.
func (m *Mux) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		chs := make([]*channel, 0, len(m.channels))
		for _, ch := range m.channels {
			chs = append(chs, ch)
		}
		m.mu.Unlock()

		for _, ch := range chs {
			m.teardown(ch)
		}
		if m.closer != nil {
			m.closeErr = m.closer.Close()
		}
	})
	m.wg.Wait()
	m.relayWG.Wait()
	return m.closeErr
}

// drain is called when the read loop has stopped: no further frames can be
// sent, so channels are torn down without echoing Close.
func (m *Mux) drain() {
	m.mu.Lock()
	m.closed = true
	chs := make([]*channel, 0, len(m.channels))
	for _, ch := range m.channels {
		chs = append(chs, ch)
	}
	m.mu.Unlock()

	for _, ch := range chs {
		m.teardown(ch)
	}
	m.wg.Wait()
	m.relayWG.Wait()
}

func (m *Mux) handleOpen(payload []byte) error {
	id, host, port, err := parseOpen(payload)
	if err != nil {
		return err
	}

	// Synchronous authorization: the grant pins the target and the policy
	// generation before any dial starts.
	grant, err := m.policy.Authorize(host, port)
	if err != nil {
		return m.writer.write(frameFail, encodeFail(id, err))
	}

	m.mu.Lock()
	switch {
	case m.closed:
		m.mu.Unlock()
		return m.writer.write(frameFail, encodeFail(id, ErrSessionClosed))
	case m.channels[id] != nil:
		m.mu.Unlock()
		return m.writer.write(frameFail, encodeFail(id, ErrChannelInUse))
	}
	m.epochs[id]++
	dialCtx, dialCancel := context.WithCancel(context.Background())
	ch := &channel{
		id:         id,
		epoch:      m.epochs[id],
		state:      chanOpening,
		grant:      grant,
		target:     grant.Target(),
		dialCtx:    dialCtx,
		dialCancel: dialCancel,
	}
	m.channels[id] = ch
	m.mu.Unlock()

	m.wg.Add(1)
	go m.dialAndAdmit(ch)
	return nil
}

// dialAndAdmit runs the asynchronous dial. The target and grant captured here
// are fixed for the channel's lifetime; after the dial returns and before the
// channel is admitted, the grant is re-checked so a revocation cannot be
// bypassed by an in-flight dial.
func (m *Mux) dialAndAdmit(ch *channel) {
	defer m.wg.Done()

	conn, dialErr := m.dialer.DialContext(ch.dialCtx, ch.target)

	m.mu.Lock()
	cur, ok := m.channels[ch.id]
	stale := !ok || cur != ch || cur.epoch != ch.epoch || ch.state == chanClosed
	if stale {
		m.mu.Unlock()
		// The id was recycled (or the channel closed) while the dial was
		// pending. A late callback must never touch the new channel.
		if ch.dialCancel != nil {
			ch.dialCancel()
		}
		if conn != nil {
			conn.Close()
		}
		return
	}
	if dialErr != nil {
		if cur := m.channels[ch.id]; cur != ch {
			// Recycled concurrently: do not send a Fail that could be
			// misattributed to the channel now owning this number.
			m.mu.Unlock()
			return
		}
		m.removeLocked(ch)
		m.mu.Unlock()
		_ = m.writer.write(frameFail, encodeFail(ch.id, dialErr))
		return
	}
	// The dial succeeded: re-validate the grant BEFORE admitting the channel.
	if err := m.policy.Revoked(ch.grant); err != nil {
		if cur := m.channels[ch.id]; cur != ch {
			m.mu.Unlock()
			conn.Close()
			return
		}
		m.removeLocked(ch)
		m.mu.Unlock()
		conn.Close()
		_ = m.writer.write(frameFail, encodeFail(ch.id, err))
		return
	}
	peerHalf := ch.peerHalf
	ch.conn = conn
	ch.state = chanOpen
	ch.relayDone = make(chan struct{})
	m.mu.Unlock()

	if peerHalf {
		_ = closeWrite(conn)
	}
	if err := m.writer.write(frameOK, encodeID(ch.id)); err != nil {
		m.teardown(ch)
		return
	}

	m.relayWG.Add(1)
	go m.relay(ch, conn)
}

// relay pumps bytes from the upstream target back to the peer. A clean EOF is
// propagated as half-close; other failures close the whole channel.
func (m *Mux) relay(ch *channel, conn net.Conn) {
	defer m.relayWG.Done()
	defer close(ch.relayDone)

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if werr := m.writer.write(frameData, encodeData(ch.id, buf[:n])); werr != nil {
				_ = conn.Close()
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = m.writer.write(frameHalf, encodeID(ch.id))
				return
			}
			_ = m.writer.write(frameClose, encodeID(ch.id))
			m.teardown(ch)
			return
		}
	}
}

func (m *Mux) handleData(payload []byte) error {
	id, data, err := parseData(payload)
	if err != nil {
		return err
	}
	m.mu.Lock()
	ch := m.channels[id]
	var conn net.Conn
	state := chanClosed
	if ch != nil {
		conn, state = ch.conn, ch.state
	}
	m.mu.Unlock()

	if ch == nil || conn == nil || state == chanClosed || state == chanOpening {
		return nil
	}
	if _, err := conn.Write(data); err != nil {
		m.teardown(ch)
		_ = m.writer.write(frameClose, encodeID(ch.id))
	}
	return nil
}

func (m *Mux) handleHalf(payload []byte) {
	id := decodeID(payload)
	m.mu.Lock()
	ch := m.channels[id]
	var conn net.Conn
	if ch != nil {
		if ch.state == chanOpening {
			ch.peerHalf = true
		} else if ch.state == chanOpen {
			ch.state = chanHalf
			conn = ch.conn
		}
	}
	m.mu.Unlock()

	if ch == nil || conn == nil {
		return
	}
	if err := closeWrite(conn); err != nil {
		m.teardown(ch)
		_ = m.writer.write(frameClose, encodeID(ch.id))
	}
}

func (m *Mux) handleClose(payload []byte) {
	id := decodeID(payload)
	m.mu.Lock()
	ch := m.channels[id]
	m.mu.Unlock()
	if ch != nil {
		// The peer initiated the close: no Close frame is echoed back.
		m.teardown(ch)
	}
}

// removeLocked drops a channel from the registry. The caller must hold m.mu
// and must verify identity before calling.
func (m *Mux) removeLocked(ch *channel) {
	if cur := m.channels[ch.id]; cur == ch {
		delete(m.channels, ch.id)
		ch.state = chanClosed
		if ch.dialCancel != nil {
			ch.dialCancel()
		}
	}
}

// teardown removes and closes a channel with a strict recycling order:
// registry entry first (so the id becomes reusable), then the dial is
// cancelled and the upstream connection closed, and finally it waits for the
// relay goroutine to exit. Stale callers (a superseded channel sharing a
// recycled id) are rejected by identity check.
func (m *Mux) teardown(ch *channel) {
	m.mu.Lock()
	cur, ok := m.channels[ch.id]
	if !ok || cur != ch {
		m.mu.Unlock()
		return
	}
	conn := ch.conn
	relayDone := ch.relayDone
	cancel := ch.dialCancel
	delete(m.channels, ch.id)
	ch.state = chanClosed
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	if relayDone != nil {
		<-relayDone
	}
}

// --- frame payload codecs -------------------------------------------------

func encodeOpen(id uint32, host string, port uint16) []byte {
	target := encodeTarget(host, port)
	payload := make([]byte, 4+len(target))
	binary.BigEndian.PutUint32(payload, id)
	copy(payload[4:], target)
	return payload
}

func parseOpen(payload []byte) (id uint32, host string, port uint16, err error) {
	if len(payload) < 4 {
		return 0, "", 0, errors.New("sshchannel: truncated open frame")
	}
	id = binary.BigEndian.Uint32(payload)
	host, port, err = decodeTarget(payload[4:])
	return id, host, port, err
}

func encodeID(id uint32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, id)
	return payload
}

func decodeID(payload []byte) uint32 {
	if len(payload) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(payload)
}

func encodeData(id uint32, data []byte) []byte {
	payload := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(payload, id)
	copy(payload[4:], data)
	return payload
}

func parseData(payload []byte) (uint32, []byte, error) {
	if len(payload) < 4 {
		return 0, nil, errors.New("sshchannel: truncated data frame")
	}
	return binary.BigEndian.Uint32(payload), bytes.Clone(payload[4:]), nil
}

func encodeFail(id uint32, cause error) []byte {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	payload := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(payload, id)
	copy(payload[4:], msg)
	return payload
}

func parseFail(payload []byte) (uint32, string) {
	if len(payload) < 4 {
		return 0, "sshchannel: open rejected"
	}
	return binary.BigEndian.Uint32(payload), string(payload[4:])
}
