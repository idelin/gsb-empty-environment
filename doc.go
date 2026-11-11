// Package netsec implements publish transactions for immutable artifact
// labels.
//
// An immutable label can be bound to exactly one uploaded content digest.
// Concurrent first-time publishes are serialized through a transactional
// [Store]: only one request may win the label, every other request fails with
// [ErrConflict]. A reservation binds an upload digest and a request ID before
// the actual publish, so retries carrying the same request ID are idempotent
// while a retried request carrying a different digest is rejected.
//
// Label lifecycle:
//
//   - [StateAbsent]: no record exists for the label.
//   - [StateReserved]: the label is held for an in-flight publish; the record
//     carries the upload digest, the request ID and an expiry time.
//   - [StatePublished]: the label is immutably bound to the digest.
//   - [StateRevoked]: a reservation was released or expired and no longer
//     blocks future requests.
//
// The uploaded content is re-verified immediately before the
// reserved-to-published transition, even on idempotent retries.
package netsec
