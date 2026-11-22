// Package lease provides lease management for a Go HTTP/3 datagram
// dispatcher that migrates a datagram channel from an old connection to a
// new one while preserving the authorization of the same business request.
//
// Identity namespaces: ConnectionID, StreamID and AuthID are distinct
// types. Equal integer values in different namespaces never denote the
// same object, and the compiler enforces the separation.
//
// Leases: the host issues a Lease on every establishment or migration.
// A lease binds (tenant, connection, request stream, authorization,
// generation). Migration supersedes the old lease but keeps it routable
// for a bounded dual-path overlap window.
//
// Sequencing: datagram sequence numbers are 24-bit ring values. The
// initiator declares an anchor and a maximum span; Expand resolves a wire
// value against them and rejects values on the half-ring boundary, which
// cannot be expanded uniquely.
//
// Delivery: duplicates of the same logical sequence with the same payload
// are delivered once. Receiving the same sequence with different payloads
// on the two paths freezes the request and retains both sources; the
// winner is never chosen by comparing arrival times.
//
// Acknowledgement: network acknowledgements (NetAck) and business
// receipts are separate. Acks are scoped by generation, so an ack on the
// old path cannot release the new generation's staging area.
//
// Commit protocol: the receiver's persistent state and business delivery
// are joined by a retryable acknowledgement protocol with three states:
// StateCommitted, StatePending and StateUnknown. If a crash happens
// between delivery and receipt, only a definite answer from the host's
// idempotent inbox may mark a record complete. PausableInbox and
// PausableStore let tests stop at every persistent write point.
package lease
