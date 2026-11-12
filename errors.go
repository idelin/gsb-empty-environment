package netsec

import "errors"

var (
	// ErrMalformedInput indicates a malformed TPM2B structure.
	ErrMalformedInput = errors.New("malformed TPM structure")
	// ErrSignature indicates the Quote signature failed verification.
	ErrSignature = errors.New("quote signature verification failed")
	// ErrNonceMismatch indicates the Quote carries an unexpected nonce.
	ErrNonceMismatch = errors.New("quote nonce mismatch")
	// ErrPCRSelectionMismatch indicates the Quote PCR selection is not the
	// selection requested by the caller.
	ErrPCRSelectionMismatch = errors.New("quote PCR selection does not match requested selection")
	// ErrPCRDigestMismatch indicates the replayed PCR values do not reproduce
	// the digest embedded in the Quote.
	ErrPCRDigestMismatch = errors.New("replayed PCR digest does not match quote")
	// ErrMalformedEventLog indicates a structurally invalid TCG event log.
	ErrMalformedEventLog = errors.New("malformed TCG event log")
	// ErrTruncatedEvent indicates an event log entry that ends prematurely.
	ErrTruncatedEvent = errors.New("truncated event log entry")
	// ErrUnknownAlgorithm indicates an algorithm that is not supported.
	ErrUnknownAlgorithm = errors.New("unknown or unsupported hash algorithm")
	// ErrMissingDigest indicates that an event lacks a digest for an
	// algorithm declared by the Spec ID event.
	ErrMissingDigest = errors.New("event missing required digest")
	// ErrNoMatchingIdentity indicates that no allowed sequence matches.
	ErrNoMatchingIdentity = errors.New("measurement sequence matches no allowed device")
)
