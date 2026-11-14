// Package netsec implements a receiver-index and handshake lifecycle component
// for a WireGuard tunnel host.
//
// A numeric receiver index is only a routing label. The identity of an index
// allocation is the tuple (device instance, peer static public key, handshake
// generation, temporary public key, creation nonce, opaque binding ID). That
// tuple is frozen into a Capability when Allocate returns a Binding.
//
// Lifecycle:
//
//	InitiationSent -> ResponseAccepted -> KeypairActive -> Retired -> Quarantine
//
// HandleResponse accepts a transition only when the response MAC, timestamp,
// and handshake hash all match the binding. HandleCookie additionally checks
// the cookie MAC, offending-message digest, and original source address family;
// CookieNATPolicy decides whether a changed source port (or, explicitly, a
// changed IP in the same family) is accepted.
//
// A quarantined index is not returned to the allocator until all configured
// old-key message gates close, RejectAfterTime elapses, and every CryptoTask
// started against the binding has finished. Work that finishes late holds a
// *Binding and BindingID, not a numeric index, so it can only release or
// destroy the old material. It cannot resolve or mutate a replacement binding
// that happens to carry the same numeric index.
//
// Forced index collisions are supported through AllocationRequest.ForceIndex.
// They are deliberately kept in one index bucket and disambiguated by
// authenticated message fields; the collision never merges capabilities.
package netsec
