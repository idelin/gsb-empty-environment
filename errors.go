package netsec

import "errors"

// Sentinel errors returned by the validation and pull helpers. Callers
// can use errors.Is to match the underlying reason behind a joined or
// wrapped error.
var (
	// ErrInvalidEndpoint is returned when an endpoint is not an absolute
	// http(s) URL with a non-empty host.
	ErrInvalidEndpoint = errors.New("netsec: endpoint must be an absolute http(s) URL with a host")
	// ErrUserInfoNotAllowed is returned when an endpoint embeds
	// userinfo, which would smuggle credentials into the URL.
	ErrUserInfoNotAllowed = errors.New("netsec: userinfo in endpoint URL is not allowed")
	// ErrPlaintextNotAllowed is returned for non-loopback http endpoints.
	ErrPlaintextNotAllowed = errors.New("netsec: plaintext http endpoint is not allowed outside loopback")
	// ErrSkipVerifyNotAllowed is returned for skip_verify=true on a
	// non-loopback endpoint.
	ErrSkipVerifyNotAllowed = errors.New("netsec: skip_verify is not allowed outside loopback")
	// ErrCredentialLeak is returned when an Authorization header is
	// configured on a host outside the server's domain.
	ErrCredentialLeak = errors.New("netsec: Authorization header is only allowed on a host in the same domain as the server")
	// ErrClientCredential is returned when a client entry sets only one
	// of cert/key.
	ErrClientCredential = errors.New("netsec: client cert and key must both be set")
	// ErrUnknownCapability is returned for a capability value outside
	// resolve/pull/push.
	ErrUnknownCapability = errors.New("netsec: unknown host capability")
	// ErrNoPullHost is returned when no configured host may serve pulls.
	ErrNoPullHost = errors.New("netsec: no configured host provides the pull capability")
	// ErrDigestMismatch is returned when fetched content does not hash to
	// the requested digest.
	ErrDigestMismatch = errors.New("netsec: fetched content digest does not match the requested digest")
)

// HostError associates a validation error with the offending host
// endpoint.
type HostError struct {
	Host string
	Err  error
}

func (e *HostError) Error() string {
	return "host " + quote(e.Host) + ": " + e.Err.Error()
}

func (e *HostError) Unwrap() error {
	return e.Err
}

func quote(s string) string {
	return "\"" + s + "\""
}
