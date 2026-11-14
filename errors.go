package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidURL is returned when a target is not an absolute HTTP(S) URL.
	ErrInvalidURL = errors.New("netsec: invalid target URL")
	// ErrUserInfo is returned when a URL carries a userinfo component.
	ErrUserInfo = errors.New("netsec: URL userinfo is not allowed")
	// ErrFragment is returned when a URL carries a fragment.
	ErrFragment = errors.New("netsec: URL fragments are not allowed")
	// ErrScheme is returned for schemes other than http and https.
	ErrScheme = errors.New("netsec: scheme must be http or https")
	// ErrHost is returned for invalid or ambiguous host names.
	ErrHost = errors.New("netsec: invalid host")
	// ErrPort is returned for a non-standard or invalid port.
	ErrPort = errors.New("netsec: non-standard port")
	// ErrNoAddress is returned when DNS produced no usable address.
	ErrNoAddress = errors.New("netsec: host resolved to no usable address")
	// ErrPrivateAddress is returned for private address space.
	ErrPrivateAddress = errors.New("netsec: private address is not allowed")
	// ErrLoopbackAddress is returned for loopback address space.
	ErrLoopbackAddress = errors.New("netsec: loopback address is not allowed")
	// ErrLinkLocalAddress is returned for link-local address space.
	ErrLinkLocalAddress = errors.New("netsec: link-local address is not allowed")
	// ErrBlockedAddress is returned for unspecified, multicast, and reserved space.
	ErrBlockedAddress = errors.New("netsec: address is not a public unicast address")
	// ErrDNS is returned when name resolution fails.
	ErrDNS = errors.New("netsec: DNS lookup failed")
	// ErrDial is returned when all validated addresses fail to connect.
	ErrDial = errors.New("netsec: connection failed")
	// ErrTooManyRedirects is returned when the redirect limit is exceeded.
	ErrTooManyRedirects = errors.New("netsec: too many redirects")
	// ErrUnpinnedDial is returned when the transport is asked to dial without
	// a validated, pinned target.
	ErrUnpinnedDial = errors.New("netsec: refusing unpinned outbound dial")
	// ErrTargetMismatch is returned when the dial address does not match the
	// address that was validated.
	ErrTargetMismatch = errors.New("netsec: connection target does not match validated target")
)

// ErrUserinfo is an alias retained for callers that spell userinfo as one word.
var ErrUserinfo = ErrUserInfo

// ValidationError describes why a URL was rejected. It unwraps to one of the
// sentinel errors above.
type ValidationError struct {
	URL     string
	Host    string
	Address string
	Err     error
}

func (e *ValidationError) Error() string {
	switch {
	case e.Address != "" && e.Host != "":
		return fmt.Sprintf("%v: host %q address %s (%s)", e.Err, e.Host, e.Address, e.URL)
	case e.Host != "":
		return fmt.Sprintf("%v: host %q (%s)", e.Err, e.Host, e.URL)
	default:
		return fmt.Sprintf("%v (%s)", e.Err, e.URL)
	}
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

func validationError(err error, rawURL, host, address string) error {
	return &ValidationError{URL: rawURL, Host: host, Address: address, Err: err}
}
