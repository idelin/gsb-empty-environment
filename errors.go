package netsec

import "errors"

var (
	// ErrTooManyVirtualObjects means the input exceeded MaxVirtualObjects.
	ErrTooManyVirtualObjects = errors.New("netsec: too many virtual object definitions")
	// ErrDuplicateVirtual means a virtual identifier was defined twice.
	ErrDuplicateVirtual = errors.New("netsec: duplicate virtual object definition")
	// ErrMissingVirtual means a reference named a virtual identifier that has
	// no definition.
	ErrMissingVirtual = errors.New("netsec: missing virtual object definition")
	// ErrInvalidValueKind means a Value carried an unknown kind.
	ErrInvalidValueKind = errors.New("netsec: invalid value kind")
	// ErrInvalidClosedField means the reserved closed field was not an integer.
	ErrInvalidClosedField = errors.New("netsec: closed field must be an integer")
	// ErrInvalidReconstruction means the input could not be interpreted as a
	// reconstruction description.
	ErrInvalidReconstruction = errors.New("netsec: invalid reconstruction description")
	// ErrNoAllocator means no usable allocator was supplied.
	ErrNoAllocator = errors.New("netsec: no allocator")
	// ErrAllocationFailed wraps allocator failures.
	ErrAllocationFailed = errors.New("netsec: allocation failed")
	// ErrNilObject means an allocator succeeded without returning an object.
	ErrNilObject = errors.New("netsec: allocator returned a nil object")
	// ErrObjectIDCollision means the allocator reused an identifier that
	// already belonged to an existing host object.
	ErrObjectIDCollision = errors.New("netsec: allocator reused an existing object id")
	// ErrDuplicateObjectID means two virtual objects were assigned one
	// concrete object identifier.
	ErrDuplicateObjectID = errors.New("netsec: duplicate concrete object id")
	// ErrObjectClosed is returned when a read targets a closed object.
	ErrObjectClosed = errors.New("netsec: object is closed")
	// ErrMissingField is returned when a field does not exist.
	ErrMissingField = errors.New("netsec: missing field")
	// ErrNoRelease means an allocator provided no way to reclaim allocations.
	ErrNoRelease = errors.New("netsec: allocator has no release operation")
	// ErrInjectedAllocationFailure is used by Heap's failure injection.
	ErrInjectedAllocationFailure = errors.New("netsec: injected allocation failure")
)
