package netsec

import "errors"

var (
	ErrInvalidField      = errors.New("invalid prime field")
	ErrInvalidShare      = errors.New("share out of range")
	ErrInvalidTriple     = errors.New("invalid triple")
	ErrTripleID          = errors.New("invalid or duplicate triple ID")
	ErrTripleAlreadyUsed = errors.New("triple ID already used")
	ErrTripleNotChecked  = errors.New("triple has not passed sacrifice check")
	ErrTripleMismatch    = errors.New("triple shares do not match checked digest")
	ErrMissingMessage    = errors.New("missing party message")
	ErrDuplicateSender   = errors.New("duplicate sender")
	ErrConflictingValues = errors.New("conflicting values from same sender")
	ErrBadSignature      = errors.New("invalid Ed25519 signature")
	ErrWrongContext      = errors.New("message bound to a different round, triple pair, or field")
	ErrMalformedMessage  = errors.New("malformed message")
	ErrOpeningMismatch   = errors.New("opening share does not match the sending party's local share")
	ErrCommitMismatch    = errors.New("reveal does not match commit")
	ErrSacrificeFailed   = errors.New("sacrifice check rejected the triple pair")
	ErrRandom            = errors.New("randomness failure")
	ErrStore             = errors.New("persistent store failure")
	ErrClosed            = errors.New("host is closed")
	ErrRange             = errors.New("value out of field range")
)
