// Package capnet is a capability-routing layer between a Go test host and
// untrusted security-test plug-ins.
//
// The test host is the only component that knows real network addresses. A
// plug-in never receives an address: it receives an opaque Handle minted by
// the host for one simulated endpoint inside one trial generation. Every I/O,
// timing and parsing operation the plug-in performs through its Router is
// served by the deterministic in-process simulator, so a misbehaving adapter
// cannot cause a real dial.
//
// Security guarantees:
//
//   - Handle has no exported fields and no exported constructor. The only way
//     to obtain one is Trial.Mint. An address string can never be turned back
//     into a handle, and Handle.String redacts every identifying component.
//   - Each handle is bound to a 128-bit random token, an endpoint id and a
//     trial generation. Zero, guessed or foreign handles fail every Router
//     operation with ErrBadHandle before any I/O is attempted; handles whose
//     generation has ended fail with ErrStaleHandle.
//   - The host's real dialer is captured privately and is unreachable from the
//     plug-in-facing API; the simulator never invokes it.
//   - All clocks are fake and advanced explicitly by the host. Timers and
//     deadlines observe only simulated time, never wall time or the runtime
//     network poller, and timers pending when a generation ends are stopped.
package capnet
