# nat64 — RFC 6146 stateful NAT64 + RFC 7915 translation

`nat64` is an importable, dependency-free Go package implementing the stateful
NAT64 gateway state machine (RFC 6146) together with the stateless IP/ICMP
translation algorithm (RFC 7915) and RFC 6052 address synthesis.

## Components

| File | Responsibility |
| --- | --- |
| `nat64.go` | Public types, `Config`, `Engine`, BIB/session value-copies, stats |
| `bib.go` | EIM BIB, multi-pool selection, range/parity-preserving port allocation, ref counts |
| `session.go` | Five-tuple session table, dual-direction keys, expiry sweep, BIB cascade cleanup |
| `tcp.go` | RFC 6146 six-state TCP table, event classification, per-state timers |
| `engine.go` / `engine_v4.go` | Packet ingress, flow creation, V4_INIT hold, simultaneous-open pairing |
| `headers.go` | IPv4/IPv6 parse & build, transport port rewrite, checksum recompute |
| `icmp.go` | ICMPv4/ICMPv6 type-code map, nested inner rewrite, MTU rules, echo sessions, Code 13 |
| `checksum.go` | RFC 1071 checksum, pseudo-headers, RFC 1624 incremental adjustment |
| `address.go` | RFC 6052 prefix synthesis/extraction (32/40/48/56/64/96) |

## Usage

```go
import "prescreen.local/ps-65414bcbe7/nat64"

eng, err := nat64.New(nat64.Config{
    PoolV4:  []netip.Addr{netip.MustParseAddr("192.0.2.10")},
    Now:     clock.Now,            // injectable; defaults to time.Now
    OnV4:    gateway.SendIPv4,     // translated IPv4 / local ICMPv4 errors
    OnV6:    gateway.SendIPv6,     // translated IPv6
})
if err != nil { /* ... */ }

eng.HandleV6(inboundIPv6)   // emits 0..n IPv4 packets via OnV4
eng.HandleV4(inboundIPv4)   // emits 0..n IPv6 packets via OnV6
eng.Sweep()                 // call from a timer; reaps expired sessions/BIBs
```

An `Engine` is single-goroutine; serialize calls (the table is plain maps, no
locks, which keeps the data plane hot path allocation-light).

## RFC 6146 behavior

- **EIM BIB.** One `(v6-addr, proto, port) -> (pool-addr, port)` binding is
  reused across every remote IPv4 destination. New ports preserve the source
  range (system 0..1023 vs user 1024..65535) and parity, scanning
  exact -> same-parity -> any-in-range -> other-range. Pools fill least-loaded
  first, so distinct clients never collide on a `(pool, port)` pair.
- **TCP six-state machine.** `V6_INIT`, `V4_INIT`, `V6_ESTABLISHED`,
  `V4_ESTABLISHED`, `V6_TRANS`, `V4_TRANS`. An unsolicited IPv4 SYN to a
  live BIB mapping enters `V4_INIT`, is cached, and is held for exactly
  `V4InitSynTimeout` (6 s default). A matching V6 SYN within the window pairs
  it to `V6_ESTABLISHED` and flushes both SYNs; on timeout the hold and its
  BIB reference are removed and ICMPv4 Destination Unreachable **Code 13** is
  returned. A SYN to a port with no BIB (a scan) is silently dropped.
- **Timers.** UDP 5 m inactivity (refreshed per packet), TCP established 2 h,
  TCP transitory/init 4 m, V4_INIT 6 s, ICMP 60 s. BIB entries are deleted when
  their session reference count reaches zero.

## RFC 7915 / ICMP behavior

- Outer ICMP type/code mapping in both directions per RFC 7915 section 5.
- PTB MTU: IPv6 -> IPv4 subtracts 20; IPv4 -> IPv6 adds 20 and is floored at
  1280.
- The embedded triggering packet is reverse-mapped through the session/BIB:
  inner IP addresses and the inner source port are rewritten, while the remote
  destination port is preserved. Because the inner packet may be truncated,
  transport checksums are adjusted incrementally per RFC 1624 (pseudo-header
  delta + port-word delta), and tests prove the delta equals a full
  recomputation over the untruncated packet.
- ICMP echo identifiers are modeled as pseudo-ports and get their own
  BIB/session entries with the 60 s ICMP timer.

## Tests

`go test ./...` covers the four acceptance areas: EIM cross-destination reuse
and parity/range allocation, V4_INIT 6 s pairing vs timeout Code 13, nested
ICMPv6 PTB five-tuple rewrite with MTU-20 (plus the reverse 1280 clamp), and
session timeouts with BIB reference-count cascade cleanup.
