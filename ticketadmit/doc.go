// Package ticketadmit provides admission control for TLS session tickets so
// that a ticket issued by one virtual host cannot be replayed onto another to
// bypass client authentication during PSK resumption.
//
// # Threat model
//
// A permissive virtual host (one that does not require client certificates)
// hands a resumption ticket to any client. Without binding the ticket to the
// context in which it was issued, an attacker can present that ticket to a
// different, strict virtual host and ride PSK resumption past the client
// certificate request, because resumed sessions do not re-run client
// authentication.
//
// Each ticket therefore seals the following claims with AES-256-GCM:
//
//   - SNI of the issuing virtual host
//   - negotiated ALPN protocol
//   - SHA-256 of the leaf client certificate (absent when no cert was used)
//   - negotiated cipher suite
//   - issuance timestamp
//
// On resumption the Admitter enforces that the ticket's SNI matches the
// current ClientHello, that the client-certificate posture of the target
// virtual host is consistent with the fingerprint carried in the ticket, and
// that the ticket is at most seven days old. Any failure results in a full
// handshake rather than resumption.
//
// # Key rotation
//
// A KeyRing holds at most two key generations: the current key seals new
// tickets, while both the current and previous generations can open tickets.
// Rotate with KeyRing.Rotate; tickets sealed by a key that has aged out after
// two rotations (three generations old) become unknown tickets and are simply
// ignored.
//
// # Unknown tickets never abort the handshake
//
// Tickets that fail AEAD authentication, use an unknown key generation, or
// are otherwise unrecognizable return ErrUnknownTicket. The TLS adapter maps
// every such outcome to a (nil, nil) UnwrapSession result, which crypto/tls
// treats as "no resumable session" and continues with a full handshake. An
// unknown ticket must never produce a fatal alert.
//
// # Early data
//
// When a client offers 0-RTT data, the age reconstructed from
// obfuscated_ticket_age and ticket_age_add is compared with the age the server
// computes from the issuance timestamp. A discrepancy greater than ten
// seconds rejects the early data while still permitting the (1-RTT) PSK
// resumption.
//
// # Wiring to crypto/tls
//
// Build an Admitter over a KeyRing, wrap it in a TLSAdapter with a
// VhostPolicyFunc, and install the callbacks on each virtual host's
// tls.Config:
//
//	ring, _ := ticketadmit.NewKeyRing(currentKey, previousKey)
//	admitter, _ := ticketadmit.NewAdmitter(ring)
//	adapter, _ := ticketadmit.NewTLSAdapter(admitter, policyBySNI)
//	adapter.Configure(tlsConfig)
//
// For non-crypto/tls PSK stacks (for example QUIC), call Admitter.Issue and
// Admitter.Admit directly with the connection's binding values.
package ticketadmit
