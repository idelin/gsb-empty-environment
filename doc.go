// Package netsec implements the TPM2 Quote / TCG event log consistency
// component used by the Go side of a remote attestation service.
//
// A TPM2 Quote signature alone does not authenticate the boot measurements:
// an attacker can present a validly signed Quote alongside a forged event
// log. Verify therefore binds the Quote to the supplied log by:
//
//   - verifying the Quote signature against the supplied AK public key,
//   - checking the Quote nonce and PCR selection against verifier inputs,
//   - replaying every TCG event log extend using each event's own digest
//     algorithm, where the Spec ID event only defines the digest layout and
//     is never extended,
//   - rejecting missing digests, unknown algorithms and truncated entries,
//   - requiring the replayed PCR values to reproduce the Quote PCR digest
//     byte-for-byte, and
//   - releasing a device identity only when the replayed measurement
//     sequence matches a caller-supplied AllowedSequence.
package netsec
