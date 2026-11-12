package netsec

import "errors"

var (
	// ErrUnsupportedGroup indicates that the SAE group is not implemented.
	ErrUnsupportedGroup = errors.New("netsec: unsupported SAE group")
	// ErrInvalidStation indicates a malformed station MAC address.
	ErrInvalidStation = errors.New("netsec: invalid station MAC address")
	// ErrScalarEncoding indicates that a scalar does not have the group's
	// fixed field width.
	ErrScalarEncoding = errors.New("netsec: invalid commit scalar encoding")
	// ErrScalarRange indicates a scalar outside [2, r-1].
	ErrScalarRange = errors.New("netsec: commit scalar outside [2, r-1]")
	// ErrElementEncoding indicates a malformed curve element encoding.
	ErrElementEncoding = errors.New("netsec: invalid commit element encoding")
	// ErrElementInfinity indicates the point at infinity.
	ErrElementInfinity = errors.New("netsec: commit element is the point at infinity")
	// ErrElementNotOnCurve indicates a point that is not on the selected curve.
	ErrElementNotOnCurve = errors.New("netsec: commit element is not on the curve")
	// ErrReflection indicates that the station reflected the AP's own element.
	ErrReflection = errors.New("netsec: reflected AP commit element")
	// ErrTokenInvalid indicates a missing, stale, or incorrectly bound token.
	ErrTokenInvalid = errors.New("netsec: invalid anti-clogging token")
	// ErrTokenReplay indicates a token that has already been used.
	ErrTokenReplay = errors.New("netsec: replayed anti-clogging token")
	// ErrTransactionThreshold indicates that the soft incomplete-transaction
	// threshold was reached, so a token is required.
	ErrTransactionThreshold = errors.New("netsec: incomplete transaction threshold reached")
	// ErrTransactionCapacity indicates that the hard transaction capacity was
	// reached.
	ErrTransactionCapacity = errors.New("netsec: transaction capacity reached")
	// ErrStationTransactionLimit indicates that one station already has the
	// maximum number of incomplete transactions.
	ErrStationTransactionLimit = errors.New("netsec: station transaction limit reached")
	// ErrTransactionInProgress is returned when the same station/group tuple
	// already has a different commit pending admission.
	ErrTransactionInProgress = errors.New("netsec: transaction already in progress")
	// ErrOwnElement indicates an invalid configured AP element.
	ErrOwnElement = errors.New("netsec: invalid AP own element")
	// ErrTokenUnavailable indicates that a token could not be generated.
	ErrTokenUnavailable = errors.New("netsec: anti-clogging token unavailable")
)
