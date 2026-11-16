package netsec

import (
	"errors"
	"fmt"
)

// TLS 1.2 record layer content types (RFC 5246, section 6.2.1).
const (
	RecordTypeChangeCipherSpec uint8 = 20
	RecordTypeAlert            uint8 = 21
	RecordTypeHandshake        uint8 = 22
	RecordTypeApplicationData  uint8 = 23
)

// TLS 1.2 handshake message types (RFC 5246, section 7.4).
const (
	HandshakeHelloRequest       uint8 = 0
	HandshakeClientHello        uint8 = 1
	HandshakeServerHello        uint8 = 2
	HandshakeCertificate        uint8 = 11
	HandshakeServerKeyExchange  uint8 = 12
	HandshakeCertificateRequest uint8 = 13
	HandshakeServerHelloDone    uint8 = 14
	HandshakeCertificateVerify  uint8 = 15
	HandshakeClientKeyExchange  uint8 = 16
	HandshakeFinished           uint8 = 20
)

// AlertDescription carries the TLS 1.2 alert numbers used when the state
// machine rejects an inbound record (RFC 5246, section 7.2.2).
type AlertDescription uint8

const (
	AlertUnexpectedMessage AlertDescription = 10
	AlertIllegalParameter  AlertDescription = 47
)

// HandshakeState is one discrete state of the TLS 1.2 client handshake.
type HandshakeState uint8

const (
	// StateNew is the zero value before Start has been called.
	StateNew HandshakeState = iota
	// StateWaitServerHello expects ServerHello next.
	StateWaitServerHello
	// StateWaitCertificate expects Certificate next.
	StateWaitCertificate
	// StateWaitServerKeyExchange expects the ECDHE ServerKeyExchange next.
	StateWaitServerKeyExchange
	// StateWaitCertificateRequestOrDone accepts CertificateRequest or
	// ServerHelloDone next.
	StateWaitCertificateRequestOrDone
	// StateWaitServerHelloDone expects ServerHelloDone next and is only
	// reached after a CertificateRequest.
	StateWaitServerHelloDone
	// StateWaitChangeCipherSpec waits for the server CCS while the client is
	// expected to have already transmitted ClientKeyExchange.
	StateWaitChangeCipherSpec
	// StateWaitFinished expects the encrypted server Finished next.
	StateWaitFinished
	// StateComplete is reached after a valid Finished.
	StateComplete
	// StateFailed is terminal: the handshake was aborted by a protocol error.
	StateFailed
)

// HandshakeError is returned for every state-machine violation. The caller
// should translate Alert into the matching TLS alert record and tear the
// connection down.
type HandshakeError struct {
	// Alert is the alert description number to send to the peer.
	Alert AlertDescription
	// State is the state the machine was in when the violation was observed.
	State HandshakeState
	// Message is a human-readable explanation.
	Message string
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("tls handshake error: %s (alert %d, state %s)",
		e.Message, e.Alert, e.State)
}

// TLS12ClientHandshake is a sequencing-only state machine for the receiving
// side of a TLS 1.2 client key exchange. It enforces the inbound order
//
//	ServerHello -> Certificate -> ServerKeyExchange (ECDHE only) ->
//	[CertificateRequest] -> ServerHelloDone -> ChangeCipherSpec -> Finished
//
// and additionally requires the local ClientKeyExchange to be signalled via
// NotifyClientKeyExchangeSent before any inbound ChangeCipherSpec is accepted.
// Any out-of-order, duplicate, or missing message aborts the handshake with
// unexpected_message and never activates inbound encryption (ReadEncrypted
// stays false before the abort). A single TLS12ClientHandshake must not be
// used concurrently.
type TLS12ClientHandshake struct {
	state       HandshakeState
	ckeSent     bool
	readCrypto  bool
	certRequest bool
}

// NewTLS12ClientHandshake returns a machine already positioned in
// StateWaitServerHello, ready to consume the server's first flight.
func NewTLS12ClientHandshake() *TLS12ClientHandshake {
	return &TLS12ClientHandshake{state: StateWaitServerHello}
}

// Start moves a zero-value machine from StateNew into StateWaitServerHello.
// It is an error to start a machine more than once.
func (m *TLS12ClientHandshake) Start() error {
	if m.state != StateNew {
		return m.fail(AlertUnexpectedMessage, "Start called in state "+m.state.String())
	}
	m.state = StateWaitServerHello
	return nil
}

// State reports the current handshake state.
func (m *TLS12ClientHandshake) State() HandshakeState { return m.state }

// ReadEncrypted reports whether inbound records are protected. It only
// becomes true after a server ChangeCipherSpec has been accepted, which
// itself requires a prior local ClientKeyExchange.
func (m *TLS12ClientHandshake) ReadEncrypted() bool { return m.readCrypto }

// CertificateRequestSeen reports whether the server requested a client
// certificate during the handshake.
func (m *TLS12ClientHandshake) CertificateRequestSeen() bool {
	return m.certRequest
}

// Complete reports whether the handshake finished successfully.
func (m *TLS12ClientHandshake) Complete() bool { return m.state == StateComplete }

func (m *TLS12ClientHandshake) fail(alert AlertDescription, msg string) *HandshakeError {
	at := m.state
	m.state = StateFailed
	return &HandshakeError{Alert: alert, State: at, Message: msg}
}

// HandleChangeCipherSpec processes an inbound ChangeCipherSpec record
// payload. A CCS is accepted only when the machine is waiting for it and
// NotifyClientKeyExchangeSent has already been called. Early, duplicate, or
// otherwise misplaced CCS messages abort with unexpected_message without
// activating inbound encryption. A malformed CCS payload (the single byte
// must be 1) aborts with illegal_parameter.
func (m *TLS12ClientHandshake) HandleChangeCipherSpec(payload []byte) error {
	if m.state == StateFailed || m.state == StateComplete {
		return m.fail(AlertUnexpectedMessage,
			"change_cipher_spec received in terminal state "+m.state.String())
	}
	if len(payload) != 1 || payload[0] != 1 {
		return m.fail(AlertIllegalParameter, "malformed change_cipher_spec payload")
	}
	if m.state != StateWaitChangeCipherSpec || !m.ckeSent {
		return m.fail(AlertUnexpectedMessage,
			"change_cipher_spec before client_key_exchange or out of order")
	}
	m.readCrypto = true
	m.state = StateWaitFinished
	return nil
}

// HandleHandshake processes one inbound handshake message. msgType is the
// handshake type byte; the body is intentionally ignored because this machine
// validates sequencing only and leaves cryptographic verification to the
// record/crypto layer. Every unexpected, duplicate, or missing message moves
// the machine into StateFailed and returns unexpected_message.
func (m *TLS12ClientHandshake) HandleHandshake(msgType uint8, body []byte) error {
	switch m.state {
	case StateWaitServerHello:
		if msgType != HandshakeServerHello {
			return m.fail(AlertUnexpectedMessage,
				"expected server_hello, got "+handshakeName(msgType))
		}
		m.state = StateWaitCertificate
	case StateWaitCertificate:
		if msgType != HandshakeCertificate {
			return m.fail(AlertUnexpectedMessage,
				"expected certificate, got "+handshakeName(msgType))
		}
		m.state = StateWaitServerKeyExchange
	case StateWaitServerKeyExchange:
		if msgType != HandshakeServerKeyExchange {
			return m.fail(AlertUnexpectedMessage,
				"expected server_key_exchange, got "+handshakeName(msgType))
		}
		m.state = StateWaitCertificateRequestOrDone
	case StateWaitCertificateRequestOrDone:
		switch msgType {
		case HandshakeCertificateRequest:
			m.certRequest = true
			m.state = StateWaitServerHelloDone
		case HandshakeServerHelloDone:
			m.state = StateWaitChangeCipherSpec
		default:
			return m.fail(AlertUnexpectedMessage,
				"expected certificate_request or server_hello_done, got "+
					handshakeName(msgType))
		}
	case StateWaitServerHelloDone:
		if msgType != HandshakeServerHelloDone {
			return m.fail(AlertUnexpectedMessage,
				"expected server_hello_done, got "+handshakeName(msgType))
		}
		m.state = StateWaitChangeCipherSpec
	case StateWaitChangeCipherSpec:
		return m.fail(AlertUnexpectedMessage,
			"unexpected handshake message "+handshakeName(msgType)+
				" while waiting on the encrypted phase")
	case StateWaitFinished:
		// Finished is itself a handshake message (type 20), even though it is
		// transmitted under the newly negotiated keys.
		if msgType != HandshakeFinished {
			return m.fail(AlertUnexpectedMessage,
				"expected finished, got "+handshakeName(msgType))
		}
		m.state = StateComplete
	case StateComplete:
		return m.fail(AlertUnexpectedMessage,
			"handshake already complete; got "+handshakeName(msgType))
	default:
		return m.fail(AlertUnexpectedMessage,
			"handshake message "+handshakeName(msgType)+" on a non-running handshake")
	}
	return nil
}

// NotifyClientKeyExchangeSent tells the machine that the local
// ClientKeyExchange (and, where negotiated, the local CCS/Finished flight)
// has been transmitted. It is valid only between ServerHelloDone and the
// inbound ChangeCipherSpec. Calling it earlier, twice, or after the
// encrypted phase has started aborts with unexpected_message.
func (m *TLS12ClientHandshake) NotifyClientKeyExchangeSent() error {
	if m.state != StateWaitChangeCipherSpec {
		return m.fail(AlertUnexpectedMessage,
			"client_key_exchange signalled in state "+m.state.String())
	}
	if m.ckeSent {
		return m.fail(AlertUnexpectedMessage,
			"duplicate client_key_exchange notification")
	}
	m.ckeSent = true
	return nil
}

// HandleRecord dispatches one TLS record by its content type. It is a thin
// adapter over HandleChangeCipherSpec and HandleHandshake so a record layer
// can feed parsed records directly. Alert and application-data records are
// never valid while the handshake is still in progress and abort it.
func (m *TLS12ClientHandshake) HandleRecord(contentType uint8, fragment []byte) error {
	switch contentType {
	case RecordTypeChangeCipherSpec:
		return m.HandleChangeCipherSpec(fragment)
	case RecordTypeHandshake:
		if len(fragment) < 4 {
			return m.fail(AlertIllegalParameter, "truncated handshake record")
		}
		return m.HandleHandshake(fragment[0], fragment[4:])
	case RecordTypeAlert:
		return m.fail(AlertUnexpectedMessage, "alert record received during handshake")
	case RecordTypeApplicationData:
		return m.fail(AlertUnexpectedMessage,
			"application_data received before handshake completion")
	default:
		return m.fail(AlertUnexpectedMessage,
			fmt.Sprintf("unknown record content type %d", contentType))
	}
}

// AsHandshakeError unwraps a state-machine error.
func AsHandshakeError(err error) (*HandshakeError, bool) {
	var he *HandshakeError
	if errors.As(err, &he) {
		return he, true
	}
	return nil, false
}

func handshakeName(t uint8) string {
	switch t {
	case HandshakeHelloRequest:
		return "hello_request"
	case HandshakeClientHello:
		return "client_hello"
	case HandshakeServerHello:
		return "server_hello"
	case HandshakeCertificate:
		return "certificate"
	case HandshakeServerKeyExchange:
		return "server_key_exchange"
	case HandshakeCertificateRequest:
		return "certificate_request"
	case HandshakeServerHelloDone:
		return "server_hello_done"
	case HandshakeCertificateVerify:
		return "certificate_verify"
	case HandshakeClientKeyExchange:
		return "client_key_exchange"
	case HandshakeFinished:
		return "finished"
	default:
		return fmt.Sprintf("handshake_type(%d)", t)
	}
}

// String renders the state name for diagnostics and tests.
func (s HandshakeState) String() string {
	switch s {
	case StateNew:
		return "new"
	case StateWaitServerHello:
		return "wait_server_hello"
	case StateWaitCertificate:
		return "wait_certificate"
	case StateWaitServerKeyExchange:
		return "wait_server_key_exchange"
	case StateWaitCertificateRequestOrDone:
		return "wait_certificate_request_or_done"
	case StateWaitServerHelloDone:
		return "wait_server_hello_done"
	case StateWaitChangeCipherSpec:
		return "wait_change_cipher_spec"
	case StateWaitFinished:
		return "wait_finished"
	case StateComplete:
		return "complete"
	case StateFailed:
		return "failed"
	default:
		return fmt.Sprintf("state(%d)", uint8(s))
	}
}

// String renders the alert number.
func (a AlertDescription) String() string {
	switch a {
	case AlertUnexpectedMessage:
		return "unexpected_message"
	case AlertIllegalParameter:
		return "illegal_parameter"
	default:
		return fmt.Sprintf("alert(%d)", uint8(a))
	}
}
