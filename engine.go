package netsec

import (
	"encoding/binary"
	"errors"
)

// Action is the transport-level action the server must take after feeding a
// client message to the engine.
type Action int

const (
	// ActionAllow means the message passed every policy check and the server
	// continues the normal SMB2 state machine.
	ActionAllow Action = iota

	// ActionReject means the server should send a protocol-level error
	// response and then close the transport. This is used for negotiable
	// mismatches (for example no common encryption cipher).
	ActionReject

	// ActionDisconnect means the server must close the transport immediately
	// without sending any further message on this connection.
	ActionDisconnect
)

// Reason identifies why the engine returned a terminal decision.
type Reason int

const (
	ReasonOK Reason = iota
	ReasonMalformedMessage
	ReasonNoCommonDialect
	ReasonNoPreauthContext
	ReasonNoSHA512Preauth
	ReasonNoEncryptionCipher
	ReasonCipherNoIntersection
	ReasonEncryptedDialectUnsupported
	ReasonDuplicateNegotiate
	ReasonUnexpectedCommand
	ReasonSignatureFailed
	ReasonVerifierMissing
	ReasonUnencryptedAfterTreeConnect
	ReasonEncryptionNotNegotiated
	ReasonUnknownProtocol
)

// Error maps terminal reasons to exported sentinel errors so callers can use
// errors.Is when they prefer errors over reason codes.
var (
	ErrMalformedMessage            = errors.New("netsec: malformed SMB2 message")
	ErrNoCommonDialect             = errors.New("netsec: no common dialect")
	ErrNoPreauthContext            = errors.New("netsec: missing preauth integrity negotiate context")
	ErrNoSHA512Preauth             = errors.New("netsec: client does not support SHA-512 preauth integrity")
	ErrNoEncryptionCipher          = errors.New("netsec: client advertised no usable encryption cipher")
	ErrCipherNoIntersection        = errors.New("netsec: no common encryption cipher with server preference")
	ErrEncryptedDialectUnsupported = errors.New("netsec: encryption required but dialect is below SMB 3.0")
	ErrDuplicateNegotiate          = errors.New("netsec: NEGOTIATE received after negotiation completed")
	ErrUnexpectedCommand           = errors.New("netsec: command received in an invalid connection state")
	ErrSignatureFailed             = errors.New("netsec: session setup signature failed preauth integrity verification")
	ErrVerifierMissing             = errors.New("netsec: no SessionSetupVerifier configured")
	ErrUnencryptedAfterTreeConnect = errors.New("netsec: unencrypted request after TREE_CONNECT on an encrypted share")
	ErrEncryptionNotNegotiated     = errors.New("netsec: encrypted message without a negotiated encryption cipher")
	ErrUnknownProtocol             = errors.New("netsec: message does not begin with an SMB2 protocol id")
)

func (r Reason) error() error {
	switch r {
	case ReasonMalformedMessage:
		return ErrMalformedMessage
	case ReasonNoCommonDialect:
		return ErrNoCommonDialect
	case ReasonNoPreauthContext:
		return ErrNoPreauthContext
	case ReasonNoSHA512Preauth:
		return ErrNoSHA512Preauth
	case ReasonNoEncryptionCipher:
		return ErrNoEncryptionCipher
	case ReasonCipherNoIntersection:
		return ErrCipherNoIntersection
	case ReasonEncryptedDialectUnsupported:
		return ErrEncryptedDialectUnsupported
	case ReasonDuplicateNegotiate:
		return ErrDuplicateNegotiate
	case ReasonUnexpectedCommand:
		return ErrUnexpectedCommand
	case ReasonSignatureFailed:
		return ErrSignatureFailed
	case ReasonVerifierMissing:
		return ErrVerifierMissing
	case ReasonUnencryptedAfterTreeConnect:
		return ErrUnencryptedAfterTreeConnect
	case ReasonEncryptionNotNegotiated:
		return ErrEncryptionNotNegotiated
	case ReasonUnknownProtocol:
		return ErrUnknownProtocol
	default:
		return nil
	}
}

// Decision is the outcome of processing one inbound client message.
type Decision struct {
	// Action is the transport action the server must perform.
	Action Action

	// Reason is set for every non-allow decision.
	Reason Reason

	// Err is the sentinel error associated with Reason, or nil on allow.
	Err error

	// Dialect is the dialect selected during negotiation. It is zero before a
	// successful NEGOTIATE has been processed.
	Dialect uint16

	// Cipher is the encryption cipher selected during negotiation, or zero if
	// the negotiated dialect does not encrypt.
	Cipher uint16

	// EncryptionRequired reports whether this connection is bound to an
	// encrypted share or the server enforces encryption globally.
	EncryptionRequired bool

	// PreauthHash is a snapshot of the running pre-authentication integrity
	// hash at the time of the decision.
	PreauthHash []byte
}

// SessionSetupVerifier is injected by the caller. It receives the raw
// SESSION_SETUP request and the current pre-authentication integrity hash
// (already covering the negotiate request and response). A false result means
// the signature does not validate and the connection must be dropped.
type SessionSetupVerifier func(sessionSetupMessage, preauthHash []byte) bool

// ShareEncryptionPolicy reports whether the share addressed by a
// TREE_CONNECT path requires encryption. The path is the decoded UTF-16
// share path from the request (for example "\\server\share").
type ShareEncryptionPolicy func(sharePath string) bool

// Config configures an Engine. Zero values are replaced with safe defaults by
// NewEngine.
type Config struct {
	// RequireEncryption enforces encryption server-wide for every share.
	RequireEncryption bool

	// Dialects is the server's supported dialect list, highest preference
	// first. Defaults to SMB 3.1.1 down to SMB 2.0.2.
	Dialects []uint16

	// Ciphers is the server's encryption-cipher preference list, highest
	// preference first. Defaults to AES-128-GCM, AES-128-CCM, AES-256-GCM.
	Ciphers []uint16

	// VerifySessionSetup validates the SESSION_SETUP signature against the
	// accumulated preauth hash. When nil, SESSION_SETUP cannot be accepted.
	VerifySessionSetup SessionSetupVerifier

	// ShareRequiresEncryption reports the per-share encryption requirement.
	// When nil it reports RequireEncryption for every share.
	ShareRequiresEncryption ShareEncryptionPolicy
}

// connState is the internal SMB2 connection state.
type connState int

const (
	stateInitial connState = iota
	stateNegotiating
	stateNegotiated
	stateSession
	stateTreeConnected
	stateClosed
)

// Engine holds the negotiation policy for one SMB2 connection. An Engine is
// intended to be used by a single goroutine at a time, matching one network
// connection; callers needing concurrency should provide their own mutex.
type Engine struct {
	requireEncryption  bool
	dialects           []uint16
	ciphers            []uint16
	verifySessionSetup SessionSetupVerifier
	shareEncryption    ShareEncryptionPolicy

	state              connState
	negotiatedDialect  uint16
	negotiatedCipher   uint16
	encryptionRequired bool
	preauth            *preauthHasher
}

// NewEngine builds an Engine for one connection from cfg.
func NewEngine(cfg Config) *Engine {
	dialects := cfg.Dialects
	if len(dialects) == 0 {
		dialects = []uint16{DialectSMB311, DialectSMB302, DialectSMB300, DialectSMB210, DialectSMB202}
	}

	ciphers := cfg.Ciphers
	if len(ciphers) == 0 {
		ciphers = []uint16{CipherAES128GCM, CipherAES128CCM, CipherAES256GCM}
	}

	shareEncryption := cfg.ShareRequiresEncryption
	if shareEncryption == nil {
		shareEncryption = func(string) bool { return cfg.RequireEncryption }
	}

	return &Engine{
		requireEncryption:  cfg.RequireEncryption,
		dialects:           append([]uint16(nil), dialects...),
		ciphers:            append([]uint16(nil), ciphers...),
		verifySessionSetup: cfg.VerifySessionSetup,
		shareEncryption:    shareEncryption,
		state:              stateInitial,
		preauth:            newPreauthHasher(),
	}
}

// Dialect returns the dialect selected during negotiation, or zero before
// negotiation completes.
func (e *Engine) Dialect() uint16 { return e.negotiatedDialect }

// Cipher returns the selected encryption cipher, or zero when none applies.
func (e *Engine) Cipher() uint16 { return e.negotiatedCipher }

// EncryptionRequired reports whether the connection is bound to an encrypted
// share or the server enforces encryption globally.
func (e *Engine) EncryptionRequired() bool { return e.encryptionRequired }

// PreauthHash returns a snapshot of the current pre-authentication hash.
func (e *Engine) PreauthHash() []byte { return e.preauth.value() }

// Closed reports whether the engine has entered the terminal closed state.
func (e *Engine) Closed() bool { return e.state == stateClosed }

func (e *Engine) decide(action Action, reason Reason) Decision {
	d := Decision{
		Action:             action,
		Reason:             reason,
		Dialect:            e.negotiatedDialect,
		Cipher:             e.negotiatedCipher,
		EncryptionRequired: e.encryptionRequired,
		PreauthHash:        e.preauth.value(),
	}
	d.Err = reason.error()
	return d
}

func (e *Engine) allow() Decision { return e.decide(ActionAllow, ReasonOK) }

// close moves the engine to the terminal state and returns a disconnect or
// reject decision. Once closed every subsequent message is disconnected.
func (e *Engine) close(action Action, reason Reason) Decision {
	e.state = stateClosed
	return e.decide(action, reason)
}

// HandleRequest feeds one complete inbound client message to the engine. The
// message is the full transport PDU: either an SMB2 request beginning with
// 0xFE "SMB" or an encrypted transform message beginning with 0xFD "SMB".
func (e *Engine) HandleRequest(message []byte) Decision {
	if len(message) < 4 {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}

	protocolID := binary.LittleEndian.Uint32(message[:4])
	if protocolID == protocolTransform {
		return e.handleTransform(message)
	}
	if protocolID != protocolSMB2 {
		return e.close(ActionDisconnect, ReasonUnknownProtocol)
	}

	if e.state == stateClosed {
		return e.decide(ActionDisconnect, ReasonUnexpectedCommand)
	}

	if len(message) < smb2HeaderLen {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}
	command := binary.LittleEndian.Uint16(message[12:14])

	// A second NEGOTIATE at any point after negotiation completed is an
	// immediate disconnect even if it would otherwise parse correctly.
	if command == cmdNegotiate && e.state != stateInitial {
		return e.close(ActionDisconnect, ReasonDuplicateNegotiate)
	}

	switch command {
	case cmdNegotiate:
		if e.state != stateInitial {
			return e.close(ActionDisconnect, ReasonDuplicateNegotiate)
		}
		return e.handleNegotiate(message)
	case cmdSessionSetup:
		if e.state != stateNegotiated && e.state != stateSession {
			return e.close(ActionDisconnect, ReasonUnexpectedCommand)
		}
		return e.handleSessionSetup(message)
	case cmdTreeConnect:
		if e.state != stateSession {
			return e.close(ActionDisconnect, ReasonUnexpectedCommand)
		}
		return e.handleTreeConnect(message)
	default:
		return e.handlePostNegotiateCommand(message)
	}
}

// handlePostNegotiateCommand enforces state ordering and the encrypted-share
// plaintext ban for every command that is not NEGOTIATE/SESSION_SETUP/
// TREE_CONNECT.
func (e *Engine) handlePostNegotiateCommand(message []byte) Decision {
	switch e.state {
	case stateInitial, stateNegotiating:
		return e.close(ActionDisconnect, ReasonUnexpectedCommand)
	case stateNegotiated:
		// Only SESSION_SETUP is legal between NEGOTIATE and session setup.
		return e.close(ActionDisconnect, ReasonUnexpectedCommand)
	case stateSession:
		// Post-session, pre-tree commands (LOGOFF, etc.) are permitted;
		// nothing is encrypted before an encrypted TREE_CONNECT.
		return e.allow()
	case stateTreeConnected:
		if e.encryptionRequired {
			return e.close(ActionDisconnect, ReasonUnencryptedAfterTreeConnect)
		}
		return e.allow()
	default:
		return e.decide(ActionDisconnect, ReasonUnexpectedCommand)
	}
}

// handleTransform rejects encrypted traffic that arrives without a negotiated
// cipher and otherwise allows it. On an encrypted share all further traffic
// must arrive through this path; plaintext traffic is rejected by
// handlePostNegotiateCommand.
func (e *Engine) handleTransform(message []byte) Decision {
	if e.state == stateClosed {
		return e.decide(ActionDisconnect, ReasonUnexpectedCommand)
	}
	if len(message) < transformHeaderLen {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}
	if e.negotiatedCipher == 0 {
		return e.close(ActionDisconnect, ReasonEncryptionNotNegotiated)
	}
	return e.allow()
}
