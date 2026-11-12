// Package netsec validates io_uring submission queue entries (SQEs) that a
// privileged proxy submits on behalf of an untrusted client.
//
// Background: io_uring registered ("fixed") files keep an internal reference
// that can outlive the original file descriptor, and linked SQEs can hop
// between slots. The kernel therefore cannot be relied upon to notice that a
// client's fd was closed or that its rights were revoked. This package moves
// that enforcement into the proxy:
//
//   - ParseSQE decodes exactly 64 bytes into an SQE value.
//   - SlotTable stores each fixed-file slot together with an immutable
//     Credential snapshot, its read/write Access mask and a sticky
//     SlotRevoked state. Revocation survives until a new-generation
//     RegisterSlot call.
//   - Validator enforces, per SQE: the opcode whitelist, allowed IOSQE flag
//     bits, IOSQE_FIXED_FILE index bounds (including the 6.12
//     IOSQE_FILE_INDEX_UPPER extended encoding), slot read/write
//     permissions, and sqe.personality against the slot credential.
//   - Validator.ValidateBatch additionally treats IOSQE_IO_LINK /
//     IOSQE_IO_HARDLINK sequences as one chain: the chain may never switch
//     CredID or personality, dangling links are rejected, and the whole
//     batch is judged against one table snapshot to close TOCTOU windows.
//
// Every rejection is a *RuleError wrapping one of the exported sentinel
// errors, so callers can branch on errors.Is while still locating the failed
// rule and batch position.
package netsec
