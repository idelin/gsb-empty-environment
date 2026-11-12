// Package netsec provides an append-only audit log handle for a Go service.
//
// The Log allocates every write offset itself. Callers receive a Reservation
// with a fixed span and a forward-only write cursor; no Seek, WriteAt, or
// caller-supplied offset is exposed. A record becomes visible only after its
// full payload and trailer are durable, and only records in the contiguous
// committed prefix are readable. Cancelled or failed reservations are sealed
// as explicit holes and are never returned to the allocator. OpenStore
// recovers an interrupted tail by sealing it as a hole rather than reusing it.
package netsec
