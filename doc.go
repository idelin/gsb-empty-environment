// Package netsec implements a generation-checked object lifecycle for
// host/script capability tests.
//
// The safe model binds every method ticket to the logical object ID, reusable
// address, allocation generation, capability generation, and host resource ID.
// Revoke increments the capability generation before clearing the target
// resource, so an old ticket cannot be made valid by a finalizer.
//
// Finalizers execute without the runtime lock. They may attempt WeakGet and
// request one or more global slots, but the host policy and current revocation
// state are checked after the callback returns. At most one request is
// accepted, and only while the capability is still unrevoked.
package netsec
