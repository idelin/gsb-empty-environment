package readproof

import (
	"errors"
	"fmt"
)

var (
	// ErrNoVerifier means no root signature verifier was injected.
	ErrNoVerifier = errors.New("readproof: root signature verifier is nil")
	// ErrSignature means the injected verifier rejected the root signature.
	ErrSignature = errors.New("readproof: root signature rejected")
	// ErrRootMismatch means the signed root, authorization view, and request
	// disagree about the trusted root.
	ErrRootMismatch = errors.New("readproof: root mismatch")
	// ErrTenantMismatch means the certificate or authorization view is for a
	// different tenant.
	ErrTenantMismatch = errors.New("readproof: tenant mismatch")
	// ErrMissingNode means a referenced manifest node is absent.
	ErrMissingNode = errors.New("readproof: manifest node missing")
	// ErrIdentityMismatch means stored bytes do not hash to their claimed ID.
	ErrIdentityMismatch = errors.New("readproof: node identity mismatch")
	// ErrInvalidNode means a node has an invalid shape or child interval.
	ErrInvalidNode = errors.New("readproof: invalid manifest node")
	// ErrCycle means the manifest graph reachable from the root contains a
	// directed cycle.
	ErrCycle = errors.New("readproof: manifest graph contains a cycle")
	// ErrLengthOverflow means a logical or physical length overflowed uint64.
	ErrLengthOverflow = errors.New("readproof: length overflow")
	// ErrInvalidRange means a range is reversed or otherwise malformed.
	ErrInvalidRange = errors.New("readproof: invalid byte range")
	// ErrGap means no certificate can cover the requested range.
	ErrGap = errors.New("readproof: read evidence gap")
	// ErrUnauthorized means a certificate segment is not authorized by the
	// current authorization view.
	ErrUnauthorized = errors.New("readproof: unauthorized segment")
	// ErrUnvalidated means a segment is not backed by verified physical bytes.
	ErrUnvalidated = errors.New("readproof: segment is not validated")
	// ErrInvalidCertificate means a certificate is malformed or does not
	// exactly match the requested read.
	ErrInvalidCertificate = errors.New("readproof: invalid certificate")
	// ErrSegmentLimit means the certificate would exceed MaxSegments.
	ErrSegmentLimit = errors.New("readproof: segment limit exceeded")
)

// Gap reasons returned in GapError.Reason.
const (
	ReasonOutsideRoot     = "outside root logical range"
	ReasonNoAuthorization = "no active authorization covers position"
	ReasonRevoked         = "authorization covering position is revoked"
	ReasonNoValidatedData = "no validated physical bytes cover position"
	ReasonMissingManifest = "manifest path is unavailable"
)

// GapError identifies the first root-coordinate byte for which no usable
// evidence exists.
type GapError struct {
	Offset uint64
	Range  ByteRange
	Reason string
}

func (e *GapError) Error() string {
	return fmt.Sprintf("readproof: evidence gap at root byte %d: %s", e.Offset, e.Reason)
}

// Is lets errors.Is(err, ErrGap) succeed.
func (e *GapError) Is(target error) bool { return target == ErrGap }

// SegmentLimitError reports that a safe proof would need more than Limit
// segments.  No partial certificate is returned.
type SegmentLimitError struct {
	Limit int
}

func (e *SegmentLimitError) Error() string {
	return fmt.Sprintf("readproof: segment limit exceeded (limit %d)", e.Limit)
}

// Is lets errors.Is(err, ErrSegmentLimit) succeed.
func (e *SegmentLimitError) Is(target error) bool { return target == ErrSegmentLimit }
