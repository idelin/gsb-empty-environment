// Package netsec sanitizes Authentication-Results headers on inbound mail.
//
// Senders can inject forged Authentication-Results headers, so a receiving
// host must never trust the topmost "pass" verbatim. This package lets a Go
// mail ingestion host attest, per receiving connection, which trusted nodes
// added which headers, and then separates verified results from external
// same-name headers, which are preserved verbatim as untrusted evidence.
//
// Trust is bound to the ordered node chain: each header must match the
// attestation of the expected hop (source node identity and authentication
// methods). A duplicate node, a renamed forwarder, a stripped header, or a
// proof replayed from a different connection breaks the chain and refuses
// any further trust elevation.
package netsec
