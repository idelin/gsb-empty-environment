package netsec

import "errors"

// Sentinel errors returned by the lazy binding machinery. Resolver
// implementations may wrap any of these; callers should use errors.Is.
var (
	// ErrCycle is returned when a re-entrant resolution reaches a slot that
	// is already being resolved by an ancestor frame of the same chain.
	ErrCycle = errors.New("lazybind: symbol resolution cycle detected")

	// ErrStaleGeneration is returned when a resolver callback finishes after
	// its slot generation has been invalidated (for example because a module
	// was unloaded while the resolution was in flight). The result of such a
	// callback is never published to the new generation.
	ErrStaleGeneration = errors.New("lazybind: resolver result belongs to a stale generation")

	// ErrBadSignature is returned when the target module fails signature
	// verification before a resolved address is published.
	ErrBadSignature = errors.New("lazybind: target module signature verification failed")

	// ErrAddressOutOfRange is returned when a resolved address does not fall
	// inside the target module's declared [Base, Base+Size) range.
	ErrAddressOutOfRange = errors.New("lazybind: resolved address outside target module range")

	// ErrModuleNotLoaded is returned when the target module of a symbol is
	// missing from the registry or has been unloaded.
	ErrModuleNotLoaded = errors.New("lazybind: target module is not loaded")

	// ErrSlotNotFound is returned when querying a slot that has never been
	// resolved.
	ErrSlotNotFound = errors.New("lazybind: slot not found")
)
