package ptrauth

import "errors"

// Sentinel errors returned by the authentication gate. Callers can use
// errors.Is to distinguish forged tags, context violations and retired
// generations; every failure mode denies the requested access.
var (
	// ErrWeakKey is returned when an Authenticator key is too short.
	ErrWeakKey = errors.New("ptrauth: authenticator key must be at least 16 bytes")

	// ErrUnknownAddress is returned when no object is registered at the
	// authenticated address.
	ErrUnknownAddress = errors.New("ptrauth: address is not registered")

	// ErrAddressLive is returned when a live object already owns the target
	// address and a second registration is attempted.
	ErrAddressLive = errors.New("ptrauth: address already owned by a live object")

	// ErrTagForged is returned when the HMAC tag is missing, truncated,
	// computed with another key, or does not match the presented pointer
	// attributes.
	ErrTagForged = errors.New("ptrauth: pointer authentication tag missing or forged")

	// ErrContextMismatch is returned when a pointer authenticated for one
	// call context is presented in another context without going through
	// the explicit Recontext gate.
	ErrContextMismatch = errors.New("ptrauth: pointer used outside its authenticated call context")

	// ErrKindNotPermitted is returned for kind widening (for example a
	// read-only pointer used for writes, or a data pointer called as code)
	// and for derivation that adds capabilities the parent lacks.
	ErrKindNotPermitted = errors.New("ptrauth: access kind exceeds the authenticated pointer kind")

	// ErrWXConflict is returned when one tag carries both write and execute
	// rights; writable and executable memory are mutually exclusive.
	ErrWXConflict = errors.New("ptrauth: writable and executable kinds are mutually exclusive")

	// ErrZeroKind is returned when an attempt is made to authenticate a
	// pointer without any access kind.
	ErrZeroKind = errors.New("ptrauth: authenticated pointer must carry at least one kind")

	// ErrEmptyObject is returned when a zero-length backing object is
	// registered.
	ErrEmptyObject = errors.New("ptrauth: cannot register a zero-length object")

	// ErrGenerationRetired is returned when the object backing a pointer has
	// been freed and its generation retired.
	ErrGenerationRetired = errors.New("ptrauth: pointer generation retired because the object was freed")

	// ErrGenerationStale is returned when the address was reused by a newer
	// object generation after the pointer was authenticated.
	ErrGenerationStale = errors.New("ptrauth: pointer generation is stale: address was reused")

	// ErrOutOfBounds is returned when an access or derivation escapes the
	// authenticated pointer span.
	ErrOutOfBounds = errors.New("ptrauth: access exceeds authenticated pointer span")

	// ErrNotExecutable is returned when Call targets a data object.
	ErrNotExecutable = errors.New("ptrauth: target object is not executable")

	// ErrNotData is returned when a memory read/write targets a code-only
	// object.
	ErrNotData = errors.New("ptrauth: target object has no data backing")

	// ErrOffsetOverflow is returned when pointer arithmetic wraps the
	// address space.
	ErrOffsetOverflow = errors.New("ptrauth: offset overflows the address space")
)
