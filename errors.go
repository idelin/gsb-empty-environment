package netsec

import "errors"

// Sentinel errors returned by dictionary operations. Callers use errors.Is to
// match them; they never carry tenant data themselves.
var (
	// ErrEmptyWord is returned when an entry would be created with no bytes.
	ErrEmptyWord = errors.New("netsec: dictionary word must not be empty")

	// ErrEmptyTenant is returned when an origin or authorization set is empty.
	ErrEmptyTenant = errors.New("netsec: tenant must not be empty")

	// ErrZeroDigest is returned when an origin does not pin an input digest.
	ErrZeroDigest = errors.New("netsec: origin must carry a non-zero input digest")

	// ErrRevoked is returned when an entry or derivation depends on a source
	// that has been revoked, or when new data is imported from such a source.
	ErrRevoked = errors.New("netsec: source has been revoked")

	// ErrUnknownEntry is returned when an entry id does not exist.
	ErrUnknownEntry = errors.New("netsec: unknown entry id")

	// ErrNoParents is returned when a derivation has no parents.
	ErrNoParents = errors.New("netsec: derivation requires at least one parent")

	// ErrNilSnapshot is returned when Merge receives a nil snapshot.
	ErrNilSnapshot = errors.New("netsec: cannot merge nil snapshot")

	// ErrNilDictionary is returned when Merge receives a nil destination.
	ErrNilDictionary = errors.New("netsec: destination dictionary is nil")
)
