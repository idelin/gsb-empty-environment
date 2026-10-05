// Package framedstream implements a length-prefixed, bidirectional
// streaming state machine.
//
// The design is sans-IO: the caller feeds inbound bytes with Input and
// drains outbound bytes with Outbound, while the Session emits Events.
// Each direction is independent: either side may CloseSend (half-close)
// to signal it will send no more frames, while continuing to receive.
// Reset terminates both directions immediately.
//
// Wire format:
//
//	frame   := uint32be length | payload[length]     (length <= MaxPayload)
//	control := uint32be 0xFFFFFFFF | byte type
//	type    := 0x01 FIN (half-close) | 0x02 RST (reset)
package framedstream

import (
	"encoding/binary"
	"errors"
)

const (
	headerLen     = 4
	controlMarker = 0xFFFFFFFF

	ctrlFin byte = 0x01
	ctrlRst byte = 0x02

	// MaxPayload is the largest accepted frame payload.
	MaxPayload = 16 << 20
)

var (
	// ErrSendClosed is returned by SendFrame/CloseSend after the local
	// send side has been half-closed.
	ErrSendClosed = errors.New("framedstream: send side is closed")
	// ErrReset is returned when the session has been reset.
	ErrReset = errors.New("framedstream: session reset")
	// ErrPayloadTooLarge is returned when a frame exceeds MaxPayload.
	ErrPayloadTooLarge = errors.New("framedstream: payload exceeds MaxPayload")
)

// EventKind identifies the kind of an Event.
type EventKind int

const (
	// EventFrame delivers one complete inbound frame.
	EventFrame EventKind = iota
	// EventPeerFinished means the peer half-closed its send side (FIN):
	// the peer will send no more frames, but may still receive ours.
	// This is the normal end of the peer's stream.
	EventPeerFinished
	// EventPeerTruncated means the transport closed in the middle of a
	// frame: a partial header or payload was buffered and is lost.
	EventPeerTruncated
	// EventPeerAborted means the transport closed while the peer's send
	// side was still open (no FIN received) but no partial frame was
	// pending: the peer did not finish its stream.
	EventPeerAborted
	// EventReset means the session was reset, by the peer (RST), by a
	// protocol violation, or locally via Reset. Both directions are dead.
	EventReset
)

// Event is emitted by Input and TransportClosed.
type Event struct {
	Kind EventKind
	// Frame is the payload for EventFrame.
	Frame []byte
	// Partial is the number of buffered bytes of the incomplete frame
	// for EventPeerTruncated.
	Partial int
}

type sendState int

const (
	sendOpen sendState = iota
	sendClosed
	sendReset
)

type recvState int

const (
	recvOpen recvState = iota
	recvClosed
	recvReset
)

// Session is one endpoint of a framed bidirectional stream.
type Session struct {
	send sendState
	recv recvState

	in       []byte // reassembly buffer for inbound bytes
	out      []byte // pending outbound bytes
	frameLen int  // expected payload length, -1 while reading a header
}

// NewSession returns a Session with both directions open.
func NewSession() *Session {
	return &Session{frameLen: -1}
}

// SendFrame queues one length-prefixed frame for transmission.
func (s *Session) SendFrame(payload []byte) error {
	switch s.send {
	case sendReset:
		return ErrReset
	case sendClosed:
		return ErrSendClosed
	}
	if len(payload) > MaxPayload {
		return ErrPayloadTooLarge
	}
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	s.out = append(s.out, hdr[:]...)
	s.out = append(s.out, payload...)
	return nil
}

// CloseSend half-closes the local send side: a FIN control frame is
// queued and no further frames may be sent, but the receive side stays
// open until the peer finishes or the session resets.
func (s *Session) CloseSend() error {
	switch s.send {
	case sendReset:
		return ErrReset
	case sendClosed:
		return ErrSendClosed
	}
	s.enqueueControl(ctrlFin)
	s.send = sendClosed
	return nil
}

// Reset terminates both directions immediately and queues an RST
// control frame for the peer. Buffered inbound bytes are discarded.
func (s *Session) Reset() {
	if s.send != sendReset {
		s.enqueueControl(ctrlRst)
	}
	s.send = sendReset
	s.recv = recvReset
	s.in = nil
	s.frameLen = -1
}

// SendClosed reports whether the local send side is half-closed.
func (s *Session) SendClosed() bool { return s.send != sendOpen }

// RecvClosed reports whether the peer's stream has ended (FIN) or reset.
func (s *Session) RecvClosed() bool { return s.recv != recvOpen }

// Done reports whether both directions are terminated.
func (s *Session) Done() bool { return s.send != sendOpen && s.recv != recvOpen }

// Outbound drains and returns bytes queued for the transport.
func (s *Session) Outbound() []byte {
	if len(s.out) == 0 {
		return nil
	}
	out := s.out
	s.out = nil
	return out
}

// Input feeds inbound bytes from the transport and returns the events
// they produce. Bytes may arrive in arbitrarily small pieces.
func (s *Session) Input(p []byte) []Event {
	if s.recv != recvOpen {
		// After FIN or reset, further inbound bytes are meaningless.
		return nil
	}
	s.in = append(s.in, p...)
	var events []Event
	for s.recv == recvOpen {
		ev, ok := s.parseOne()
		if !ok {
			break
		}
		events = append(events, ev)
	}
	if s.recv != recvOpen {
		s.in = nil
		s.frameLen = -1
	}
	return events
}

// TransportClosed must be called when the underlying transport reaches
// EOF or is closed. It reports how the peer's stream ended:
//
//   - EventPeerTruncated: a frame was only partially received.
//   - EventPeerAborted:   no FIN was received, but nothing was mid-frame.
//   - no event:           the peer sent FIN first (normal end).
//
// After this call the receive side is closed.
func (s *Session) TransportClosed() []Event {
	if s.recv == recvReset {
		return nil
	}
	var events []Event
	if s.recv == recvOpen {
		if partial := s.partialBytes(); partial > 0 {
			events = append(events, Event{Kind: EventPeerTruncated, Partial: partial})
		} else {
			events = append(events, Event{Kind: EventPeerAborted})
		}
	}
	s.recv = recvReset
	s.in = nil
	s.frameLen = -1
	return events
}

// partialBytes counts bytes belonging to an incomplete inbound frame.
func (s *Session) partialBytes() int {
	if s.frameLen >= 0 {
		return headerLen + len(s.in)
	}
	return len(s.in)
}

func (s *Session) parseOne() (Event, bool) {
	if s.frameLen < 0 {
		if len(s.in) < headerLen {
			return Event{}, false
		}
		n := binary.BigEndian.Uint32(s.in[:headerLen])
		if n == controlMarker {
			if len(s.in) < headerLen+1 {
				return Event{}, false
			}
			typ := s.in[headerLen]
			s.in = s.in[headerLen+1:]
			switch typ {
			case ctrlFin:
				s.recv = recvClosed
				return Event{Kind: EventPeerFinished}, true
			case ctrlRst:
				s.recv = recvReset
				s.send = sendReset
				return Event{Kind: EventReset}, true
			default:
				return s.protocolError(), true
			}
		}
		if n > MaxPayload {
			return s.protocolError(), true
		}
		s.frameLen = int(n)
		s.in = s.in[headerLen:]
	}
	if len(s.in) < s.frameLen {
		return Event{}, false
	}
	frame := make([]byte, s.frameLen)
	copy(frame, s.in[:s.frameLen])
	s.in = s.in[s.frameLen:]
	s.frameLen = -1
	return Event{Kind: EventFrame, Frame: frame}, true
}

// protocolError resets both directions and queues an RST for the peer.
func (s *Session) protocolError() Event {
	s.recv = recvReset
	if s.send != sendReset {
		s.enqueueControl(ctrlRst)
	}
	s.send = sendReset
	return Event{Kind: EventReset}
}

func (s *Session) enqueueControl(typ byte) {
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], controlMarker)
	s.out = append(s.out, hdr[:]...)
	s.out = append(s.out, typ)
}
