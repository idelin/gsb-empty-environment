package bufauth

import "errors"

var (
	// ErrNoPerms is returned when a grant is requested without permissions.
	ErrNoPerms = errors.New("bufauth: grant must request at least one permission")
	// ErrUnknownPerm is returned when a permission mask contains unknown bits.
	ErrUnknownPerm = errors.New("bufauth: permission mask contains unknown bits")
	// ErrPermEscalation is returned when a derived grant asks for rights the
	// upstream grant does not hold.
	ErrPermEscalation = errors.New("bufauth: requested permissions exceed upstream grant")
	// ErrGrantRevoked is returned when a revoked grant is used.
	ErrGrantRevoked = errors.New("bufauth: grant has been revoked")
	// ErrUnknownGrant is returned when a grant id is not managed by the authority.
	ErrUnknownGrant = errors.New("bufauth: unknown grant")
	// ErrBufferMismatch is returned when a grant for one buffer is used with a
	// file descriptor belonging to another buffer.
	ErrBufferMismatch = errors.New("bufauth: grant and transfer reference different buffers")
	// ErrReexportDenied is returned when a grant without PermReexport is used
	// to forward a buffer.
	ErrReexportDenied = errors.New("bufauth: upstream grant may not reexport")
	// ErrBadFD is returned when the supplied file descriptor is not valid.
	ErrBadFD = errors.New("bufauth: invalid file descriptor")
	// ErrTransferGone is returned when a transfer token is unknown, already
	// claimed, or reclaimed.
	ErrTransferGone = errors.New("bufauth: transfer token unknown, claimed, or reclaimed")
)
