package streamguard

import (
	"errors"
	"fmt"
)

// ErrNeedMore is returned by incremental readers when a complete token or
// frame is not yet available.
var ErrNeedMore = errors.New("streamguard: need more bytes")

// Security and protocol errors are sentinels so callers can classify attacks
// with errors.Is instead of matching human-readable strings.
var (
	ErrSecurity = errors.New("streamguard: security policy violation")

	ErrInvalidUTF8    = fmt.Errorf("%w: invalid UTF-8", ErrSecurity)
	ErrInvalidXMLChar = fmt.Errorf("%w: invalid XML character", ErrSecurity)
	ErrMalformedXML   = fmt.Errorf("%w: malformed XML", ErrSecurity)

	ErrDTDForbidden        = fmt.Errorf("%w: DTD declaration is forbidden", ErrSecurity)
	ErrEntityDeclForbidden = fmt.Errorf("%w: entity declaration is forbidden", ErrSecurity)
	ErrUndeclaredEntity    = fmt.Errorf("%w: reference to undeclared custom entity", ErrSecurity)
	ErrMalformedEntity     = fmt.Errorf("%w: malformed entity reference", ErrMalformedXML)

	ErrCommentBomb = fmt.Errorf("%w: comment limit exceeded", ErrSecurity)

	ErrLimit               = errors.New("streamguard: parser limit exceeded")
	ErrBufferLimit         = fmt.Errorf("%w: pending buffer too large", ErrLimit)
	ErrDepthExceeded       = fmt.Errorf("%w: element depth limit exceeded", ErrLimit)
	ErrAttributeLimit      = fmt.Errorf("%w: attribute count limit exceeded", ErrLimit)
	ErrNameLimit           = fmt.Errorf("%w: element or attribute name is too long", ErrLimit)
	ErrAttributeValueLimit = fmt.Errorf("%w: attribute value is too long", ErrLimit)
	ErrTextLimit           = fmt.Errorf("%w: text node is too long", ErrLimit)
	ErrProcessingInstLimit = fmt.Errorf("%w: processing instruction is too long", ErrLimit)

	ErrInvalidState = errors.New("streamguard: invalid protocol state transition")
	ErrClosed       = errors.New("streamguard: connection guard is closed")

	ErrTLSRequired      = fmt.Errorf("%w: StartTLS must complete before this operation", ErrInvalidState)
	ErrStartTLSStripped = fmt.Errorf(
		"%w: StartTLS appears stripped or was not performed",
		ErrTLSRequired,
	)
	ErrSASLBeforeTLS = fmt.Errorf(
		"%w: SASL credentials must not be sent in plaintext",
		ErrStartTLSStripped,
	)
	ErrStanzaBeforeTLS = fmt.Errorf(
		"%w: business stanza is forbidden in plaintext",
		ErrStartTLSStripped,
	)
	ErrBindRequired          = fmt.Errorf("%w: resource binding is required", ErrInvalidState)
	ErrSASLRequired          = fmt.Errorf("%w: SASL authentication is required", ErrInvalidState)
	ErrSASLNotComplete       = fmt.Errorf("%w: SASL handshake has not completed", ErrInvalidState)
	ErrStreamRestartRequired = fmt.Errorf(
		"%w: fresh stream open after StartTLS reset is required",
		ErrInvalidState,
	)
	ErrTLSUpgradePending = fmt.Errorf(
		"%w: call UpgradeTLS/ResetBufferAndStream before reading more frames",
		ErrInvalidState,
	)
)

// Compatibility aliases for callers and tests that use the security wording
// directly.
var (
	ErrEntityForbidden      = ErrEntityDeclForbidden
	ErrTooManyAttributes    = ErrAttributeLimit
	ErrDepthLimitExceeded   = ErrDepthExceeded
	ErrPlaintextSASL        = ErrSASLBeforeTLS
	ErrPlaintextStanza      = ErrStanzaBeforeTLS
	ErrBufferSmuggleBlocked = errors.New("streamguard: pre-TLS buffered bytes were discarded")
)

// SecurityError optionally carries a machine-readable code and numeric context.
type SecurityError struct {
	Code    string
	State   State
	Limit   int
	Actual  int
	Wrapped error
}

func (e *SecurityError) Error() string {
	msg := "streamguard: security error"
	if e.Code != "" {
		msg = "streamguard: " + e.Code
	}
	if e.Wrapped != nil {
		msg += ": " + e.Wrapped.Error()
	}
	return msg
}

func (e *SecurityError) Unwrap() error {
	return e.Wrapped
}

func limitError(sentinel error, limit, actual int) error {
	return &SecurityError{
		Code:    "limit_exceeded",
		Limit:   limit,
		Actual:  actual,
		Wrapped: sentinel,
	}
}
