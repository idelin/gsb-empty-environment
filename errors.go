package netsec

import (
	"errors"
	"fmt"
)

var (
	ErrMalformedToken           = errors.New("netsec: malformed timestamp token")
	ErrUnsupportedContentType   = errors.New("netsec: timestamp token is not RFC 3161 TSTInfo")
	ErrMultipleTokens           = errors.New("netsec: exactly one timestamp token is required")
	ErrNoTimestampToken         = errors.New("netsec: no timestamp token supplied")
	ErrImprintMismatch          = errors.New("netsec: timestamp message imprint does not match signature")
	ErrDigestAlgorithmMismatch  = errors.New("netsec: timestamp digest algorithm does not match")
	ErrUnsupportedDigest        = errors.New("netsec: unsupported timestamp digest algorithm")
	ErrTokenSignature           = errors.New("netsec: timestamp token signature is invalid")
	ErrIssuerNotTrusted         = errors.New("netsec: timestamp issuer is not in the trust list")
	ErrCertificateChain         = errors.New("netsec: timestamp certificate chain is invalid")
	ErrNoTrustAnchor            = errors.New("netsec: no timestamp trust anchor configured")
	ErrSignerCertificateExpired = errors.New("netsec: timestamp is outside signer certificate validity")
	ErrTokenExpired             = errors.New("netsec: timestamp token has expired")
	ErrTokenRevoked             = errors.New("netsec: timestamp token or its signer has been revoked")
	ErrDuplicateSerial          = errors.New("netsec: duplicate timestamp serial number")
	ErrTimestampInFuture        = errors.New("netsec: timestamp generation time is in the future")
	ErrTokenReuse               = errors.New("netsec: timestamp token was reused for another signature")
	ErrNoSignerCertificate      = errors.New("netsec: signer certificate is required")
	ErrNoSignature              = errors.New("netsec: signature or digest is required")
	ErrPolicyNotAllowed         = errors.New("netsec: timestamp policy is not allowed")
	ErrRevocationCheck          = errors.New("netsec: timestamp revocation check failed")
)

type verifyError struct {
	kind   error
	detail string
	err    error
}

func (e *verifyError) Error() string {
	switch {
	case e.detail != "":
		return fmt.Sprintf("%v: %s", e.kind, e.detail)
	case e.err != nil:
		return fmt.Sprintf("%v: %v", e.kind, e.err)
	default:
		return e.kind.Error()
	}
}

func (e *verifyError) Unwrap() []error {
	if e.err == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.err}
}

func errorf(kind error, format string, args ...any) error {
	return &verifyError{kind: kind, detail: fmt.Sprintf(format, args...)}
}

func wrapError(kind, err error) error {
	if err == nil {
		return kind
	}
	return &verifyError{kind: kind, err: err}
}
