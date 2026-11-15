// Package netsec implements connection-layer authenticated record reception
// with AES-GCM and a per-epoch 1024-record replay window.
//
// Each Record carries a connection identifier, epoch, uint64 sequence number,
// and ciphertext. The 12-byte GCM nonce is the big-endian epoch followed by the
// big-endian sequence. The AAD is the big-endian uint32 byte length of the
// connection identifier, the identifier itself, the big-endian uint32 epoch,
// and the big-endian uint64 sequence.
//
// A Receiver tracks at most the current and immediately previous epochs.
// Encryption keys are immutable per epoch, epochs must increase, and the
// previous epoch remains readable until the caller explicitly retires it.
//
// Receive authenticates a record before consulting the replay window, so
// forged sequences cannot advance it. Sequence claims and duplicate rejection
// are atomic across concurrent Receive calls. A sequence is consumed before
// the delivery callback runs; callback errors do not make the record deliverable
// again.
package netsec
