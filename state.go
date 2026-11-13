package netsec

import (
	"errors"
	"fmt"
)

// TLS record content types (RFC 8446, section 5.1).
type RecordType = uint8

const (
	RecordTypeChangeCipherSpec RecordType = 20
	RecordTypeAlert            RecordType = 21
	RecordTypeHandshake        RecordType = 22
	RecordTypeApplicationData  RecordType = 23
)

// TLS alert levels.
type AlertLevel = uint8

const (
	AlertLevelWarning AlertLevel = 1
	AlertLevelFatal   AlertLevel = 2
)

// TLS alert descriptions.
type AlertDescription = uint8

const (
	AlertCloseNotify AlertDescription = 0
)

var (
	// ErrClosed is returned when a record is supplied after the connection
	// has ended.
	ErrClosed = errors.New("netsec: connection is closed")
)

// State is the connection state tracked by Machine.
type State uint8

const (
	// StateOpen means the machine is accepting handshake fragments and
	// alerts. The zero value is StateOpen.
	StateOpen State = iota

	// StateClosed means the connection ended. Any buffered, incomplete
	// handshake message has been discarded.
	StateClosed
)

// AlertError reports an alert that terminated the connection. A close_notify
// alert is a normal shutdown and is not reported as an AlertError.
type AlertError struct {
	Level       AlertLevel
	Description AlertDescription
}

func (e *AlertError) Error() string {
	return fmt.Sprintf("netsec: alert level=%d description=%d", e.Level, e.Description)
}

// HandshakeMessage is a complete TLS handshake message reassembled from one or
// more handshake records.
type HandshakeMessage struct {
	// Type is the TLS HandshakeType value.
	Type uint8

	// Body is the handshake message body, excluding the four-byte header.
	Body []byte

	// Raw is the complete encoded handshake message, including its header.
	Raw []byte
}

// Machine reassembles TLS handshake messages across record boundaries and
// tracks connection shutdown.
//
// The zero value is ready for use. A Machine is not safe for concurrent use.
type Machine struct {
	state   State
	pending []byte
}

// NewMachine returns an open Machine.
func NewMachine() *Machine {
	return &Machine{state: StateOpen}
}

// State returns the current connection state.
func (m *Machine) State() State {
	return m.state
}

// PendingBytes returns the number of bytes buffered for an incomplete
// handshake message. It is zero after the connection is closed.
func (m *Machine) PendingBytes() int {
	return len(m.pending)
}

// FeedRecord supplies one decoded TLS record to the state machine. payload is
// the record fragment, not including the record header.
//
// Handshake records are buffered until a complete handshake message is
// available; every complete message is returned. Alert records end the
// connection. A close_notify alert ends it normally, while any other alert is
// also returned as an *AlertError. In both cases an incomplete handshake
// message is discarded and can never be joined with bytes from a later record.
//
// Once the connection is closed, FeedRecord returns ErrClosed without
// inspecting payload.
func (m *Machine) FeedRecord(recordType RecordType, payload []byte) ([]HandshakeMessage, error) {
	if m.state == StateClosed {
		return nil, ErrClosed
	}

	switch recordType {
	case RecordTypeHandshake:
		return m.FeedHandshake(payload)
	case RecordTypeAlert:
		if len(payload) != 2 {
			m.close()
			return nil, fmt.Errorf("netsec: malformed alert record: got %d bytes, want 2", len(payload))
		}
		return nil, m.FeedAlert(payload[0], payload[1])
	default:
		return nil, nil
	}
}

// FeedHandshake supplies the fragment carried by one handshake record. It
// returns all handshake messages completed by the fragment. A trailing partial
// message remains buffered until more data arrives.
func (m *Machine) FeedHandshake(fragment []byte) ([]HandshakeMessage, error) {
	if m.state == StateClosed {
		return nil, ErrClosed
	}
	if len(fragment) == 0 {
		return nil, nil
	}

	m.pending = append(m.pending, fragment...)

	var messages []HandshakeMessage
	for {
		if len(m.pending) < 4 {
			break
		}

		bodyLen := int(m.pending[1])<<16 | int(m.pending[2])<<8 | int(m.pending[3])
		messageLen := 4 + bodyLen
		if len(m.pending) < messageLen {
			break
		}

		raw := make([]byte, messageLen)
		copy(raw, m.pending[:messageLen])

		body := make([]byte, bodyLen)
		copy(body, m.pending[4:messageLen])

		messages = append(messages, HandshakeMessage{
			Type: raw[0],
			Body: body,
			Raw:  raw,
		})
		m.pending = m.pending[messageLen:]
	}

	if len(m.pending) == 0 {
		m.pending = nil
	} else {
		// Do not retain the backing array of already-consumed messages.
		remaining := make([]byte, len(m.pending))
		copy(remaining, m.pending)
		m.pending = remaining
	}

	return messages, nil
}

// FeedAlert supplies one decoded alert. Every alert ends the connection and
// discards an incomplete handshake message. close_notify is a normal shutdown
// and returns nil; any other alert returns an *AlertError.
func (m *Machine) FeedAlert(level AlertLevel, description AlertDescription) error {
	if m.state == StateClosed {
		return ErrClosed
	}

	m.close()
	if description == AlertCloseNotify {
		return nil
	}
	return &AlertError{Level: level, Description: description}
}

// Close ends the connection and discards any incomplete handshake message.
// It is idempotent.
func (m *Machine) Close() {
	m.close()
}

func (m *Machine) close() {
	clear(m.pending)
	m.pending = nil
	m.state = StateClosed
}
