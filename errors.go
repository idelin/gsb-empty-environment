package netsec

import "errors"

var (
	// ErrHeight means that the requested tree height is outside [1, 64].
	ErrHeight = errors.New("netsec: tree height must be between 1 and 64")

	// ErrDepth means that a proof node or path depth is outside its tree.
	ErrDepth = errors.New("netsec: depth is outside the tree")

	// ErrQueryCount means that a proof has zero or more than 256 queries.
	ErrQueryCount = errors.New("netsec: query count must be between 1 and 256")

	// ErrQueryOrder means that query keys are not strictly increasing.
	ErrQueryOrder = errors.New("netsec: query keys must be strictly increasing")

	// ErrQueryKey means that a query key does not fit in the tree height.
	ErrQueryKey = errors.New("netsec: query key is outside the tree")

	// ErrQueryPresence means that an absent query carries a value.
	ErrQueryPresence = errors.New("netsec: absent query must not carry a value")

	// ErrDuplicateCoordinate means that two side branches share depth/prefix.
	ErrDuplicateCoordinate = errors.New("netsec: duplicate side-branch coordinate")

	// ErrNonZeroPrefixBits means that a side-branch prefix has bits above depth.
	ErrNonZeroPrefixBits = errors.New("netsec: side-branch prefix has non-zero extra bits")

	// ErrExplicitEmptySide means that an empty subtree was explicitly supplied.
	ErrExplicitEmptySide = errors.New("netsec: explicitly supplied empty side branch")

	// ErrSideCoversQuery means that a side branch contains a query key.
	ErrSideCoversQuery = errors.New("netsec: side branch covers a query")

	// ErrSideNotSibling means that a side branch is not on a query sibling boundary.
	ErrSideNotSibling = errors.New("netsec: side branch is not on a query sibling boundary")

	// ErrTooManySides means that the side count cannot fit the query set.
	ErrTooManySides = errors.New("netsec: too many side branches")

	// ErrUnconsumedSide means that verification did not use a supplied side branch.
	ErrUnconsumedSide = errors.New("netsec: unconsumed side branch")

	// ErrRootMismatch means that the reconstructed root differs from the trusted root.
	ErrRootMismatch = errors.New("netsec: reconstructed root does not match trusted root")

	// ErrDuplicateUpdate means that an update batch repeats a key.
	ErrDuplicateUpdate = errors.New("netsec: duplicate update key")

	// ErrUpdateKeyNotProven means that a batch update key is not in the proof.
	ErrUpdateKeyNotProven = errors.New("netsec: update key is not covered by the proof")

	// ErrInvalidUpdate means that an update has contradictory delete/value fields.
	ErrInvalidUpdate = errors.New("netsec: invalid update")

	// ErrNilProof means that a nil proof was supplied.
	ErrNilProof = errors.New("netsec: nil proof")

	// ErrMalformedProof means that encoded proof bytes are malformed.
	ErrMalformedProof = errors.New("netsec: malformed encoded proof")

	// ErrTrailingBytes means that encoded proof bytes remain after decoding.
	ErrTrailingBytes = errors.New("netsec: trailing bytes after proof")
)
