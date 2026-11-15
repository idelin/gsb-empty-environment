package netsec

import "errors"

var (
	// ErrInvalidStation is returned when the peer address is not a 48-bit
	// IEEE 802 MAC address.
	ErrInvalidStation = errors.New("netsec: invalid station MAC address")

	// ErrUnsupportedGroup is returned for an SAE group that this package
	// cannot validate.
	ErrUnsupportedGroup = errors.New("netsec: unsupported SAE group")

	// ErrInvalidScalar is returned when the peer scalar is not in [2, r-1].
	ErrInvalidScalar = errors.New("netsec: SAE scalar is outside [2, r-1]")

	// ErrInvalidElement is returned when the peer element is not a valid
	// point on the selected group.
	ErrInvalidElement = errors.New("netsec: invalid SAE element")

	// ErrPointAtInfinity is returned when the peer element is the point at
	// infinity.
	ErrPointAtInfinity = errors.New("netsec: SAE element is the point at infinity")

	// ErrReflectedCommit is returned when the peer element is the AP's own
	// element. Accepting it would let an attacker reflect the AP's commit.
	ErrReflectedCommit = errors.New("netsec: SAE commit reflects the local element")

	// ErrInvalidLocalElement is returned when the AP-supplied local element
	// cannot be decoded on the selected group.
	ErrInvalidLocalElement = errors.New("netsec: invalid local SAE element")

	// ErrInvalidToken is returned when an anti-clogging token does not
	// authenticate under a current or recently rotated key.
	ErrInvalidToken = errors.New("netsec: invalid anti-clogging token")

	// ErrTokenReplay is returned when an otherwise valid anti-clogging token
	// has already admitted a transaction.
	ErrTokenReplay = errors.New("netsec: anti-clogging token replay")

	// ErrTooManyTransactions is returned when the configured transaction
	// table bound has been reached.
	ErrTooManyTransactions = errors.New("netsec: too many SAE transactions")
)
