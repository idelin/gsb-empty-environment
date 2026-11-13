// Package netsec provides admission control and atomic hot switching for
// low-rank adapters in a Go inference host.
//
// The host supplies an immutable base-model manifest, a detached-signature
// verifier, and a provider of audited read-only tensor handles. Manager.Load
// verifies the complete manifest bytes, validates every target against the
// host-fixed layer dimensions, reads and hashes every factor tensor, and only
// then publishes an immutable Generation.
//
// Requests must pin a generation with Manager.Acquire and release the returned
// Lease when the request completes. Publishing a replacement only redirects
// new requests. Revoking an adapter blocks new requests, while in-flight leases
// keep the old generation alive; the final release clears its increment
// tensors.
package netsec
