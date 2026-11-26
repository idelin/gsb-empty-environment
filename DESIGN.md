# quicguard design

`quicguard` is a dependency-free Go state machine that implements the
server-side QUIC security controls from RFC 9000 and RFC 9001. It never opens
a socket: the transport host feeds raw datagrams in and decides when to write
the returned byte slices to UDP.

## Components

| File | Responsibility |
| --- | --- |
| `header.go` | Raw long-header (Initial/0-RTT/Handshake/Retry) and short-header (1-RTT) parser, varint codec, coalesced-datagram splitter. |
| `guard.go` | Per-source `rxBytes`/`txBytes` accounting, the strict `txBytes <= 3*rxBytes` invariant, FIFO suspension queue, validation transitions. |
| `retry.go` | AES-256-GCM Retry tokens bound to `(clientIP, origDCID, retrySCID, issuedAt)`, nonce replay table, RFC 9001 §5.8 Retry integrity tag. |
| `migration.go` | 8-byte random `PATH_CHALLENGE`/`PATH_RESPONSE` frames and new-path budget reset. |
| `reset.go` | HMAC-SHA256 CID-bound stateless reset tokens, length-validated reset packets, reset-loop interception. |
| `hkdf.go` | RFC 5869 HKDF-Extract and RFC 8446 HKDF-Expand-Label (HMAC-SHA256). |

## State machine

```
                 1200-byte Initial (rejected before counting if smaller)
                                      |
                                      v
                       +------------------------------+
        UDP datagram ->| unvalidated path             |
                       | rxBytes += len(datagram)     |
                       | send allowed only while      |
                       | txBytes+len <= 3*rxBytes     |
                       | otherwise whole packet queued|
                       +---------------+--------------+
                                       |
        valid Retry token (IP+CID+TTL+ |  Handshake packet
        single-use nonce)              |  or ConfirmHandshake
                                       v
                       +------------------------------+
                       | validated path               |
                       | multiplier lifted, queue     |
                       | drained in FIFO order         |
                       +------------------------------+
```

Connection migration (`BeginPathValidation`) replaces the record for the new
source address with a zeroed unvalidated one, so a new path can never inherit
an old path's amplification budget. It becomes validated only after a
constant-time match of the returned `PATH_RESPONSE`.

## Key invariants

- The 1200-byte check runs on the full UDP datagram when its first packet is an
  Initial; rejected datagrams are never counted.
- Every egress attempt (ACK, retransmit, coalesced datagram, PATH_CHALLENGE)
  goes through `Guard.Send`, which bills the exact wire length. An over-budget
  packet is stored whole, never truncated or partially emitted.
- Retry token nonces are random GCM nonces and are recorded as consumed on the
  first successful verification, giving single-use replay protection with TTL
  based expiry and a bounded table.
- A stateless reset is built only when `21 <= len(reset) < len(trigger)`; a
  datagram that is itself a recognized reset is answered with
  `ErrResetLoop`, so a reset can never trigger another reset.
- Reset tokens are derived with a server-private HMAC key, compared in
  constant time, and are only returned inside reset packets.

## Minimal usage

```go
guard, err := quicguard.NewGuard(quicguard.Config{
    TokenAEADKey: tokenKey, // 32 random bytes
    ResetHMACKey: hmacKey,  // >= 32 random bytes
})
if err != nil { /* ... */ }

func (s *Server) handle(src netip.AddrPort, dgram []byte) {
    res, err := guard.OnDatagram(src, dgram)
    if errors.Is(err, quicguard.ErrInitialTooSmall) {
        return // silently drop the short Initial
    }
    if err != nil { return }
    for _, p := range res.Drained {
        _ = s.udp.WriteToUDP(p.Data, udpAddr(src))
    }
    // Build the response, then gate it through the anti-amplification check.
    out, _ := guard.Send(src, response)
    if out != nil && out.Sent {
        _ = s.udp.WriteToUDP(response, udpAddr(src))
    } // else the response is suspended until validation drains the queue.
}
```

## Testing

`go test ./...` is fully offline and deterministic. The four mandated
adversarial cases live in `guard_adversarial_test.go`:

- `TestAdversarialInitial1199` — 1199/1198/100-byte Initials rejected,
  counters untouched, 1200-byte Initial accepted.
- `TestAdversarialThreeXBoundary` — exact `3*rx` boundary allowed, the next
  byte suspended whole, queue drained on validation.
- `TestAdversarialRetryTokenCrossIPReplay` — cross-IP, expiry, replay and AEAD
  tampering all rejected.
- `TestAdversarialStatelessResetLoop` — minimum-size rule, strict-shorter rule
  and refusal to reset another reset.
