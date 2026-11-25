package uffd

import "errors"

var (
	// ErrUnprivilegedDisabled means the host explicitly disables
	// unprivileged userfaultfd through vm.unprivileged_userfaultfd.
	ErrUnprivilegedDisabled = errors.New("uffd: unprivileged userfaultfd is disabled")

	// ErrUserModeOnlyRequired means an unprivileged caller omitted
	// UFFD_USER_MODE_ONLY.
	ErrUserModeOnlyRequired = errors.New("uffd: UFFD_USER_MODE_ONLY is required")

	// ErrUnknownCreateFlag means the creation request contains a flag the
	// gateway does not understand.
	ErrUnknownCreateFlag = errors.New("uffd: unknown userfaultfd creation flag")

	// ErrInvalidRange means a range has an impossible start/end combination.
	ErrInvalidRange = errors.New("uffd: invalid range")

	// ErrZeroLength means a range has zero length.
	ErrZeroLength = errors.New("uffd: range length must be non-zero")

	// ErrRangeOverflow means start+length wraps around.
	ErrRangeOverflow = errors.New("uffd: range end overflows")

	// ErrUnaligned means a range is not page aligned.
	ErrUnaligned = errors.New("uffd: range is not page aligned")

	// ErrInvalidMapping means a supplied mapping has an invalid interval.
	ErrInvalidMapping = errors.New("uffd: invalid mapping")

	// ErrNotAnonymousPrivate means registration crossed a non-anonymous or
	// non-private mapping.
	ErrNotAnonymousPrivate = errors.New("uffd: range is not anonymous private memory")

	// ErrNotOwner means a mapping or descriptor belongs to another process.
	ErrNotOwner = errors.New("uffd: mapping is not owned by the caller")

	// ErrUnmappedHole means the requested registration range is not fully
	// backed by mappings.
	ErrUnmappedHole = errors.New("uffd: range crosses an unmapped hole")

	// ErrNotHolder means the caller does not hold the descriptor.
	ErrNotHolder = errors.New("uffd: caller does not hold the userfaultfd")

	// ErrNotCreator means a descriptor receiver tried to change its
	// registration state.
	ErrNotCreator = errors.New("uffd: only the descriptor creator may change registrations")

	// ErrModeRequired means a register request selected no access mode.
	ErrModeRequired = errors.New("uffd: at least one register mode is required")

	// ErrUnknownMode means a register request contains an unknown mode bit.
	ErrUnknownMode = errors.New("uffd: unknown register mode")

	// ErrPermissionDenied means an operation needs an additional grant.
	ErrPermissionDenied = errors.New("uffd: operation requires additional permission")

	// ErrPartialOverlap means a registration partially overlaps an existing
	// registration.
	ErrPartialOverlap = errors.New("uffd: partial overlap with an existing registration")

	// ErrModeConflict means the same range was re-registered with a
	// different mode.
	ErrModeConflict = errors.New("uffd: range is already registered with a different mode")

	// ErrNotRegistered means the range is not fully covered by existing
	// registrations.
	ErrNotRegistered = errors.New("uffd: range is not registered")

	// ErrModeMismatch means the registration does not include the mode the
	// requested operation needs.
	ErrModeMismatch = errors.New("uffd: registration mode does not allow this operation")

	// ErrInvalidAPI means the UFFDIO_API handshake used an unsupported
	// version or feature set.
	ErrInvalidAPI = errors.New("uffd: invalid UFFDIO_API request")

	// ErrUnknownIoctl means an ioctl request is not recognized.
	ErrUnknownIoctl = errors.New("uffd: unknown ioctl request")
)
