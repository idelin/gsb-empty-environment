package par

import "errors"

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound means no entry exists for the reference.
	ErrNotFound = errors.New("par: request_uri not found")
	// ErrExpired means the entry exists but its time-to-live has elapsed.
	ErrExpired = errors.New("par: request_uri expired")
	// ErrUsed means the one-time reference has already been consumed.
	ErrUsed = errors.New("par: request_uri has already been used")
	// ErrConflict means a freshly generated reference collides with an
	// existing entry; the caller should generate a new reference.
	ErrConflict = errors.New("par: request_uri reference conflict")
)
