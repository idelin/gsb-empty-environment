package netsec

import "errors"

var (
	// ErrWindowRounds is returned for a non-positive window length.
	ErrWindowRounds = errors.New("netsec: complaint window rounds must be positive")
	// ErrWindowClosed is returned when a complaint or disclosure arrives
	// after the complaint window has closed.
	ErrWindowClosed = errors.New("netsec: complaint window is closed")
	// ErrWindowStillOpen is returned when Finalize is called while the
	// window is still open.
	ErrWindowStillOpen = errors.New("netsec: complaint window is still open")
	// ErrFinalized is returned for any mutation attempted after the
	// result was finalized.
	ErrFinalized = errors.New("netsec: run already finalized")
	// ErrEmptyID is returned for an empty dealer or receiver identifier.
	ErrEmptyID = errors.New("netsec: empty participant identifier")
	// ErrDuplicateDealer is returned when a dealer ID is registered
	// twice.
	ErrDuplicateDealer = errors.New("netsec: duplicate dealer id")
	// ErrDuplicateReceiver is returned when a receiver ID is registered
	// twice.
	ErrDuplicateReceiver = errors.New("netsec: duplicate receiver id")
	// ErrDuplicateIndex is returned when two receivers claim the same x.
	ErrDuplicateIndex = errors.New("netsec: duplicate receiver index x")
	// ErrUnknownDealer is returned when a dealer ID is not registered.
	ErrUnknownDealer = errors.New("netsec: unknown dealer id")
	// ErrUnknownReceiver is returned when a receiver ID is not
	// registered.
	ErrUnknownReceiver = errors.New("netsec: unknown receiver id")
	// ErrIdentityConstant is returned when a commitment's constant term
	// is the identity element.
	ErrIdentityConstant = errors.New("netsec: constant-term commitment is the identity element")
	// ErrDoubleBroadcast is returned when two receivers observed
	// different commitment hashes for one dealer.
	ErrDoubleBroadcast = errors.New("netsec: dealer broadcast inconsistent commitment hashes")
	// ErrCommitmentHashMismatch is returned when a complaint or
	// disclosure quotes a hash that was never observed for the dealer.
	ErrCommitmentHashMismatch = errors.New("netsec: quoted commitment hash does not match receipt")
	// ErrDuplicateShare is returned when a receiver registers a second
	// share from the same dealer.
	ErrDuplicateShare = errors.New("netsec: duplicate share registration")
	// ErrDuplicateComplaint is returned when a receiver complains twice
	// against the same dealer.
	ErrDuplicateComplaint = errors.New("netsec: duplicate complaint")
	// ErrDuplicateDisclosure is returned when a dealer discloses the
	// same complaint twice.
	ErrDuplicateDisclosure = errors.New("netsec: duplicate disclosure")
	// ErrNoComplaint is returned when a disclosure has no matching
	// filed complaint.
	ErrNoComplaint = errors.New("netsec: no matching complaint on file")
	// ErrMissingShare is returned at finalization when a qualified
	// dealer's share is missing for a registered receiver.
	ErrMissingShare = errors.New("netsec: missing share for aggregation")
	// ErrNoQualifiedDealers is returned when every dealer was
	// disqualified and no aggregate can be built.
	ErrNoQualifiedDealers = errors.New("netsec: no qualified dealers remain")
)
