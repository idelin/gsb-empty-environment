package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

// Close status codes (RFC 6455 §7.4.1).
const (
	StatusNormalClosure           uint16 = 1000
	StatusGoingAway               uint16 = 1001
	StatusProtocolError           uint16 = 1002
	StatusUnsupportedData         uint16 = 1003
	StatusNoStatusRcvd            uint16 = 1005
	StatusAbnormalClosure         uint16 = 1006
	StatusInvalidFramePayloadData uint16 = 1007
	StatusPolicyViolation         uint16 = 1008
	StatusMessageTooBig           uint16 = 1009
	StatusMandatoryExtension      uint16 = 1010
	StatusInternalServerError     uint16 = 1011
	StatusTLSHandshake            uint16 = 1015
)

// ErrClosed is returned by Closer methods once the transport is terminated.
var ErrClosed = errors.New("netsec: connection closed")

var (
	errCloseStatusCode = errors.New("netsec: invalid close status code")
	errCloseReasonUTF8 = errors.New("netsec: close reason is not valid UTF-8")
)

// validCloseCode reports whether code may appear on the wire in a close
// frame. Reserved and undefined ranges are rejected.
func validCloseCode(code uint16) bool {
	switch code {
	case 1004, StatusNoStatusRcvd, StatusAbnormalClosure, StatusTLSHandshake:
		return false
	}
	if code < 1000 || code >= 5000 {
		return false
	}
	if code >= 1016 && code <= 2999 {
		return false
	}
	return true
}

// parseClosePayload validates a close frame body: an optional 2-byte status
// code followed by a UTF-8 reason. An empty payload means "no status".
func parseClosePayload(payload []byte) (code uint16, reason string, err error) {
	if len(payload) == 0 {
		return StatusNoStatusRcvd, "", nil
	}
	if len(payload) == 1 {
		return 0, "", fmt.Errorf("%w: truncated status code", errCloseStatusCode)
	}
	code = binary.BigEndian.Uint16(payload)
	if !validCloseCode(code) {
		return 0, "", fmt.Errorf("%w: %d", errCloseStatusCode, code)
	}
	if !utf8.Valid(payload[2:]) {
		return 0, "", errCloseReasonUTF8
	}
	return code, string(payload[2:]), nil
}

func closePayload(code uint16, reason string) []byte {
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p, code)
	copy(p[2:], reason)
	return p
}

// State identifies the phase of the close handshake.
type State int

const (
	// StateOpen: no close frame sent or received yet.
	StateOpen State = iota
	// StateCloseSent: our close frame was handed to the transport.
	StateCloseSent
	// StateCloseReceived: the peer's close frame arrived; only the close
	// response may still be written.
	StateCloseReceived
	// StateWriteDone: the transport finished writing our close frame.
	StateWriteDone
	// StateTerminated: the transport is gone; nothing more may happen.
	StateTerminated
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateCloseSent:
		return "close-sent"
	case StateCloseReceived:
		return "close-received"
	case StateWriteDone:
		return "write-done"
	case StateTerminated:
		return "terminated"
	}
	return "unknown"
}

// EventKind distinguishes business messages from the (single) close event.
type EventKind int

const (
	// EventMessage carries an ordinary data frame payload.
	EventMessage EventKind = iota
	// EventClose is delivered exactly once when the connection is closed.
	EventClose
)

// Event is a notification from the Closer to the transport host.
type Event struct {
	Kind EventKind
	// Message is set for EventMessage.
	Message []byte
	// Code and Reason describe the peer's close frame (or 1005/"" when the
	// transport died before any close frame arrived).
	Code   uint16
	Reason string
	// Err carries the transport/protocol failure, if any. It is only ever
	// attached to EventClose, never published as an EventMessage.
	Err error
}

// Closer is a close-handshake state machine driven by a Go transport host.
// It is safe for concurrent use.
type Closer struct {
	mu      sync.Mutex
	handler func(Event)
	parser  frameParser

	closeSent     bool
	closeReceived bool
	writeDone     bool
	terminated    bool
	fired         bool

	code   uint16
	reason string
	err    error
}

// NewCloser returns a Closer that reports events to handler. The handler is
// never invoked while the Closer holds its lock, so it may call back in.
func NewCloser(handler func(Event)) *Closer {
	if handler == nil {
		handler = func(Event) {}
	}
	return &Closer{handler: handler}
}

// State reports the current handshake phase.
func (c *Closer) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.terminated:
		return StateTerminated
	case c.writeDone && (c.closeSent || c.closeReceived):
		return StateWriteDone
	case c.closeReceived:
		return StateCloseReceived
	case c.closeSent:
		return StateCloseSent
	default:
		return StateOpen
	}
}

// WriteClose initiates the close handshake and returns the close frame the
// host must write. The status code and reason are validated. After a close
// frame was received, WriteClose is still permitted exactly once: it is the
// close response.
func (c *Closer) WriteClose(code uint16, reason string) ([]byte, error) {
	if !validCloseCode(code) {
		return nil, fmt.Errorf("%w: %d", errCloseStatusCode, code)
	}
	if !utf8.ValidString(reason) {
		return nil, errCloseReasonUTF8
	}
	payload := closePayload(code, reason)
	if len(payload) > 125 {
		return nil, errors.New("netsec: close reason exceeds 123 bytes")
	}

	c.mu.Lock()
	if c.terminated {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.closeSent {
		c.mu.Unlock()
		return nil, errors.New("netsec: close frame already sent")
	}
	c.closeSent = true
	events := c.maybeFireLocked()
	c.mu.Unlock()

	c.dispatch(events)
	return marshalFrame(opcodeClose, payload), nil
}

// Feed consumes inbound bytes and returns outbound bytes the host must
// write (a close response or pong), if any. Frames may be split across
// calls. Data frames arriving after a close frame was received are dropped
// rather than published as messages.
func (c *Closer) Feed(data []byte) ([]byte, error) {
	c.mu.Lock()
	if c.terminated {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	frames, err := c.parser.feed(data)
	if err != nil {
		out := c.failLocked(StatusProtocolError, err)
		events := c.maybeFireLocked()
		c.mu.Unlock()
		c.dispatch(events)
		return out, err
	}

	var out []byte
	var events []Event
	for _, f := range frames {
		switch f.opcode {
		case opcodeText, opcodeBinary, opcodeContinuation:
			if c.closeReceived {
				// Buffered data delivered after the close frame must
				// not surface as a business message.
				continue
			}
			events = append(events, Event{Kind: EventMessage, Message: f.payload})
		case opcodeClose:
			code, reason, cerr := parseClosePayload(f.payload)
			if cerr != nil {
				failCode := StatusProtocolError
				if errors.Is(cerr, errCloseReasonUTF8) {
					failCode = StatusInvalidFramePayloadData
				}
				out = append(out, c.failLocked(failCode, cerr)...)
				events = append(events, c.maybeFireLocked()...)
				c.mu.Unlock()
				c.dispatch(events)
				return out, cerr
			}
			if c.closeReceived {
				continue // duplicate close frame
			}
			c.closeReceived = true
			c.code, c.reason = code, reason
			if !c.closeSent {
				// The only frame allowed after a received close: the
				// close response, echoing the peer's code and reason.
				c.closeSent = true
				out = append(out, marshalFrame(opcodeClose, f.payload)...)
			}
		case opcodePing:
			if !c.closeReceived && !c.closeSent {
				out = append(out, marshalFrame(opcodePong, f.payload)...)
			}
		case opcodePong:
			// Nothing to do.
		}
	}
	events = append(events, c.maybeFireLocked()...)
	c.mu.Unlock()

	c.dispatch(events)
	return out, nil
}

// WriteDone tells the Closer the transport finished writing our close frame.
func (c *Closer) WriteDone() {
	c.mu.Lock()
	c.writeDone = true
	events := c.maybeFireLocked()
	c.mu.Unlock()
	c.dispatch(events)
}

// Terminate aborts the transport. err (which may be nil) is attached to the
// single close event and never published as a business message.
func (c *Closer) Terminate(err error) {
	c.mu.Lock()
	if c.terminated {
		c.mu.Unlock()
		return
	}
	c.terminated = true
	if c.err == nil {
		c.err = err
	}
	events := c.maybeFireLocked()
	c.mu.Unlock()
	c.dispatch(events)
}

// failLocked records a protocol failure, queues a close frame with the
// given status code (unless one was already sent), and terminates. The
// caller must hold c.mu.
func (c *Closer) failLocked(code uint16, err error) []byte {
	var out []byte
	if !c.closeSent {
		c.closeSent = true
		out = marshalFrame(opcodeClose, closePayload(code, ""))
	}
	if c.err == nil {
		c.err = err
	}
	c.terminated = true
	return out
}

// maybeFireLocked emits the single close event once the handshake is
// complete: either the transport terminated, or close was exchanged both
// ways and our close write finished. The caller must hold c.mu.
func (c *Closer) maybeFireLocked() []Event {
	if c.fired {
		return nil
	}
	complete := c.terminated || (c.closeReceived && c.closeSent && c.writeDone)
	if !complete {
		return nil
	}
	c.fired = true
	return []Event{{
		Kind:   EventClose,
		Code:   c.code,
		Reason: c.reason,
		Err:    c.err,
	}}
}

func (c *Closer) dispatch(events []Event) {
	for _, e := range events {
		c.handler(e)
	}
}
