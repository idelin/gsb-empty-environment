// Package netsec implements RFC 8689 REQUIRETLS policy for an SMTP relay.
//
// Inbound sessions (InboundServer) advertise REQUIRETLS but accept the
// MAIL FROM parameter only after STARTTLS has completed. A cleartext
// REQUIRETLS command is rejected with a 5xx reply and no transaction is
// opened. At DATA time the TLS-Required header is checked against the
// envelope flag.
//
// Outbound delivery (Deliverer) treats a message with RequireTLS as an
// all-or-nothing requirement: the next hop must offer STARTTLS, the
// handshake must pass the caller's VerifyCertificate callback, and the
// post-STARTTLS EHLO response must advertise REQUIRETLS. Any failure is a
// permanent DeliveryError; the message is never retried in cleartext.
//
// The sender's MTA adds TLS-Required: Require to messages sent with the
// REQUIRETLS parameter. TLS-Required: No is accepted only when the envelope
// did not use REQUIRETLS.
package netsec
