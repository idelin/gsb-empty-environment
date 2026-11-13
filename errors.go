package netsec

import "errors"

var (
	// ErrInvalidASN is returned when a host supplies zero, a reserved ASN, or
	// an ASN outside the 32-bit range.
	ErrInvalidASN = errors.New("invalid ASN")
	// ErrInvalidDataset is returned for malformed ASPA dataset input.
	ErrInvalidDataset = errors.New("invalid ASPA dataset")
	// ErrInvalidSession is returned when the host supplies an unusable session.
	ErrInvalidSession = errors.New("invalid routing session")
	// ErrInvalidPath is returned when AS_PATH cannot be represented as one or
	// more AS_SEQUENCE segments.
	ErrInvalidPath = errors.New("invalid AS_PATH")
	// ErrEmptyPath is returned for an empty AS_PATH or empty segment.
	ErrEmptyPath = errors.New("empty AS_PATH")
	// ErrNonSequenceSegment is returned for AS_SET or confederation segments.
	ErrNonSequenceSegment = errors.New("non-AS_SEQUENCE path segment")
	// ErrLocalASInPath is returned when the local AS occurs in AS_PATH.
	ErrLocalASInPath = errors.New("local AS appears in AS_PATH")
	// ErrPathLoop is returned when the compressed AS path contains a repeated
	// AS other than an adjacent prepend run.
	ErrPathLoop = errors.New("AS_PATH loop")
)
