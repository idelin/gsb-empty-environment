package mux

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// streamState tracks both directions of a stream independently so that
// half-close works: each side may finish sending while still receiving.
type streamState int

const (
	stateOpen             streamState = iota
	stateHalfClosedLocal              // we sent CLOSE: we may still receive
	stateHalfClosedRemote             // peer sent CLOSE: we may still send
	stateClosed
)

func (s streamState) canSend() bool    { return s == stateOpen || s == stateHalfClosedRemote }
func (s streamState) canReceive() bool { return s == stateOpen || s == stateHalfClosedLocal }

type stream struct {
	id         uint32
	state      streamState
	sendWindow int64 // credit to send DATA to the peer
	recvWindow int64 // credit the peer has to send us DATA

	wuSeqSent      uint32 // monotonic sequence of WINDOW_UPDATEs we send
	lastPeerWUSeq  uint32 // highest peer WINDOW_UPDATE sequence applied
	pending        []byte // queued outgoing application data
	closeAfterData bool   // CLOSE deferred until pending data is on the wire
}

// Session owns all streams on one side of the connection.
type Session struct {
	maxPayload    uint32
	connQuota     int64 // max wire bytes emitted per Flush
	initialWindow int64

	parser    *Parser
	streams   map[uint32]*stream
	dataOrder []uint32 // round-robin order of streams with pending data
	ctrl      []Frame  // prioritized control queue

	OnData  func(streamID uint32, data []byte)
	OnClose func(streamID uint32)
	OnReset func(streamID uint32)
}

func NewSession(maxPayload uint32, connQuota, initialWindow int64) *Session {
	return &Session{
		maxPayload:    maxPayload,
		connQuota:     connQuota,
		initialWindow: initialWindow,
		parser:        NewParser(maxPayload),
		streams:       make(map[uint32]*stream),
	}
}

// OpenStream registers a new stream and queues its OPEN control frame.
func (s *Session) OpenStream(id uint32) error {
	if _, ok := s.streams[id]; ok {
		return fmt.Errorf("mux: stream %d already open", id)
	}
	s.streams[id] = &stream{
		id:         id,
		state:      stateOpen,
		sendWindow: s.initialWindow,
		recvWindow: s.initialWindow,
	}
	s.ctrl = append(s.ctrl, Frame{Type: FrameOpen, StreamID: id})
	return nil
}

// SendData queues application data. It never blocks; actual transmission
// happens in Flush, bounded by the stream window and the connection quota.
func (s *Session) SendData(id uint32, data []byte) error {
	st := s.streams[id]
	if st == nil {
		return fmt.Errorf("mux: unknown stream %d", id)
	}
	if !st.state.canSend() {
		return fmt.Errorf("mux: stream %d closed for sending", id)
	}
	st.pending = append(st.pending, data...)
	s.notePending(id)
	return nil
}

// Close half-closes the local send side and queues a CLOSE control frame.
func (s *Session) Close(id uint32) error {
	st := s.streams[id]
	if st == nil {
		return fmt.Errorf("mux: unknown stream %d", id)
	}
	switch st.state {
	case stateOpen:
		st.state = stateHalfClosedLocal
	case stateHalfClosedRemote:
		st.state = stateClosed
	default:
		return fmt.Errorf("mux: stream %d already closed for sending", id)
	}
	if len(st.pending) > 0 {
		st.closeAfterData = true // keep per-stream order: DATA, then CLOSE
	} else {
		s.ctrl = append(s.ctrl, Frame{Type: FrameClose, StreamID: id})
	}
	return nil
}

// SendWindow reports the remaining send credit of a stream.
func (s *Session) SendWindow(id uint32) (int64, bool) {
	st := s.streams[id]
	if st == nil {
		return 0, false
	}
	return st.sendWindow, true
}

func (s *Session) notePending(id uint32) {
	for _, x := range s.dataOrder {
		if x == id {
			return
		}
	}
	s.dataOrder = append(s.dataOrder, id)
}

// Flush emits at most connQuota wire bytes: queued control frames first
// (they consume quota too), then DATA frames round-robin across streams so
// one window-exhausted stream never blocks the others.
func (s *Session) Flush() []byte {
	var out []byte
	quota := s.connQuota

	for len(s.ctrl) > 0 {
		enc, err := EncodeFrame(s.ctrl[0], s.maxPayload)
		if err != nil { // cannot happen: control payloads are built internally
			panic(err)
		}
		if int64(len(enc)) > quota {
			break
		}
		out = append(out, enc...)
		quota -= int64(len(enc))
		s.ctrl = s.ctrl[1:]
	}

	for quota > HeaderSize {
		progressed := false
		keep := s.dataOrder[:0]
		for _, id := range s.dataOrder {
			st := s.streams[id]
			if st == nil {
				continue // drop from rotation
			}
			if len(st.pending) == 0 {
				if st.closeAfterData && quota >= HeaderSize {
					enc, _ := EncodeFrame(Frame{Type: FrameClose, StreamID: id}, s.maxPayload)
					out = append(out, enc...)
					quota -= int64(len(enc))
					st.closeAfterData = false
					progressed = true
				}
				if !st.closeAfterData {
					continue // drop from rotation
				}
				keep = append(keep, id)
				continue
			}
			keep = append(keep, id)
			if st.sendWindow <= 0 || quota <= HeaderSize {
				continue // window exhausted or quota spent; others still go
			}
			n := int64(len(st.pending))
			if m := int64(s.maxPayload); n > m {
				n = m
			}
			if n > st.sendWindow {
				n = st.sendWindow
			}
			if q := quota - HeaderSize; n > q {
				n = q
			}
			if n <= 0 {
				continue
			}
			enc, err := EncodeFrame(Frame{Type: FrameData, StreamID: id, Payload: st.pending[:n]}, s.maxPayload)
			if err != nil {
				panic(err)
			}
			out = append(out, enc...)
			quota -= int64(len(enc))
			st.sendWindow -= n
			st.pending = st.pending[n:]
			progressed = true
		}
		s.dataOrder = keep
		if !progressed {
			break
		}
	}
	return out
}

// Receive consumes an arbitrary chunk of the byte stream, decoding as many
// complete frames as it contains. It returns *ProtocolError on malformed
// input or protocol-state violations; truncated input is simply buffered.
func (s *Session) Receive(chunk []byte) error {
	s.parser.Feed(chunk)
	for {
		f, err := s.parser.Next()
		if errors.Is(err, ErrNeedMoreData) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.handleFrame(f); err != nil {
			return err
		}
	}
}

func (s *Session) handleFrame(f Frame) error {
	switch f.Type {
	case FrameOpen:
		if _, ok := s.streams[f.StreamID]; ok {
			return &ProtocolError{Reason: fmt.Sprintf("duplicate OPEN for stream %d", f.StreamID)}
		}
		s.streams[f.StreamID] = &stream{
			id:         f.StreamID,
			state:      stateOpen,
			sendWindow: s.initialWindow,
			recvWindow: s.initialWindow,
		}
		return nil

	case FrameData:
		st := s.streams[f.StreamID]
		if st == nil {
			return &ProtocolError{Reason: fmt.Sprintf("DATA for unknown stream %d", f.StreamID)}
		}
		if !st.state.canReceive() {
			return &ProtocolError{Reason: fmt.Sprintf("DATA after close on stream %d", f.StreamID)}
		}
		if int64(len(f.Payload)) > st.recvWindow {
			return &ProtocolError{Reason: fmt.Sprintf("stream %d flow-control violation: %d bytes with window %d", f.StreamID, len(f.Payload), st.recvWindow)}
		}
		st.recvWindow -= int64(len(f.Payload))
		if s.OnData != nil {
			s.OnData(f.StreamID, f.Payload)
		}
		// The application consumed the bytes synchronously, so the credit is
		// returned to the peer and restored locally.
		st.recvWindow += int64(len(f.Payload))
		// Return the consumed credit as a sequenced WINDOW_UPDATE.
		st.wuSeqSent++
		p := make([]byte, 8)
		binary.BigEndian.PutUint32(p[0:4], st.wuSeqSent)
		binary.BigEndian.PutUint32(p[4:8], uint32(len(f.Payload)))
		s.ctrl = append(s.ctrl, Frame{Type: FrameWindowUpdate, StreamID: f.StreamID, Payload: p})
		return nil

	case FrameWindowUpdate:
		st := s.streams[f.StreamID]
		if st == nil {
			return &ProtocolError{Reason: fmt.Sprintf("WINDOW_UPDATE for unknown stream %d", f.StreamID)}
		}
		if len(f.Payload) != 8 {
			return &ProtocolError{Reason: "malformed WINDOW_UPDATE payload"}
		}
		seq := binary.BigEndian.Uint32(f.Payload[0:4])
		inc := binary.BigEndian.Uint32(f.Payload[4:8])
		if inc == 0 {
			return &ProtocolError{Reason: "zero window increment"}
		}
		if seq <= st.lastPeerWUSeq {
			return nil // duplicate or reordered: do not account twice
		}
		st.lastPeerWUSeq = seq
		st.sendWindow += int64(inc)
		return nil

	case FrameClose:
		st := s.streams[f.StreamID]
		if st == nil {
			return &ProtocolError{Reason: fmt.Sprintf("CLOSE for unknown stream %d", f.StreamID)}
		}
		switch st.state {
		case stateOpen:
			st.state = stateHalfClosedRemote
		case stateHalfClosedLocal:
			st.state = stateClosed
		default:
			return &ProtocolError{Reason: fmt.Sprintf("duplicate CLOSE on stream %d", f.StreamID)}
		}
		if s.OnClose != nil {
			s.OnClose(f.StreamID)
		}
		return nil

	case FrameReset:
		st := s.streams[f.StreamID]
		if st == nil {
			return &ProtocolError{Reason: fmt.Sprintf("RESET for unknown stream %d", f.StreamID)}
		}
		st.state = stateClosed
		if s.OnReset != nil {
			s.OnReset(f.StreamID)
		}
		return nil
	}
	return &ProtocolError{Reason: fmt.Sprintf("unknown frame type %d", f.Type)}
}
