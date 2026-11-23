// Package destruct coordinates cryptographic destruction of every
// decryptable key replica belonging to a confidential volume while the
// on-disk ciphertext itself is retained.
//
// # Identity model
//
// A volume has an immutable, unique VolumeID assigned at creation. The human
// readable volume name is only a label: creating another volume with the same
// name always produces a new VolumeID. Destruction transactions, leases and
// erasure confirmations are all keyed by VolumeID (and TxnID), so name reuse
// can never merge a new volume into an old destruction transaction.
//
// # Destruction state machine
//
// A destruction transaction (DestroyTxn) is persisted before the volume is
// sealed and moves strictly forward:
//
//		sealing -> erasing -> destroyed
//
//	  - sealing: the volume is atomically flipped Usable -> Destroying with a
//	    compare-and-swap. New lease grants (and renewals) are rejected from that
//	    point on. In-flight active leases are then awaited until they expire or
//	    are force-revoked.
//	  - erasing: every key replica in the transaction's immutable replica
//	    snapshot is asked to erase its key and return a signed erasure
//	    confirmation. Each confirmation is checked against the snapshot:
//	    unknown replica, location/generation mismatch, foreign volume/txn or
//	    invalid signature is rejected and keeps the transaction incomplete.
//	  - destroyed: reachable only when one valid confirmation exists for every
//	    snapshot replica. The volume is then flipped Destroying -> Destroyed.
//
// There is intentionally no transition back to Usable. Once sealing has
// started, a crash, storage fault or failed erasure can only delay
// destruction, never restore the volume.
//
// # Crash recovery and idempotency
//
// Every phase transition and every collected confirmation is durable before
// the next step. Recover (or a repeated Destroy call) reloads the persisted
// transaction and resumes at the phase it crashed in. All writes are
// idempotent: transaction adoption, CAS transitions and
// content-checked confirmation writes tolerate lost responses and retries.
package destruct
