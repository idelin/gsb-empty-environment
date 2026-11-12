// Package netsec coordinates cryptographic key destruction for confidential
// volumes.
//
// A confidential volume keeps its on-disk ciphertext but becomes permanently
// unreadable once every decrypting key copy is invalidated. The Coordinator
// drives that process as a durable destruction transaction:
//
//   - New leases are prohibited before any key copy is touched.
//   - Existing leases are drained until none is active.
//   - Erasure confirmations are collected for every persisted key replica.
//   - A replica that is not part of the volume manifest forces the result to
//     remain incomplete until it is gone.
//
// The transaction state is persisted through the BlobStore interface, so a
// process crash can be resumed along the same transaction and a destroying
// volume can never return to a usable state.
package netsec
