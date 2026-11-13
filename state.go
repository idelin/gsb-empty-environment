package netsec

import (
	"fmt"
	"strings"
)

// MessageType identifies a TLS handshake message or a synthetic state
// transition supplied by a TLS stack.
//
// The positive values match the TLS HandshakeType registry. Negative values
// are synthetic events used by stacks that report connection state rather
// than individual handshake bytes.
type MessageType int

const (
	MessageUnknown MessageType = 0

	MessageClientHello         MessageType = 1
	MessageServerHello         MessageType = 2
	MessageNewSessionTicket    MessageType = 4
	MessageEndOfEarlyData      MessageType = 5
	MessageEncryptedExtensions MessageType = 8
	MessageCertificate         MessageType = 11
	MessageCertificateRequest  MessageType = 13
	MessageCertificateVerify   MessageType = 15
	MessageFinished            MessageType = 20
	MessageKeyUpdate           MessageType = 24

	// MessageEarlyData is an early-data record, not a handshake message.
	MessageEarlyData MessageType = -1
	// MessageApplicationData is a 1-RTT application-data record.
	MessageApplicationData MessageType = -2
	// MessageEarlyDataAccepted reports that the server accepted 0-RTT data.
	MessageEarlyDataAccepted MessageType = -3
	// MessageEarlyDataRejected reports that the server rejected 0-RTT data.
	MessageEarlyDataRejected MessageType = -4
	// MessageHandshakeComplete reports that the handshake has completed.
	MessageHandshakeComplete MessageType = -5
	// MessageServerFinished is a server Finished message. It is kept
	// distinct from MessageFinished because Finished may be reported
	// without a direction by some stacks.
	MessageServerFinished MessageType = -6
)

// Common short names.
const (
	ClientHello         = MessageClientHello
	ServerHello         = MessageServerHello
	NewSessionTicket    = MessageNewSessionTicket
	EndOfEarlyData      = MessageEndOfEarlyData
	EncryptedExtensions = MessageEncryptedExtensions
	Certificate         = MessageCertificate
	CertificateRequest  = MessageCertificateRequest
	CertificateVerify   = MessageCertificateVerify
	Finished            = MessageFinished
	KeyUpdate           = MessageKeyUpdate
	EarlyData           = MessageEarlyData
	ApplicationData     = MessageApplicationData
	EarlyDataAccepted   = MessageEarlyDataAccepted
	EarlyDataRejected   = MessageEarlyDataRejected
	HandshakeComplete   = MessageHandshakeComplete
	ServerFinished      = MessageServerFinished
)

// Type-prefixed aliases make the API easy to use for callers that import
// this package alongside another TLS implementation.
const (
	TypeClientHello         = MessageClientHello
	TypeServerHello         = MessageServerHello
	TypeNewSessionTicket    = MessageNewSessionTicket
	TypeEndOfEarlyData      = MessageEndOfEarlyData
	TypeEncryptedExtensions = MessageEncryptedExtensions
	TypeCertificate         = MessageCertificate
	TypeCertificateRequest  = MessageCertificateRequest
	TypeCertificateVerify   = MessageCertificateVerify
	TypeFinished            = MessageFinished
	TypeKeyUpdate           = MessageKeyUpdate
	TypeEarlyData           = MessageEarlyData
	TypeApplicationData     = MessageApplicationData
	TypeEarlyDataAccepted   = MessageEarlyDataAccepted
	TypeEarlyDataRejected   = MessageEarlyDataRejected
	TypeHandshakeComplete   = MessageHandshakeComplete
)

var messageNames = map[MessageType]string{
	MessageUnknown:             "Unknown",
	MessageClientHello:         "ClientHello",
	MessageServerHello:         "ServerHello",
	MessageNewSessionTicket:    "NewSessionTicket",
	MessageEndOfEarlyData:      "EndOfEarlyData",
	MessageEncryptedExtensions: "EncryptedExtensions",
	MessageCertificate:         "Certificate",
	MessageCertificateRequest:  "CertificateRequest",
	MessageCertificateVerify:   "CertificateVerify",
	MessageFinished:            "Finished",
	MessageKeyUpdate:           "KeyUpdate",
	MessageEarlyData:           "EarlyData",
	MessageApplicationData:     "ApplicationData",
	MessageEarlyDataAccepted:   "EarlyDataAccepted",
	MessageEarlyDataRejected:   "EarlyDataRejected",
	MessageHandshakeComplete:   "HandshakeComplete",
	MessageServerFinished:      "ServerFinished",
}

func (t MessageType) String() string {
	if name, ok := messageNames[t]; ok {
		return name
	}
	return fmt.Sprintf("MessageType(%d)", int(t))
}

// HandshakeType and MessageKind are aliases retained for stacks that use
// different terminology for the same message identifier.
type HandshakeType = MessageType
type MessageKind = MessageType
type EventType = MessageType

// State is the server-side handshake state tracked by StateMachine.
type State int

const (
	StateInitial State = iota
	StateClientHello
	StateEarlyDataPending
	StateEarlyData
	StateEndOfEarlyData
	StateHandshake
	StateComplete
	StateFailed
)

// State aliases describe the same states with names that are common in
// TLS stack integrations.
const (
	StateStart                    = StateInitial
	StateEarlyDataAccepted        = StateEarlyData
	StateAwaitingEndOfEarlyData   = StateEarlyData
	StateWaitingForEndOfEarlyData = StateEarlyData
	StateEarlyDataEnded           = StateEndOfEarlyData
	StateHandshakeStarted         = StateHandshake
	StateHandshakeComplete        = StateComplete
	StateError                    = StateFailed
)

// Phase is an alias for State.
type Phase = State

func (s State) String() string {
	switch s {
	case StateInitial:
		return "Initial"
	case StateClientHello:
		return "ClientHello"
	case StateEarlyDataPending:
		return "EarlyDataPending"
	case StateEarlyData:
		return "EarlyData"
	case StateEndOfEarlyData:
		return "EndOfEarlyData"
	case StateHandshake:
		return "Handshake"
	case StateComplete:
		return "Complete"
	case StateFailed:
		return "Failed"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Message is a handshake/record event supplied by a TLS stack. Only Type is
// required; the flags carry state that a stack may report out of band.
type Message struct {
	Type              MessageType
	EarlyData         bool
	Accepted          bool
	EarlyDataAccepted bool
	Rejected          bool
	ApplicationData   bool
	Payload           []byte
}

// HandshakeMessage is an alias for Message.
type HandshakeMessage = Message

// Event is an alias for Message so callers can use either term.
type Event = Message

// NewMessage creates a simple message event.
func NewMessage(t MessageType) Message {
	return Message{Type: t}
}

// NewEarlyDataAccepted reports that the server accepted 0-RTT data.
func NewEarlyDataAccepted() Message {
	return Message{Type: MessageEarlyDataAccepted}
}

// NewEndOfEarlyData creates an EndOfEarlyData event.
func NewEndOfEarlyData() Message {
	return Message{Type: MessageEndOfEarlyData}
}

func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	name := b.String()
	for {
		trimmed := name
		for _, prefix := range []string{
			"tls13", "tls", "handshake", "message", "msg",
			"event", "state", "phase", "record", "type",
		} {
			if strings.HasPrefix(trimmed, prefix) && len(trimmed) > len(prefix) {
				trimmed = strings.TrimPrefix(trimmed, prefix)
			}
		}
		if trimmed == name {
			return name
		}
		name = trimmed
	}
}

func messageTypeFromName(s string) (MessageType, bool) {
	name := normalizeName(s)
	switch name {
	case "clienthello", "ch", "1":
		return MessageClientHello, true
	case "serverhello", "sh", "2":
		return MessageServerHello, true
	case "newsessionticket", "sessionticket", "nst", "4":
		return MessageNewSessionTicket, true
	case "endofearlydata", "endofearly", "earlydataend", "eoed", "eoedmessage", "5":
		return MessageEndOfEarlyData, true
	case "encryptedextensions", "ee", "8":
		return MessageEncryptedExtensions, true
	case "certificate", "cert", "11":
		return MessageCertificate, true
	case "certificaterequest", "certreq", "13":
		return MessageCertificateRequest, true
	case "certificateverify", "certverify", "cv", "15":
		return MessageCertificateVerify, true
	case "clientfinished", "finished", "fin", "20":
		return MessageFinished, true
	case "serverfinished":
		return MessageServerFinished, true
	case "keyupdate", "ku", "24":
		return MessageKeyUpdate, true
	case "earlydataaccepted", "acceptearlydata", "earlydatastarted", "earlydatastart", "acceptedearlydata":
		return MessageEarlyDataAccepted, true
	case "earlydatarejected", "rejectearlydata", "earlydatadeclined", "rejectedearlydata":
		return MessageEarlyDataRejected, true
	case "handshakecomplete", "complete", "completed", "done", "handshakedone":
		return MessageHandshakeComplete, true
	case "applicationdata", "appdata", "application", "1rttdata":
		return MessageApplicationData, true
	case "earlydata", "earlydatarrecord", "0rtt", "0rttdata", "earlydatarecord":
		return MessageEarlyData, true
	default:
		return MessageUnknown, false
	}
}
