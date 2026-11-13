package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Event is a client-authentication handshake message or state reported by a
// TLS stack. State and message values intentionally share one type so callers
// can compare the machine's current state with the states that produced it.
type Event string

// State, Message, and the other aliases describe the same ordered handshake
// sequence. They allow a TLS stack to use whichever terminology it exposes.
type (
	State             = Event
	Message           = Event
	HandshakeEvent    = Event
	HandshakeState    = Event
	HandshakeMessage  = Event
	ClientAuthState   = Event
	ClientAuthMessage = Event
)

const (
	Start              Event = "Start"
	CertificateRequest Event = "CertificateRequest"
	Certificate        Event = "Certificate"
	CertificateVerify  Event = "CertificateVerify"
	Finished           Event = "Finished"
)

// State names used by TLS stacks that distinguish the state entered after each
// message from the message itself.
const (
	StateInitial                   = Start
	StateStart                     = Start
	StateCertificateRequest        = CertificateRequest
	StateCertificateRequested      = CertificateRequest
	StateAwaitingCertificate       = CertificateRequest
	StateCertificate               = Certificate
	StateCertificateReceived       = Certificate
	StateAwaitingCertificateVerify = Certificate
	StateCertificateVerify         = CertificateVerify
	StateCertificateVerified       = CertificateVerify
	StateAwaitingFinished          = CertificateVerify
	StateFinished                  = Finished
	StateComplete                  = Finished
)

// Message names keep the handshake vocabulary explicit for callers that do not
// use the shorter constants above.
const (
	MessageStart              = Start
	MessageCertificateRequest = CertificateRequest
	MessageCertificate        = Certificate
	MessageCertificateVerify  = CertificateVerify
	MessageFinished           = Finished
)

var (
	// ErrUnexpectedEvent reports a message that is valid TLS vocabulary but
	// cannot occur at the current point in the client-authentication exchange.
	ErrUnexpectedEvent = errors.New("netsec: unexpected handshake event")

	// ErrUnknownEvent reports input that is neither a known TLS handshake state
	// nor a recognised alias.
	ErrUnknownEvent = errors.New("netsec: unknown handshake event")

	// ErrCertificateRequired is returned when Finished arrives after a
	// CertificateRequest but before the client Certificate.
	ErrCertificateRequired = errors.New("netsec: client Certificate required after CertificateRequest")

	// ErrCertificateVerifyRequired is returned when Finished arrives after a
	// client Certificate but before its CertificateVerify.
	ErrCertificateVerifyRequired = errors.New("netsec: CertificateVerify required before Finished")

	// ErrHandshakeComplete reports another client-authentication message after
	// the exchange has already finished.
	ErrHandshakeComplete = errors.New("netsec: client-authentication exchange already complete")
)

// Compatibility aliases for callers that use "state" or "missing" wording.
var (
	ErrUnexpectedState          = ErrUnexpectedEvent
	ErrUnknownState             = ErrUnknownEvent
	ErrMissingCertificate       = ErrCertificateRequired
	ErrCertificateMissing       = ErrCertificateRequired
	ErrMissingVerify            = ErrCertificateVerifyRequired
	ErrCertificateVerifyMissing = ErrCertificateVerifyRequired
)

func unexpected(event Event, target State) error {
	if event == Finished {
		switch target {
		case CertificateRequest:
			return fmt.Errorf("%w: received Finished before Certificate: %w",
				ErrUnexpectedEvent, ErrCertificateRequired)
		case Certificate:
			return fmt.Errorf("%w: received Finished before CertificateVerify: %w",
				ErrUnexpectedEvent, ErrCertificateVerifyRequired)
		}
	}
	return fmt.Errorf("%w: %s while in state %s", ErrUnexpectedEvent, event, target)
}

// parseEvent canonicalises the spelling variants commonly used by TLS stacks.
// The second result is false for unrelated handshake states, which the machine
// deliberately ignores while a client-authentication exchange is not active.
func parseEvent(value any) (Event, bool, error) {
	raw, numeric, err := eventString(value)
	if err != nil {
		return "", false, err
	}

	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, raw)

	switch name {
	case "", "start", "initial", "idle", "begin", "statebegin", "stateinitial", "stateidle":
		return Start, true, nil
	case "certificaterequest", "certificaterequested", "requestcertificate",
		"requestedcertificate", "clientcertificaterequest", "clientcertrequest",
		"certrequest", "certreq":
		return CertificateRequest, true, nil
	case "certificate", "clientcertificate", "clientcert", "cert", "statecertificate":
		return Certificate, true, nil
	case "certificateverify", "certificateverified", "clientcertificateverify",
		"clientcertverify", "certverify", "statecertificateverify":
		return CertificateVerify, true, nil
	case "finished", "finish", "clientfinished", "statefinished", "complete", "completed":
		return Finished, true, nil
	}

	if numeric {
		switch raw {
		case "0":
			return Start, true, nil
		case "1":
			return CertificateRequest, true, nil
		case "2":
			return Certificate, true, nil
		case "3":
			return CertificateVerify, true, nil
		case "4":
			return Finished, true, nil
		}
	}

	if foreignHandshakeState(name) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("%w: %q", ErrUnknownEvent, raw)
}

func eventString(value any) (raw string, numeric bool, err error) {
	switch v := value.(type) {
	case nil:
		return "", false, fmt.Errorf("%w: nil", ErrUnknownEvent)
	case Event:
		return string(v), false, nil
	case string:
		return v, false, nil
	case fmt.Stringer:
		return v.String(), false, nil
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), false, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprint(rv.Int()), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprint(rv.Uint()), true, nil
	default:
		return "", false, fmt.Errorf("%w: %T", ErrUnknownEvent, value)
	}
}

func foreignHandshakeState(name string) bool {
	switch name {
	case "clienthello", "serverhello", "serverhellodone",
		"encryptedextensions", "servercertificate", "servercertificatestatus",
		"servercertificateverify", "serverfinished",
		"clientkeyexchange", "serverkeyexchange", "changecipherspec",
		"endofearlydata", "newsessionticket", "keyupdate",
		"applicationdata", "alert", "handshake":
		return true
	default:
		return false
	}
}
