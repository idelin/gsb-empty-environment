// Package quicguard implements the QUIC (RFC 9000/RFC 9001) server-side
// security state machine for handshake anti-amplification, connection ID
// handling and stateless resets. It is deliberately transport-agnostic: it
// parses raw QUIC packets and accounts bytes, but never performs network I/O
// and never imports any network-facing dependency.
//
// Security properties enforced:
//
//   - A datagram whose first packet is an Initial is rejected unless the whole
//     UDP datagram is at least 1200 bytes (RFC 9000 section 14.1).
//   - Every unvalidated source address keeps precise rxBytes/txBytes counters
//     and the invariant txBytes <= 3*rxBytes holds at all times. Retransmits,
//     ACKs and coalesced datagrams are billed by their exact wire length.
//     Over-budget egress is suspended in a FIFO queue, never truncated and
//     never partially sent (RFC 9000 section 8.1.4).
//   - Retry tokens are AES-256-GCM sealed and bind (clientIP, origDCID,
//     retrySCID, issuedAt). Reuse across IPs, expired tokens and replays are
//     rejected; Retry packets carry an RFC 9001 section 5.8 integrity tag.
//   - Connection migration requires an 8-byte random PATH_CHALLENGE /
//     PATH_RESPONSE exchange before the new path is enabled, and the new path
//     starts with a fresh anti-amplification budget (RFC 9000 section 8.2/9).
//   - Stateless reset packets are always shorter than the triggering packet
//     and at least 21 bytes long, which terminates reset amplification loops
//     (RFC 9000 section 10.3). Reset tokens are HMAC-SHA256 derived from the
//     connection ID and a server-private key; they are only emitted inside a
//     reset packet addressed to the triggering peer.
package quicguard
