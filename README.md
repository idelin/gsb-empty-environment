# WireGuard receiver index and handshake lifecycle

This package provides the index and handshake-lifecycle layer used by a Go
WireGuard tunnel host. It addresses the case where a concentrator deletes and
recreates peers quickly: a replay or late handshake response/cookie reply for
the old peer must never be associated with the new peer's keypair.

## Design

- A numeric `Index` is only a routing label.
- A `Binding` is the identity: device instance, peer static key, handshake
  generation, temporary key, random creation nonce, and opaque `BindingID`.
- `Capability` is a digest over that immutable tuple.
- States are `InitiationSent`, `ResponseAccepted`, `KeypairActive`, `Retired`,
  and `Quarantine`.
- `HandleResponse` transitions a binding only when its MAC, timestamp, and
  handshake hash all match.
- `HandleCookie` checks the cookie MAC, offending-message digest, binding
  generation, handshake hash, and original source address family. Port rebinding
  is controlled explicitly by `CookieNATPolicy`.
- An index is reclaimed only after the configured old-key message bound,
  `RejectAfterTime`, and all asynchronous `CryptoTask` work have completed.
- Async work captures a `*Binding`/`BindingID`; it never looks work up by index.
  A late task can only wipe the old binding's material.
- `ForceIndex` deliberately collides a numeric index for adversarial tests. The
  collision is resolved by authenticated message fields, never by merging
  capabilities.

## Basic flow

```go
m, _ := netsec.NewManager(netsec.Config{
    DeviceID: "concentrator-1",
    CookieNAT: netsec.CookieNATAllowPortRebind,
    Reclaim: &netsec.ReclaimPolicy{
        OldKeyMessageLimit:  4,
        RejectAfterMessages: 1 << 20,
        RejectAfterTime:     180 * time.Second,
    },
})

b, _ := m.Allocate(netsec.AllocationRequest{
    PeerPublicKey:      peerStaticKey,
    EphemeralPublicKey: handshakeEphemeral,
})

// After the host computes the authenticated response tuple:
m.SetResponseAuth(b, netsec.ResponseAuth{
    MAC:           responseMAC,
    Timestamp:     responseTimestamp,
    HandshakeHash: handshakeHash,
})

if got, err := m.HandleResponse(msg); err == nil && got == b {
    _ = m.Activate(got) // ResponseAccepted -> KeypairActive
}
```

On deletion or a completed rekey, call `RetireAndQuarantine` (or
`DeletePeer`/`RetireOtherGenerations`), account for delayed old-key messages
with `NoteLegacyMessage` or close the window with `SealLegacyMessages`, and
periodically call `Sweep`. `ReclaimStatus` reports every open gate.

## Tests

`go test -race ./...` covers forced index collision, peer delete/recreate,
late cookie and response, concurrent response acceptance, NAT port/IP
rebinding policy, empty/active async-task gating, and legitimate rekey reuse of
the same numeric index without identity reuse.
