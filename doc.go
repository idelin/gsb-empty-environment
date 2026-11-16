// Package netsec maintains the per-connection HTTP/2 priority state defined
// by RFC 9218 ("Extensible Prioritization Scheme for HTTP").
//
// State is intended to be driven directly from the server's frame
// processing loop: UpdateFrame consumes a complete PRIORITY_UPDATE frame
// payload (a 4-byte stream identifier followed by the Priority Field Value),
// StreamOpened and StreamClosed track stream lifecycle, and Priority or
// EffectivePriority answer scheduler queries.
//
// Hardening against state-exhaustion attacks:
//
//   - Stream identifiers 0, even identifiers, and identifiers with the
//     reserved R bit set are rejected as connection errors, because request
//     streams are client-initiated and use odd identifiers.
//   - The Priority Field Value is parsed as an RFC 8941 Structured Fields
//     dictionary before any state changes, so malformed values never create
//     entries. "u" must be an integer in 0..7 and "i" a boolean; unknown
//     keys are ignored and duplicate keys use the last value. A bad
//     recognized key discards the whole field value.
//   - At most 64 entries are kept for streams that have not been opened;
//     overflow evicts the oldest entry.
//   - Closing a stream removes its entry and its identifier is remembered,
//     so later PRIORITY_UPDATE frames for that stream are ignored without
//     re-creating state. The closed-identifier set is compacted behind a
//     monotonic watermark and therefore stays bounded.
package netsec
