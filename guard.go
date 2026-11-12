package netsec

import (
	"errors"
	"fmt"
	"math"
)

// FrameType enumerates the yamux frame kinds the session read loop
// forwards to the guard.
type FrameType uint8

const (
	FrameSYN FrameType = iota
	FrameFIN
	FrameRST
	FrameWindowUpdate
	FrameData
)

// Frame is a decoded yamux frame relevant to stream management.
type Frame struct {
	Type     FrameType
	StreamID uint32
	Length   uint32 // payload length, valid for FrameData
	Delta    uint32 // window increment, valid for FrameWindowUpdate
}

// Effect tells the session read loop what to do after a frame.
type Effect uint8

const (
	// EffectNone means the frame was accepted; no action required.
	EffectNone Effect = iota
	// EffectResetStream means the frame violated flow control; the
	// loop must send RST for Frame.StreamID and drop the stream.
	EffectResetStream
	// EffectGoAway means the peer committed a protocol violation or
	// exhausted session limits; the loop must send GoAway and tear
	// the session down.
	EffectGoAway
)

// ErrSessionTerminated is returned on every call after a GoAway
// decision has been made.
var ErrSessionTerminated = errors.New("netsec: session terminated by GoAway")

// Config tunes a Manager.
type Config struct {
	// PeerParityOdd is true when the remote peer initiates streams
	// with odd IDs (yamux client side). Server-side peers use even.
	PeerParityOdd bool
	// InitialWindow is the per-stream receive window in bytes.
	InitialWindow uint32
	// MaxPendingSYNs bounds peer-initiated streams that have not yet
	// been acknowledged by any follow-up frame. Exceeding it triggers
	// GoAway.
	MaxPendingSYNs int
}

type stream struct {
	window        uint32 // remaining receive window
	windowUpdates uint64 // cumulative WindowUpdate deltas
	pending       bool   // SYN received, not yet acknowledged
}

// Manager enforces yamux stream-ID, flow-control and SYN-flood rules
// for one session. It is not safe for concurrent use; the session read
// loop is expected to be the sole caller.
type Manager struct {
	cfg        Config
	lastPeerID uint32
	hasPeerID  bool
	resetIDs   map[uint32]struct{}
	streams    map[uint32]*stream
	pending    int
	gone       bool
}

// NewManager returns a Manager configured by cfg.
func NewManager(cfg Config) *Manager {
	return &Manager{
		cfg:      cfg,
		resetIDs: make(map[uint32]struct{}),
		streams:  make(map[uint32]*stream),
	}
}

// HandleFrame validates one inbound frame and reports the effect the
// read loop must apply.
func (m *Manager) HandleFrame(f Frame) (Effect, error) {
	if m.gone {
		return EffectGoAway, ErrSessionTerminated
	}
	switch f.Type {
	case FrameSYN:
		return m.handleSYN(f.StreamID)
	case FrameRST:
		m.handleRST(f.StreamID)
		return EffectNone, nil
	case FrameFIN:
		m.closeStream(f.StreamID)
		return EffectNone, nil
	case FrameData:
		return m.handleData(f.StreamID, f.Length)
	case FrameWindowUpdate:
		return m.handleWindowUpdate(f.StreamID, f.Delta)
	default:
		return EffectNone, fmt.Errorf("netsec: unknown frame type %d", f.Type)
	}
}

// OpenLocal registers a locally initiated stream so inbound data and
// window updates for it are flow-controlled as well.
func (m *Manager) OpenLocal(id uint32) {
	if _, ok := m.streams[id]; !ok {
		m.streams[id] = &stream{window: m.cfg.InitialWindow}
	}
}

// PendingSYNs reports how many peer-initiated streams await
// acknowledgement.
func (m *Manager) PendingSYNs() int { return m.pending }

func (m *Manager) handleSYN(id uint32) (Effect, error) {
	odd := id%2 == 1
	if odd != m.cfg.PeerParityOdd {
		m.gone = true
		return EffectGoAway, fmt.Errorf("netsec: stream %d has wrong parity for peer", id)
	}
	if _, ok := m.resetIDs[id]; ok {
		m.gone = true
		return EffectGoAway, fmt.Errorf("netsec: stream %d was reset and must not reopen", id)
	}
	if m.hasPeerID && id <= m.lastPeerID {
		m.gone = true
		return EffectGoAway, fmt.Errorf("netsec: stream %d not strictly increasing after %d", id, m.lastPeerID)
	}
	if m.pending >= m.cfg.MaxPendingSYNs {
		m.gone = true
		return EffectGoAway, fmt.Errorf("netsec: pending SYN limit %d exceeded", m.cfg.MaxPendingSYNs)
	}
	m.lastPeerID = id
	m.hasPeerID = true
	m.streams[id] = &stream{window: m.cfg.InitialWindow, pending: true}
	m.pending++
	return EffectNone, nil
}

func (m *Manager) handleRST(id uint32) {
	m.resetIDs[id] = struct{}{}
	m.closeStream(id)
}

func (m *Manager) closeStream(id uint32) {
	if s, ok := m.streams[id]; ok {
		m.ack(s)
		delete(m.streams, id)
	}
}

// ack marks a stream acknowledged, releasing its pending SYN slot.
func (m *Manager) ack(s *stream) {
	if s.pending {
		s.pending = false
		m.pending--
	}
}

func (m *Manager) handleData(id uint32, n uint32) (Effect, error) {
	s, ok := m.streams[id]
	if !ok {
		// Data for an unknown or closed stream: ignore silently; the
		// ID rules are enforced at SYN time.
		return EffectNone, nil
	}
	m.ack(s)
	if n > s.window {
		m.resetIDs[id] = struct{}{}
		m.closeStream(id)
		return EffectResetStream, fmt.Errorf("netsec: stream %d data %d exceeds window %d", id, n, s.window)
	}
	s.window -= n
	return EffectNone, nil
}

func (m *Manager) handleWindowUpdate(id uint32, delta uint32) (Effect, error) {
	s, ok := m.streams[id]
	if !ok {
		return EffectNone, nil
	}
	m.ack(s)
	s.windowUpdates += uint64(delta)
	if s.windowUpdates > math.MaxUint32 {
		m.resetIDs[id] = struct{}{}
		m.closeStream(id)
		return EffectResetStream, fmt.Errorf("netsec: stream %d cumulative window updates exceed 2^32-1", id)
	}
	if uint64(s.window)+uint64(delta) > math.MaxUint32 {
		m.resetIDs[id] = struct{}{}
		m.closeStream(id)
		return EffectResetStream, fmt.Errorf("netsec: stream %d window overflow", id)
	}
	s.window += delta
	return EffectNone, nil
}
