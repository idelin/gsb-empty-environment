package netsec

import "errors"

// Resolution failures. Callers can use errors.Is to distinguish them.
var (
	// ErrInvalidName means the name is empty or malformed (e.g. empty label).
	ErrInvalidName = errors.New("netsec: invalid name")
	// ErrEgoNotFound means the right-most label has no live local ego. The
	// root label is never interpreted through remote zone records.
	ErrEgoNotFound = errors.New("netsec: ego not found for root label")
	// ErrZoneNotFound means a delegated zone key cannot be fetched from the
	// zone store.
	ErrZoneNotFound = errors.New("netsec: zone not found")
	// ErrLabelNotFound means the current zone publishes no live record for
	// the next label.
	ErrLabelNotFound = errors.New("netsec: label not found in zone")
	// ErrDelegationTooDeep means more than MaxDelegationDepth PKEY hops were
	// followed.
	ErrDelegationTooDeep = errors.New("netsec: delegation depth exceeded")
	// ErrDelegationLoop means a PKEY pointed at a zone key already present in
	// the current delegation chain.
	ErrDelegationLoop = errors.New("netsec: delegation loop detected")
	// ErrConflictingRecords means a label block contains both a live PKEY and
	// a live CNAME, which makes the whole block invalid.
	ErrConflictingRecords = errors.New("netsec: PKEY and CNAME conflict in block")
	// ErrTooManyRedirects means CNAME aliases restarted resolution more than
	// MaxCNAMERedirects times without terminating.
	ErrTooManyRedirects = errors.New("netsec: too many CNAME redirects")
	// ErrInvalidRecord means a steering record (PKEY/CNAME) carried no usable
	// value.
	ErrInvalidRecord = errors.New("netsec: invalid record value")
)
