package framemux

import (
	"errors"
	"fmt"
	"io"
)

// ErrBackpressure is returned by Feed/Resume when a received DATA frame
// cannot be queued because the per-stream or the global buffer quota is
// exhausted. It is not corruption: the frame is held internally and will
// be admitted after the application drains queued data with Receive and
// calls Resume (or Feed again).
var ErrBackpressure = errors.New("framemux: backpressure: buffer quota exhausted")

// Config configures a Mux. Zero fields take defaults.
type Config struct {
	MaxPayload  int // maximum payload bytes per frame (default 1024)
	StreamQuota int // maximum queued, undelivered payload bytes per stream (default 64 KiB)
	TotalQuota  int // maximum queued payload bytes across all streams (default 1 MiB)
}

func (c *Config) defaults() {
	if c.MaxPayload <= 0 {
		c.MaxPayload = DefaultMaxPayload
	}
	if c.StreamQuota <= 0 {
		c.StreamQuota = 1 << 16
	}
	if c.TotalQuota <= 0 {
		c.TotalQuota = 1 << 20
	}
}

// EventKind classifies asynchronous Mux events.
type EventKind int

const (
	// EventBadData reports corrupt input (a *BadDataError in Err). The
	// connection stays usable; other streams are unaffected.
	EventBadData EventKind = iota
	// EventEpochChanged reports that the encoding epoch switched (at a
	// frame boundary, after mutual confirmation).
	EventEpochChanged
	// EventStreamReset reports that the peer reset the stream; its
	// queued data was discarded. Other streams are unaffected.
	EventStreamReset
	// EventDuplicate reports a retransmitted frame that was already
	// delivered; it is re-acknowledged and dropped, never delivered
	// twice.
	EventDuplicate
)

// Event is an asynchronous notification from a Mux, drained with Events.
type Event struct {
	Kind   EventKind
	Stream uint8 // for EventStreamReset and EventDuplicate
	Epoch  uint8 // for EventEpochChanged
	Err    error // for EventBadData
}

type streamState struct {
	queue   [][]byte
	queued  int
	recvSeq uint32
}

// Mux multiplexes logical streams over a single ordered byte channel.
// It is not safe for concurrent use; pausing and resuming simply means
// stopping and restarting Feed calls — all parser, escape and
// negotiation state is preserved in the struct.
type Mux struct {
	w   io.Writer
	cfg Config

	parser *parser

	epoch      uint8
	prevEpoch  uint8
	acceptPrev bool // tolerate in-flight tail frames from prevEpoch
	pendEpoch  int  // epoch we proposed, -1 when no negotiation pending

	streams     [256]*streamState
	totalQueued int

	sendSeq [256]uint32
	unacked []frame // sent DATA frames not yet acknowledged, in send order

	inBuf   []byte // fed bytes not yet parsed (held during backpressure)
	pending []frame
	events  []Event
}

// NewMux creates a Mux writing outbound frames to w.
func NewMux(w io.Writer, cfg Config) *Mux {
	cfg.defaults()
	return &Mux{
		w:         w,
		cfg:       cfg,
		parser:    newParser(cfg.MaxPayload),
		pendEpoch: -1,
	}
}

// Epoch returns the current encoding epoch.
func (m *Mux) Epoch() uint8 { return m.epoch }

// Unacked returns the number of sent DATA frames not yet acknowledged.
func (m *Mux) Unacked() int { return len(m.unacked) }

// Queued returns the queued, undelivered payload bytes for a stream.
func (m *Mux) Queued(stream uint8) int {
	if st := m.streams[stream]; st != nil {
		return st.queued
	}
	return 0
}

// Events drains the pending asynchronous events.
func (m *Mux) Events() []Event {
	ev := m.events
	m.events = nil
	return ev
}

// Send queues payload as a DATA frame on stream and writes it out. The
// frame is tracked until acknowledged so Retransmit can recover it.
func (m *Mux) Send(stream uint8, payload []byte) error {
	if len(payload) > m.cfg.MaxPayload {
		return fmt.Errorf("framemux: payload %d bytes exceeds max %d", len(payload), m.cfg.MaxPayload)
	}
	f := frame{epoch: m.epoch, stream: stream, seq: m.sendSeq[stream], typ: typeData,
		payload: append([]byte(nil), payload...)}
	m.sendSeq[stream]++
	m.unacked = append(m.unacked, f)
	return m.writeFrame(f)
}

// ProposeEpoch asks the peer to switch the encoding epoch. The switch
// takes effect only after the peer confirms, at a frame boundary; until
// then frames keep using the current epoch. Only one negotiation may be
// in flight per direction.
func (m *Mux) ProposeEpoch(epoch uint8) error {
	if !validEpoch(epoch) {
		return fmt.Errorf("framemux: unsupported epoch %d", epoch)
	}
	if epoch == m.epoch {
		return nil
	}
	if m.pendEpoch >= 0 {
		return errors.New("framemux: epoch negotiation already in progress")
	}
	m.pendEpoch = int(epoch)
	return m.writeFrame(frame{epoch: m.epoch, typ: typeEpochPropose, payload: []byte{epoch}})
}

// ResetStream discards the stream's queued inbound data and tells the
// peer to do the same. Other streams are unaffected.
func (m *Mux) ResetStream(stream uint8) error {
	if st := m.streams[stream]; st != nil {
		m.totalQueued -= st.queued
		st.queue = nil
		st.queued = 0
	}
	return m.writeFrame(frame{epoch: m.epoch, typ: typeReset, stream: stream})
}

// Retransmit rewrites every unacknowledged DATA frame (with the current
// epoch). Receivers deduplicate by sequence number, so already
// acknowledged or delivered frames are never delivered twice.
func (m *Mux) Retransmit() error {
	for _, f := range m.unacked {
		f.epoch = m.epoch
		if err := m.writeFrame(f); err != nil {
			return err
		}
	}
	return nil
}

// Feed ingests an arbitrary chunk of inbound bytes. It returns
// ErrBackpressure when quotas block delivery; anything else is reported
// asynchronously via Events (EventBadData), never as a fatal error.
func (m *Mux) Feed(p []byte) error {
	m.inBuf = append(m.inBuf, p...)
	return m.pump()
}

// Resume retries delivery of frames held by backpressure after the
// application has drained queued data with Receive.
func (m *Mux) Resume() error { return m.pump() }

// Receive pops the oldest queued payload for stream.
func (m *Mux) Receive(stream uint8) ([]byte, bool) {
	st := m.streams[stream]
	if st == nil || len(st.queue) == 0 {
		return nil, false
	}
	p := st.queue[0]
	st.queue[0] = nil
	st.queue = st.queue[1:]
	st.queued -= len(p)
	m.totalQueued -= len(p)
	return p, true
}

func (m *Mux) pump() error {
	if len(m.inBuf) > 0 {
		var evs []parseEvent
		evs = m.parser.feed(m.inBuf, evs)
		m.inBuf = m.inBuf[:0]
		for _, ev := range evs {
			if ev.err != nil {
				m.pushEvent(Event{Kind: EventBadData, Err: ev.err})
				continue
			}
			m.pending = append(m.pending, *ev.frame)
		}
	}
	for len(m.pending) > 0 {
		f := m.pending[0]
		if f.typ == typeData && !m.fits(f) {
			// Quota checked before the frame is admitted (and before
			// any queue allocation happens for it).
			return ErrBackpressure
		}
		m.pending = m.pending[1:]
		m.handleFrame(f)
	}
	if len(m.pending) == 0 {
		m.pending = nil
	}
	return nil
}

func (m *Mux) fits(f frame) bool {
	var queued int
	if st := m.streams[f.stream]; st != nil {
		queued = st.queued
	}
	return queued+len(f.payload) <= m.cfg.StreamQuota &&
		m.totalQueued+len(f.payload) <= m.cfg.TotalQuota
}

func (m *Mux) handleFrame(f frame) {
	if f.epoch != m.epoch {
		if m.acceptPrev && f.epoch == m.prevEpoch {
			// In-flight tail frame encoded before the peer switched.
		} else {
			m.pushEvent(Event{Kind: EventBadData, Err: fmt.Errorf(
				"framemux: epoch mismatch: frame %d, current %d", f.epoch, m.epoch)})
			return
		}
	} else if m.acceptPrev {
		m.acceptPrev = false
	}
	switch f.typ {
	case typeData:
		m.handleData(f)
	case typeAck:
		m.handleAck(f.stream, f.seq)
	case typeEpochPropose:
		if len(f.payload) == 1 && validEpoch(f.payload[0]) {
			e := f.payload[0]
			// Confirm first (with the old epoch), then switch at this
			// frame boundary.
			m.writeFrame(frame{epoch: m.epoch, typ: typeEpochAck, payload: []byte{e}})
			m.setEpoch(e)
		}
	case typeEpochAck:
		if len(f.payload) == 1 && m.pendEpoch == int(f.payload[0]) {
			m.pendEpoch = -1
			m.setEpoch(f.payload[0])
		}
	case typeReset:
		if st := m.streams[f.stream]; st != nil {
			m.totalQueued -= st.queued
			st.queue = nil
			st.queued = 0
		}
		m.pushEvent(Event{Kind: EventStreamReset, Stream: f.stream})
	}
}

func (m *Mux) handleData(f frame) {
	st := m.stream(f.stream)
	switch {
	case f.seq < st.recvSeq:
		// Already delivered (and acknowledged): re-acknowledge but
		// never deliver twice.
		m.pushEvent(Event{Kind: EventDuplicate, Stream: f.stream})
		m.sendAck(f.stream, st.recvSeq-1)
	case f.seq > st.recvSeq:
		// Out of order: drop and wait for the sender's retransmission.
	default:
		st.queue = append(st.queue, f.payload)
		st.queued += len(f.payload)
		m.totalQueued += len(f.payload)
		st.recvSeq++
		m.sendAck(f.stream, f.seq)
	}
}

func (m *Mux) handleAck(stream uint8, seq uint32) {
	kept := m.unacked[:0]
	for _, f := range m.unacked {
		if f.stream == stream && f.seq <= seq {
			continue
		}
		kept = append(kept, f)
	}
	m.unacked = kept
}

func (m *Mux) sendAck(stream uint8, seq uint32) {
	m.writeFrame(frame{epoch: m.epoch, typ: typeAck, stream: stream, seq: seq})
}

func (m *Mux) setEpoch(e uint8) {
	m.prevEpoch = m.epoch
	m.epoch = e
	m.acceptPrev = true
	m.pushEvent(Event{Kind: EventEpochChanged, Epoch: e})
}

func (m *Mux) stream(id uint8) *streamState {
	st := m.streams[id]
	if st == nil {
		st = &streamState{}
		m.streams[id] = st
	}
	return st
}

func (m *Mux) writeFrame(f frame) error {
	_, err := m.w.Write(appendEscaped(nil, f.appendContent(nil), f.epoch))
	return err
}

func (m *Mux) pushEvent(ev Event) { m.events = append(m.events, ev) }
