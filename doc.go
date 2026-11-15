// Package netsec provides fixed-trajectory, authenticated output shaping for
// an isolated task whose completion time must not appear in the external frame
// count, size, or slot schedule.
//
// A Plan fixes N slots and L external bytes per slot. A task may submit result
// bytes while it runs. Every slot emits exactly L AES-GCM bytes: data slots
// carry queued result or encrypted zero padding, and the final slot carries an
// encrypted uint64 length plus a success/failure byte. Result bytes occupy a
// contiguous suffix of the data-slot stream (the first partial slot is
// right-aligned), so a final length is sufficient to recover them regardless
// of when they were submitted. Nonces are big-endian(round uint32) ||
// big-endian(slot uint64), and associated data binds the public plan, round,
// and slot.
//
// The package hides only what is inside authenticated ciphertext and the
// resulting externally visible frame trace. It makes no claim about process
// scheduling, CPU/cache activity, memory access, network metadata, or other
// side channels outside the encrypted frames.
package netsec
