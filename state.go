package netsec

// SlotState is the lifecycle state of a lazy binding jump slot.
//
// State transitions:
//
//	Unresolved -> Resolving -> Bound
//	                     |        ^
//	                     v        |
//	                   Failed ----+   (a later call may retry)
//
// Module unloading forcibly returns an active slot to Unresolved under a
// new generation number.
type SlotState uint32

const (
	// StateUnresolved means the slot has never been bound or has been
	// invalidated by a module unload.
	StateUnresolved SlotState = iota

	// StateResolving means a resolver callback currently owns the slot.
	// Other callers wait on the slot's completion channel instead of
	// issuing a second concurrent callback.
	StateResolving

	// StateBound means the slot holds a verified target address and may be
	// used without taking the resolution path again.
	StateBound

	// StateFailed means the last resolution attempt failed. The next call
	// is allowed to retry from this state.
	StateFailed
)

// String renders the state for diagnostics and tests.
func (s SlotState) String() string {
	switch s {
	case StateUnresolved:
		return "unresolved"
	case StateResolving:
		return "resolving"
	case StateBound:
		return "bound"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
