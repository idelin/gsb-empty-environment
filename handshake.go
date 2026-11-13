// Package netsec implements a strict TLS 1.2 client-side handshake
// message state machine. It enforces the exact server message order
// ServerHello, Certificate, ServerKeyExchange (ECDHE only),
// optional CertificateRequest, ServerHelloDone, ChangeCipherSpec,
// Finished, and requires that ChangeCipherSpec only arrives after the
// client has sent its ClientKeyExchange. Any out-of-order, duplicate,
// or missing message aborts the handshake with an unexpected_message
// alert and encryption is never enabled on a failed handshake.
package netsec

import "fmt"

// MessageType identifies a TLS 1.2 handshake or ChangeCipherSpec
// message relevant to the client state machine.
type MessageType uint8

const (
	MsgServerHello MessageType = iota + 1
	MsgCertificate
	MsgServerKeyExchange
	MsgCertificateRequest
	MsgServerHelloDone
	MsgChangeCipherSpec
	MsgFinished
)

func (t MessageType) String() string {
	switch t {
	case MsgServerHello:
		return "ServerHello"
	case MsgCertificate:
		return "Certificate"
	case MsgServerKeyExchange:
		return "ServerKeyExchange"
	case MsgCertificateRequest:
		return "CertificateRequest"
	case MsgServerHelloDone:
		return "ServerHelloDone"
	case MsgChangeCipherSpec:
		return "ChangeCipherSpec"
	case MsgFinished:
		return "Finished"
	default:
		return fmt.Sprintf("Unknown(%d)", uint8(t))
	}
}

// AlertUnexpectedMessage is the TLS alert description
// unexpected_message (10).
const AlertUnexpectedMessage uint8 = 10

// AlertError is returned when the peer violates the handshake
// protocol. The handshake must be aborted with the contained alert.
type AlertError struct {
	Alert  uint8
	Reason string
}

func (e *AlertError) Error() string {
	return fmt.Sprintf("tls: alert %d (unexpected_message): %s", e.Alert, e.Reason)
}

func unexpected(format string, args ...any) *AlertError {
	return &AlertError{Alert: AlertUnexpectedMessage, Reason: fmt.Sprintf(format, args...)}
}

type state uint8

const (
	stExpectServerHello state = iota
	stExpectCertificate
	stExpectServerKeyExchange // ECDHE cipher suites only
	stExpectCertificateRequestOrDone
	stExpectServerHelloDone
	stExpectClientKeyExchange // server flight complete; client must send first
	stExpectChangeCipherSpec
	stExpectFinished
	stEstablished
	stAborted
)

// ClientHandshake is a TLS 1.2 client handshake state machine.
// It is not safe for concurrent use.
type ClientHandshake struct {
	state                 state
	ecdhe                 bool
	clientKeyExchangeSent bool
	encryptionEnabled     bool
	abortErr              *AlertError
}

// NewClientHandshake creates a state machine for a TLS 1.2 client.
// ecdhe must be true when the negotiated cipher suite uses ECDHE key
// exchange, in which case a ServerKeyExchange message is mandatory.
func NewClientHandshake(ecdhe bool) *ClientHandshake {
	return &ClientHandshake{state: stExpectServerHello, ecdhe: ecdhe}
}

// EncryptionEnabled reports whether the handshake has reached the
// point where record-layer encryption is active. It only becomes true
// after a valid ChangeCipherSpec, which is unreachable unless every
// preceding message arrived in the correct order.
func (c *ClientHandshake) EncryptionEnabled() bool {
	return c.encryptionEnabled
}

// Established reports whether the handshake completed successfully.
func (c *ClientHandshake) Established() bool {
	return c.state == stEstablished
}

// SendClientKeyExchange records that the client has sent its
// ClientKeyExchange message. It is only valid after ServerHelloDone
// has been received; a ChangeCipherSpec from the server is rejected
// until this has been called.
func (c *ClientHandshake) SendClientKeyExchange() error {
	if c.state == stAborted {
		return c.abortErr
	}
	if c.state != stExpectClientKeyExchange {
		err := unexpected("client sent ClientKeyExchange out of order")
		c.abort(err)
		return err
	}
	c.clientKeyExchangeSent = true
	c.state = stExpectChangeCipherSpec
	return nil
}

// Receive processes one incoming server message. On any protocol
// violation it returns an *AlertError carrying unexpected_message,
// the state machine is permanently aborted, and encryption stays off.
func (c *ClientHandshake) Receive(msg MessageType) error {
	if c.state == stAborted {
		return c.abortErr
	}
	if err := c.transition(msg); err != nil {
		c.abort(err)
		return err
	}
	return nil
}

func (c *ClientHandshake) abort(err *AlertError) {
	c.state = stAborted
	c.encryptionEnabled = false
	c.abortErr = err
}

func (c *ClientHandshake) transition(msg MessageType) *AlertError {
	switch c.state {
	case stExpectServerHello:
		if msg != MsgServerHello {
			return unexpected("expected ServerHello, got %s", msg)
		}
		c.state = stExpectCertificate

	case stExpectCertificate:
		if msg != MsgCertificate {
			return unexpected("expected Certificate, got %s", msg)
		}
		if c.ecdhe {
			c.state = stExpectServerKeyExchange
		} else {
			c.state = stExpectCertificateRequestOrDone
		}

	case stExpectServerKeyExchange:
		if msg != MsgServerKeyExchange {
			return unexpected("expected ServerKeyExchange (ECDHE), got %s", msg)
		}
		c.state = stExpectCertificateRequestOrDone

	case stExpectCertificateRequestOrDone:
		switch msg {
		case MsgCertificateRequest:
			c.state = stExpectServerHelloDone
		case MsgServerHelloDone:
			c.state = stExpectClientKeyExchange
		default:
			return unexpected("expected CertificateRequest or ServerHelloDone, got %s", msg)
		}

	case stExpectServerHelloDone:
		if msg != MsgServerHelloDone {
			return unexpected("expected ServerHelloDone, got %s", msg)
		}
		c.state = stExpectClientKeyExchange

	case stExpectClientKeyExchange:
		// The client must send ClientKeyExchange before any further
		// server message; an early ChangeCipherSpec lands here.
		if msg == MsgChangeCipherSpec && !c.clientKeyExchangeSent {
			return unexpected("ChangeCipherSpec received before client ClientKeyExchange")
		}
		return unexpected("expected client ClientKeyExchange first, got %s", msg)

	case stExpectChangeCipherSpec:
		if msg != MsgChangeCipherSpec {
			return unexpected("expected ChangeCipherSpec, got %s", msg)
		}
		c.encryptionEnabled = true
		c.state = stExpectFinished

	case stExpectFinished:
		if msg != MsgFinished {
			return unexpected("expected Finished, got %s", msg)
		}
		c.state = stEstablished

	case stEstablished:
		return unexpected("handshake already established, got %s", msg)

	default:
		return unexpected("unexpected message %s", msg)
	}
	return nil
}
