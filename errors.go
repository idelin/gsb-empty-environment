package netsec

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by validation and digest-verified pulls.
var (
	// ErrInsecureHost means a host used http:// or skip_verify on a
	// non-loopback endpoint (or on loopback while the policy forbids it).
	ErrInsecureHost = errors.New("insecure host: http or skip_verify is forbidden outside loopback")

	// ErrCredentialLeak means an Authorization header is configured on a
	// host that is not same-domain with the upstream server.
	ErrCredentialLeak = errors.New("authorization header may only be sent to a host in the same domain as the server")

	// ErrDigestMismatch means fetched content did not hash to the requested
	// digest. Already downloaded bytes must be discarded before another host
	// is tried.
	ErrDigestMismatch = errors.New("content digest mismatch")

	// ErrDigestNotAllowed means the digest algorithm is not enabled by the
	// caller policy.
	ErrDigestNotAllowed = errors.New("digest algorithm not allowed by policy")

	// ErrNoPullHost means no host entry provides the pull capability.
	ErrNoPullHost = errors.New("no host provides the pull capability")
)

// ConfigError associates a validation violation with the offending entry so
// callers can report precisely which host table was rejected.
type ConfigError struct {
	// Endpoint is the offending host URL, or the server URL when Server
	// itself is invalid.
	Endpoint string
	// Op names the failed check, e.g. "validate.insecure".
	Op string
	// Err is the wrapped sentinel error.
	Err error
}

func (e *ConfigError) Error() string {
	if e.Endpoint == "" {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("%s %q: %v", e.Op, e.Endpoint, e.Err)
}

func (e *ConfigError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrInsecureHost) and friends match ConfigErrors.
func (e *ConfigError) Is(target error) bool { return e.Err == target }

func configError(endpoint, op string, err error) error {
	return &ConfigError{Endpoint: endpoint, Op: op, Err: err}
}
