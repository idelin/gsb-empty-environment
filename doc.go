// Package netsec enforces the extended_master_secret (RFC 7627) policy for
// TLS 1.2 full handshakes and session resumption.
//
// # Enforced policy
//
// During a full handshake EMS is negotiated only when both peers advertise
// the extension; a single-sided advertisement fails instead of downgrading.
// When neither peer advertises it a classic master secret is still derived,
// matching a TLS stack that treats EMS as mandatory whenever support exists.
//
// During resumption the negotiated EMS state must be symmetric and must equal
// the flag recorded for the original session. An EMS session that loses the
// flag and a legacy session that gains it are both rejected. The EMS flag is
// never serialized into resumption tickets: the server-side SessionStore is
// the sole authoritative source, so a ticket cannot carry a forged or
// stripped flag.
//
// # Integration
//
// A TLS stack calls NegotiateEMS when parsing ServerHello, DeriveMasterSecret
// after the key exchange, ValidateResumption before reusing a session, and
// TicketCipher plus SessionStore to mint and resolve tickets. Server and
// MemoryClientSessionCache provide end-to-end orchestration helpers.
package netsec
